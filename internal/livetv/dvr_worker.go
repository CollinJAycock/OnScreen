package livetv

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DVRWorkerConfig configures the recording worker. RecordDir is the
// on-disk destination; should live under the DVR library's scan path so
// the scanner picks up finalized .mp4 files on its next pass.
type DVRWorkerConfig struct {
	RecordDir string
	FFmpegBin string
}

// DVRLibraryResolver returns the UUID of the library recordings should
// land in. Wired to settings at startup; may return uuid.Nil which the
// worker treats as "recording captured but not linked to a library
// row" — the file stays on disk but media_items isn't populated until
// a library is configured.
type DVRLibraryResolver func(ctx context.Context) (uuid.UUID, error)

// DVRMediaCreator creates the media_items row for a finalized recording.
// Interface lives here because cross-package wiring in main.go supplies
// the concrete implementation (it touches media.Service which we can't
// import from livetv without a cycle).
type DVRMediaCreator interface {
	CreateDVRMediaItem(ctx context.Context, p DVRMediaItemParams) (uuid.UUID, error)
}

// DVRMediaItemParams is what the media creator needs to build a
// media_items row for a recording.
type DVRMediaItemParams struct {
	LibraryID  uuid.UUID
	Title      string
	Subtitle   *string
	SeasonNum  *int32
	EpisodeNum *int32
	FilePath   string
	AiredAt    *time.Time
}

// DVRWorker captures scheduled recordings. Polls every few seconds for
// recordings whose starts_at is imminent, spins up ffmpeg to capture
// each, and on completion creates a media_items row so the recording
// appears in the user's library.
type DVRWorker struct {
	cfg      DVRWorkerConfig
	q        DVRQuerier
	live     *Service
	lib      DVRLibraryResolver
	media    DVRMediaCreator
	logger   structuredLogger

	mu     sync.Mutex
	active map[uuid.UUID]*captureSession
}

type captureSession struct {
	recID    uuid.UUID
	cancel   context.CancelFunc
	cmd      *exec.Cmd
	upstream Stream
	filePath string
}

// NewDVRWorker wires the worker. lib and media can be nil during tests
// that exercise only the scheduling path — the capture loop skips rows
// it can't finalize and emits a warning.
func NewDVRWorker(cfg DVRWorkerConfig, q DVRQuerier, live *Service, lib DVRLibraryResolver, media DVRMediaCreator, logger structuredLogger) *DVRWorker {
	if cfg.FFmpegBin == "" {
		cfg.FFmpegBin = "ffmpeg"
	}
	return &DVRWorker{
		cfg: cfg, q: q, live: live, lib: lib, media: media, logger: logger,
		active: make(map[uuid.UUID]*captureSession),
	}
}

