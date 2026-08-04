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
	cfg    DVRWorkerConfig
	q      DVRQuerier
	live   dvrStreamSource
	lib    DVRLibraryResolver
	media  DVRMediaCreator
	logger structuredLogger

	mu     sync.Mutex
	active map[uuid.UUID]*captureSession
}

type captureSession struct {
	recID    uuid.UUID
	cancel   context.CancelFunc
	cmd      *exec.Cmd
	upstream *onceCloser
	filePath string
	// endTimer fires at the recording's end and closes upstream so ffmpeg
	// sees EOF and FINALIZES the MP4 — see beginCapture for why this must
	// never be a process kill.
	endTimer *time.Timer
	// done is closed by the per-capture reaper goroutine once cmd.Wait()
	// returns. The reaper MUST call Wait — only then is cmd.ProcessState
	// populated (exec.CommandContext kills on ctx but never reaps), and
	// without it the exited ffmpeg zombies and finalize() never runs.
	done chan struct{}
	// itemID is stamped once CreateDVRMediaItem succeeds so a finalize retry
	// after a failed status write can't create a duplicate library item.
	itemID uuid.UUID
	// finalizeFirstErr marks when finalize first hit a transient error; the
	// session is retried each tick until dvrFinalizeRetryWindow elapses.
	finalizeFirstErr time.Time
}

// onceCloser makes a Stream safe to close from any of the paths that race to
// do it — the end-of-recording timer, cancellation, finalize, and shutdown.
type onceCloser struct {
	Stream
	once sync.Once
}

func (c *onceCloser) Close() error {
	c.once.Do(func() { _ = c.Stream.Close() })
	return nil
}

// dvrEndGrace is how far past ends_at the capture keeps reading before the
// upstream is closed — absorbs clock skew and lets the last GOP land.
const dvrEndGrace = 30 * time.Second

// dvrFinalizeKillGrace is how long after the upstream closes ffmpeg gets to
// finish writing the MP4 before the context backstop hard-kills it. The
// faststart pass rewrites the whole file at the end, and a multi-hour HD
// recording is gigabytes — give it real time.
const dvrFinalizeKillGrace = 10 * time.Minute

// dvrFinalizeRetryWindow is how long finalize retries transient DB failures
// before giving up and marking the recording failed. Single-shot finalize
// stranded recordings in 'recording' on any blip, and the startup reconcile
// then mislabelled a perfectly good capture as failed.
const dvrFinalizeRetryWindow = 10 * time.Minute

// dvrStreamSource is the one Service capability the capture path needs.
// An interface so worker tests can stub the tuner layer; *Service satisfies it.
type dvrStreamSource interface {
	OpenChannelStream(ctx context.Context, channelID uuid.UUID) (Stream, error)
}