// Run loops until the context is cancelled. Every tick:
//  1. Pick up any scheduled recordings whose starts_at has passed → tune
//     them and start capturing.
//  2. Check in-flight captures for completion → finalize to media_items.
//  3. Honor cancellations — if a recording transitioned out of
//     'recording' (cancel button in UI), stop the ffmpeg process.
//
// One-tick-per-5-seconds is sufficient: ffmpeg captures exit on their
// own when their context times out at ends_at; we don't need per-second
// polling.
func (w *DVRWorker) Run(ctx context.Context, tickInterval time.Duration) error {
	if tickInterval <= 0 {
		tickInterval = 5 * time.Second
	}
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.shutdownActive()
			return ctx.Err()
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// tick handles one iteration. Errors within tick are logged but don't
// stop the worker — a transient DB blip shouldn't kill DVR.
func (w *DVRWorker) tick(ctx context.Context) {
	w.startDueRecordings(ctx)
	w.reapFinishedCaptures(ctx)
	w.honorCancellations(ctx)
}

func (w *DVRWorker) startDueRecordings(ctx context.Context) {
	due, err := w.q.ListDueRecordings(ctx, time.Now().UTC())
	if err != nil {
		w.logger.WarnContext(ctx, "list due recordings", "err", err)
		return
	}
	for _, r := range due {
		w.mu.Lock()
		_, alreadyActive := w.active[r.ID]
		w.mu.Unlock()
		if alreadyActive {
			continue
		}
		if err := w.beginCapture(ctx, r); err != nil {
			w.logger.ErrorContext(ctx, "begin capture",
				"recording_id", r.ID, "err", err)
			_ = w.q.SetRecordingFailed(ctx, r.ID, err.Error())
		}
	}
}

// beginCapture tunes the channel, starts ffmpeg remuxing to MP4, and
// registers the session so the reaper can finalize when ffmpeg exits.
// The capture context is derived from a session-scoped background so
// a tick's cancellation doesn't kill in-flight captures.
func (w *DVRWorker) beginCapture(_ context.Context, r Recording) error {
	if err := os.MkdirAll(w.cfg.RecordDir, 0o755); err != nil {
		return fmt.Errorf("record dir: %w", err)
	}
	// Destination filename: title + start time, colons replaced with
	// dashes for Windows compatibility. Scanner uses filename-based
	// matching, so keep it descriptive.
	safeTitle := safeFilename(r.Title)
	path := filepath.Join(w.cfg.RecordDir,
		fmt.Sprintf("%s - %s.mp4", safeTitle, r.StartsAt.UTC().Format("2006-01-02 1504")))

	captureCtx, cancel := context.WithDeadline(context.Background(), r.EndsAt.Add(30*time.Second))
	upstream, err := w.live.OpenChannelStream(captureCtx, r.ChannelID)
	if err != nil {
		cancel()
		return fmt.Errorf("open channel stream: %w", err)
	}

	// Remux MPEG-TS → MP4. Stream-copy keeps CPU near zero since the
	// live-TV pipeline's HDHomeRun tune yields standard broadcast
	// codecs. If codec compatibility becomes an issue we can promote to
	// transcode here too — mirrors the HLS proxy's approach.
	cmd := exec.CommandContext(captureCtx, w.cfg.FFmpegBin,
		"-fflags", "+genpts+discardcorrupt",
		"-i", "pipe:0",
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-sn",
		"-c", "copy",
		"-movflags", "+faststart",
		"-f", "mp4",
		path,
	)
	cmd.Stdin = upstream
	// Pipe stderr to discard for now; failures surface via exit code.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		upstream.Close()
		cancel()
		return fmt.Errorf("ffmpeg start: %w", err)
	}

	if err := w.q.SetRecordingStartedFile(captureCtx, r.ID, path); err != nil {
		cancel()
		upstream.Close()
		_ = cmd.Process.Kill()
		return fmt.Errorf("mark recording started: %w", err)
	}

	session := &captureSession{
		recID: r.ID, cancel: cancel, cmd: cmd,
		upstream: upstream, filePath: path,
	}
	w.mu.Lock()
	w.active[r.ID] = session
	w.mu.Unlock()
	w.logger.InfoContext(captureCtx, "dvr capture started",
		"recording_id", r.ID, "title", r.Title,
		"channel_id", r.ChannelID, "ends_at", r.EndsAt)
	return nil
}

// reapFinishedCaptures walks in-flight captures, and finalizes any
// whose ffmpeg process has exited. Finalize = create media_items row,
// link it, close upstream, drop from active map.
func (w *DVRWorker) reapFinishedCaptures(ctx context.Context) {
	w.mu.Lock()
	candidates := make([]*captureSession, 0, len(w.active))
	for _, s := range w.active {
		if s.cmd.ProcessState != nil {
			candidates = append(candidates, s)
		}
	}
	w.mu.Unlock()

	for _, s := range candidates {
		w.finalize(ctx, s)
	}
}

func (w *DVRWorker) finalize(ctx context.Context, s *captureSession) {
	s.cancel()
	s.upstream.Close()

	// Drop from active map regardless of outcome.
	defer func() {
		w.mu.Lock()
		delete(w.active, s.recID)
		w.mu.Unlock()
	}()

	exitErr := s.cmd.ProcessState.ExitCode()
	if exitErr != 0 && exitErr != 255 {
		// 255 happens when ffmpeg is killed via context cancel at
		// end-of-recording — not actually a failure. Any other non-zero
		// exit code we treat as a failure.
		w.logger.WarnContext(ctx, "ffmpeg exited non-zero",
			"recording_id", s.recID, "exit_code", exitErr)
		_ = w.q.SetRecordingFailed(ctx, s.recID,
			fmt.Sprintf("ffmpeg exit %d", exitErr))
		return
	}

	// Sanity: did the file actually get written?
	fi, err := os.Stat(s.filePath)
	if err != nil || fi.Size() == 0 {
		w.logger.WarnContext(ctx, "dvr capture produced empty file",
			"recording_id", s.recID, "path", s.filePath)
		_ = w.q.SetRecordingFailed(ctx, s.recID, "capture produced empty file")
		return
	}

	if w.lib == nil || w.media == nil {
		// No library/media wiring — worker is running in a test or the
		// library isn't configured yet. File is on disk; mark completed
		// without an item_id.
		_ = w.q.SetRecordingStatus(ctx, s.recID, RecordingStatusCompleted)
		return
	}
	libID, err := w.lib(ctx)
	if err != nil || libID == uuid.Nil {
		w.logger.WarnContext(ctx, "no DVR library configured",
			"recording_id", s.recID, "err", err)
		_ = w.q.SetRecordingStatus(ctx, s.recID, RecordingStatusCompleted)
		return
	}

	rec, err := w.q.GetRecording(ctx, s.recID)
	if err != nil {
		w.logger.ErrorContext(ctx, "get recording for finalize",
			"recording_id", s.recID, "err", err)
		return
	}
	itemID, err := w.media.CreateDVRMediaItem(ctx, DVRMediaItemParams{
		LibraryID:  libID,
		Title:      rec.Title,
		Subtitle:   rec.Subtitle,
		SeasonNum:  rec.SeasonNum,
		EpisodeNum: rec.EpisodeNum,
		FilePath:   s.filePath,
		AiredAt:    &rec.StartsAt,
	})
	if err != nil {
		w.logger.ErrorContext(ctx, "create dvr media item",
			"recording_id", s.recID, "err", err)
		_ = w.q.SetRecordingFailed(ctx, s.recID, fmt.Sprintf("media item: %v", err))
		return
	}
	if err := w.q.SetRecordingCompleted(ctx, s.recID, itemID); err != nil {
		w.logger.ErrorContext(ctx, "mark recording completed",
			"recording_id", s.recID, "err", err)
	}
	w.logger.InfoContext(ctx, "dvr capture finalized",
		"recording_id", s.recID, "item_id", itemID, "path", s.filePath)
}

// honorCancellations kills captures whose DB status has moved out of
// 'recording' (e.g. user clicked cancel in the UI).
func (w *DVRWorker) honorCancellations(ctx context.Context) {
	w.mu.Lock()
	activeIDs := make([]uuid.UUID, 0, len(w.active))
	for id := range w.active {
		activeIDs = append(activeIDs, id)
	}
	w.mu.Unlock()
	for _, id := range activeIDs {
		r, err := w.q.GetRecording(ctx, id)
		if err != nil {
			continue
		}
		if r.Status != RecordingStatusRecording {
			w.mu.Lock()
			s, ok := w.active[id]
			w.mu.Unlock()
			if ok {
				w.logger.InfoContext(ctx, "killing capture (cancelled)",
					"recording_id", id)
				s.cancel()
			}
		}
	}
}

func (w *DVRWorker) shutdownActive() {
	w.mu.Lock()
	sessions := make([]*captureSession, 0, len(w.active))
	for _, s := range w.active {
		sessions = append(sessions, s)
	}
	w.mu.Unlock()
	for _, s := range sessions {
		s.cancel()
		s.upstream.Close()
	}
}

// safeFilename replaces characters that are problematic on Windows or
// in URLs with hyphens. Preserves spaces — most library scanners
// tolerate them and they read better in the UI.
func safeFilename(s string) string {
	bad := []rune{'<', '>', ':', '"', '/', '\\', '|', '?', '*'}
	isBad := func(r rune) bool {
		for _, b := range bad {
			if r == b {
				return true
			}
		}
		return false
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if isBad(r) {
			out = append(out, '-')
		} else {
			out = append(out, r)
		}
	}
	if len(out) > 180 {
		out = out[:180]
	}
	return string(out)
}