// NewDVRWorker wires the worker. lib and media can be nil during tests
// that exercise only the scheduling path — the capture loop skips rows
// it can't finalize and emits a warning.
func NewDVRWorker(cfg DVRWorkerConfig, q DVRQuerier, live dvrStreamSource, lib DVRLibraryResolver, media DVRMediaCreator, logger structuredLogger) *DVRWorker {
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
	// Reconcile recordings left in 'recording' by a previous process (crash, or
	// a graceful shutdown that cancelled in-flight captures without finalizing).
	// Without this they stay wedged in 'recording' forever — never finalized,
	// never retried — and UpsertRecording can't re-arm the slot. ffmpeg was
	// SIGKILLed so the partial .mp4 is likely unplayable; mark them failed (the
	// file stays on disk for manual recovery) rather than risk a corrupt item.
	w.reconcileInterrupted(ctx)
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
			// A start failure is only PERMANENT once the show is over. Tuner
			// busy, broadcaster not yet live, a tuner mid-reboot — all clear up
			// in seconds to minutes, and the row is still 'scheduled', so the
			// next tick retries and captures the remainder of the program.
			// Failing on the first attempt threw away entire recordings for a
			// hiccup at exactly starts_at.
			if time.Now().Before(r.EndsAt) {
				w.logger.WarnContext(ctx, "begin capture failed; will retry while the program airs",
					"recording_id", r.ID, "ends_at", r.EndsAt, "err", err)
				continue
			}
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

	// The deadline is a KILL BACKSTOP, not the end of the recording. The
	// recording ends by CLOSING THE UPSTREAM (see the endTimer below): ffmpeg
	// reads EOF, writes the moov atom, runs the +faststart rewrite, and exits
	// 0 with a playable file. exec.CommandContext's kill is a hard kill —
	// ending the recording that way truncated the MP4 before its moov was
	// written, so EVERY recording that ran to its scheduled end was an
	// unplayable file marked failed. The deadline only fires if ffmpeg wedges
	// after EOF.
	captureCtx, cancel := context.WithDeadline(context.Background(),
		r.EndsAt.Add(dvrEndGrace+dvrFinalizeKillGrace))
	rawUpstream, err := w.live.OpenChannelStream(captureCtx, r.ChannelID)
	if err != nil {
		cancel()
		return fmt.Errorf("open channel stream: %w", err)
	}
	upstream := &onceCloser{Stream: rawUpstream}

	// Remux source → MP4. Stream-copy keeps CPU near zero: HDHomeRun/IPTV
	// tunes yield standard broadcast codecs and RTMP broadcasts are already
	// H.264/AAC. If codec compatibility becomes an issue we can promote to
	// transcode here too — mirrors the HLS proxy's approach.
	//
	// Optional input-format hint: TS tuners autodetect; the RTMP broadcast
	// reader returns "flv" so ffmpeg doesn't have to probe a live pipe.
	ffArgs := []string{"-fflags", "+genpts+discardcorrupt"}
	// Probe the RAW upstream for the hint — the once-closer wrapper is a
	// concrete type and would hide the interface.
	if hinter, ok := rawUpstream.(inputFormatHinter); ok {
		if f := hinter.FFmpegInputFormat(); f != "" {
			ffArgs = append(ffArgs, "-f", f)
		}
	}
	ffArgs = append(ffArgs,
		"-i", "pipe:0",
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-sn",
		"-c", "copy",
		"-movflags", "+faststart",
		"-f", "mp4",
		path,
	)
	cmd := exec.CommandContext(captureCtx, w.cfg.FFmpegBin, ffArgs...)
	cmd.Stdin = upstream
	// Pipe stderr to discard for now; failures surface via exit code.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		upstream.Close()
		cancel()
		return fmt.Errorf("ffmpeg start: %w", err)
	}

	session := &captureSession{
		recID: r.ID, cancel: cancel, cmd: cmd,
		upstream: upstream, filePath: path,
		done: make(chan struct{}),
	}
	// Reap the child as soon as it exits (end-of-recording EOF, failure, or
	// cancel). This is what populates cmd.ProcessState so reapFinishedCaptures
	// can finalize; without it the process zombies and the recording is stuck
	// forever. Spawned right after Start so it reaps even if the steps below fail.
	go func() {
		_ = cmd.Wait()
		close(session.done)
	}()

	// End the recording GRACEFULLY at its scheduled end: closing the upstream
	// gives ffmpeg EOF, so it flushes, writes the moov atom, does the
	// faststart rewrite, and exits 0 with a playable file. This timer — not
	// the context deadline — is what actually ends a normal recording.
	session.endTimer = time.AfterFunc(time.Until(r.EndsAt.Add(dvrEndGrace)), func() {
		w.logger.InfoContext(context.Background(), "dvr capture reached scheduled end; closing upstream for finalize",
			"recording_id", r.ID)
		_ = upstream.Close()
	})

	started, err := w.q.SetRecordingStartedFile(captureCtx, r.ID, path)
	if err != nil {
		session.endTimer.Stop()
		cancel() // ffmpeg dies on ctx cancel; the reaper goroutine above Wait()s it
		upstream.Close()
		return fmt.Errorf("mark recording started: %w", err)
	}
	if !started {
		// The row left 'scheduled' while we were tuning — the user cancelled
		// (or another process claimed it). Stand down without touching the
		// row: overwriting that transition is exactly the race the guarded
		// UPDATE exists to close.
		w.logger.InfoContext(captureCtx, "recording no longer scheduled; standing capture down",
			"recording_id", r.ID)
		session.endTimer.Stop()
		cancel()
		upstream.Close()
		_ = os.Remove(path)
		return nil
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
		// done closed ⇒ cmd.Wait() returned ⇒ ProcessState is populated and the
		// process is reaped. Non-blocking: still-running captures are skipped.
		select {
		case <-s.done:
			candidates = append(candidates, s)
		default:
		}
	}
	w.mu.Unlock()

	for _, s := range candidates {
		w.finalize(ctx, s)
	}
}

// finalize runs once per tick for an exited capture until it reaches a
// TERMINAL outcome. Transient DB failures leave the session in the active map
// so the next tick retries — finalize used to be single-shot, so one blip
// stranded the recording in 'recording' until a restart, whose reconcile pass
// then mislabelled the (perfectly good) capture as failed. Retrying is bounded
// by dvrFinalizeRetryWindow.
func (w *DVRWorker) finalize(ctx context.Context, s *captureSession) {
	s.endTimer.Stop()
	s.cancel()
	s.upstream.Close()

	done := w.tryFinalize(ctx, s)
	if !done {
		if s.finalizeFirstErr.IsZero() {
			s.finalizeFirstErr = time.Now()
		}
		if time.Since(s.finalizeFirstErr) < dvrFinalizeRetryWindow {
			return // stay in active; next tick retries
		}
		w.logger.ErrorContext(ctx, "finalize retries exhausted; marking failed",
			"recording_id", s.recID)
		_ = w.q.SetRecordingFailed(ctx, s.recID, "finalize retries exhausted")
	}
	w.mu.Lock()
	delete(w.active, s.recID)
	w.mu.Unlock()
}

// tryFinalize reports whether the session reached a terminal outcome
// (completed / failed / cancelled). false = transient failure, retry.
func (w *DVRWorker) tryFinalize(ctx context.Context, s *captureSession) bool {
	// Don't resurrect a recording the user moved out of 'recording' (cancel).
	// A cancelled recording must stay cancelled: no flip to completed/failed,
	// no library item for the partial capture. Keyed on DB status, not exit
	// code.
	rec, err := w.q.GetRecording(ctx, s.recID)
	if err != nil {
		w.logger.WarnContext(ctx, "get recording for finalize (will retry)",
			"recording_id", s.recID, "err", err)
		return false
	}
	if rec.Status != RecordingStatusRecording {
		w.logger.InfoContext(ctx, "dvr capture not finalized (no longer recording)",
			"recording_id", s.recID, "status", rec.Status)
		return true
	}

	// A recording that ended at its scheduled time now exits 0: the end timer
	// closes the upstream, ffmpeg sees EOF and finalizes the MP4 cleanly. Any
	// non-zero exit means the kill backstop fired or ffmpeg genuinely died —
	// either way the file has no moov atom and is not worth a library item.
	// (The old code blessed exit 255 because the DEADLINE KILL was the normal
	// end of every recording; that path no longer exists.)
	if code := s.cmd.ProcessState.ExitCode(); code != 0 {
		w.logger.WarnContext(ctx, "ffmpeg exited non-zero",
			"recording_id", s.recID, "exit_code", code)
		_ = w.q.SetRecordingFailed(ctx, s.recID, fmt.Sprintf("ffmpeg exit %d", code))
		return true
	}

	// Sanity: did the file actually get written?
	fi, err := os.Stat(s.filePath)
	if err != nil || fi.Size() == 0 {
		w.logger.WarnContext(ctx, "dvr capture produced empty file",
			"recording_id", s.recID, "path", s.filePath)
		_ = w.q.SetRecordingFailed(ctx, s.recID, "capture produced empty file")
		return true
	}

	if w.lib == nil || w.media == nil {
		// No library/media wiring — worker is running in a test or the
		// library isn't configured yet. File is on disk; mark completed
		// without an item_id.
		return w.markCompleted(ctx, s.recID, uuid.Nil)
	}
	libID, err := w.lib(ctx)
	if err != nil {
		w.logger.WarnContext(ctx, "resolve DVR library (will retry)",
			"recording_id", s.recID, "err", err)
		return false
	}
	if libID == uuid.Nil {
		w.logger.WarnContext(ctx, "no DVR library configured",
			"recording_id", s.recID)
		return w.markCompleted(ctx, s.recID, uuid.Nil)
	}

	// Idempotent across retries: create the item once, remember it on the
	// session, and retry only the status write after that — re-running the
	// create would file a duplicate library item for the same recording.
	if s.itemID == uuid.Nil {
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
			w.logger.WarnContext(ctx, "create dvr media item (will retry)",
				"recording_id", s.recID, "err", err)
			return false
		}
		s.itemID = itemID
	}
	if !w.markCompleted(ctx, s.recID, s.itemID) {
		return false
	}
	w.logger.InfoContext(ctx, "dvr capture finalized",
		"recording_id", s.recID, "item_id", s.itemID, "path", s.filePath)
	return true
}

// markCompleted writes the completed status (with or without an item link).
// Returns false on error so the caller retries next tick.
func (w *DVRWorker) markCompleted(ctx context.Context, recID, itemID uuid.UUID) bool {
	var err error
	if itemID == uuid.Nil {
		err = w.q.SetRecordingStatus(ctx, recID, RecordingStatusCompleted)
	} else {
		err = w.q.SetRecordingCompleted(ctx, recID, itemID)
	}
	if err != nil {
		w.logger.WarnContext(ctx, "mark recording completed (will retry)",
			"recording_id", recID, "err", err)
		return false
	}
	return true
}

// reconcileInterrupted marks any recording still in 'recording' status at
// startup as failed — those are orphans from a previous process that died (or
// shut down) mid-capture, since this fresh worker holds no active captures yet.
func (w *DVRWorker) reconcileInterrupted(ctx context.Context) {
	active, err := w.q.ListActiveRecordings(ctx)
	if err != nil {
		w.logger.WarnContext(ctx, "dvr: list active recordings for startup reconcile", "err", err)
		return
	}
	for _, r := range active {
		w.logger.WarnContext(ctx, "dvr: marking interrupted recording failed (mid-capture restart)",
			"recording_id", r.ID, "title", r.Title)
		if err := w.q.SetRecordingFailed(ctx, r.ID, "interrupted by server restart"); err != nil {
			w.logger.WarnContext(ctx, "dvr: reconcile set-failed", "recording_id", r.ID, "err", err)
		}
	}
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
				w.logger.InfoContext(ctx, "stopping capture (cancelled)",
					"recording_id", id)
				// Close the upstream rather than killing ffmpeg: EOF lets it
				// finalize the moov atom, so the partial file left on disk is
				// at least playable. finalize's status gate keeps a cancelled
				// row cancelled — no item is created for it. The context
				// deadline still backstops a wedged ffmpeg.
				s.endTimer.Stop()
				_ = s.upstream.Close()
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
		s.endTimer.Stop()
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
