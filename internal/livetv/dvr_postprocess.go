package livetv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ── DVR post-processing ──────────────────────────────────────────────────────
//
// A capture is a stream-copied MP4 straight off the tuner. When post-processing
// is enabled, finishing a capture queues a job that, one recording at a time:
//
//  1. remuxes (or transcodes) the capture into a temporary file and verifies
//     it with ffprobe — duration within 2% of the source, streams present;
//  2. detects commercials (comskip when configured, else black + silence);
//  3. in cut mode, stream-copies the programme segments into a second
//     temporary file and verifies that against the expected length;
//  4. swaps the result in: the capture is moved aside (or deleted), the new
//     file takes its place, and the recording + media_files row are re-pointed
//     in one transaction so the library item, its watch state and its markers
//     stay attached;
//  5. runs the operator's script (ONSCREEN_DVR_POSTPROCESS_SCRIPT), if any.
//
// Any failure before step 4 leaves the capture exactly as it was and the
// recording playable; temporary files are always removed.

// PostProcessJobKind is dvr_postprocess_jobs.kind.
type PostProcessJobKind string

const (
	// PostProcessFull is the whole pipeline, queued when a capture finishes.
	PostProcessFull PostProcessJobKind = "full"
	// PostProcessDetect runs commercial detection only and never touches the
	// file (the recording menu's "Detect commercials").
	PostProcessDetect PostProcessJobKind = "detect"
)

// PostProcessStatus is dvr_postprocess_jobs.status.
type PostProcessStatus string

const (
	PostProcessQueued     PostProcessStatus = "queued"
	PostProcessProcessing PostProcessStatus = "processing"
	PostProcessProcessed  PostProcessStatus = "processed"
	PostProcessFailed     PostProcessStatus = "failed"
)

// ErrPostProcessBusy is returned by Enqueue when the recording already has a
// queued or running job.
var ErrPostProcessBusy = errors.New("post-processing is already queued or running for this recording")

// ErrPostProcessIneligible is returned by Enqueue for a recording that has no
// finished capture to work on, or a full re-run of one already processed.
var ErrPostProcessIneligible = errors.New("recording can't be post-processed")

// PostProcessSettings is the admin's DVR post-processing configuration. The
// zero value is today's behaviour: nothing happens after capture.
type PostProcessSettings struct {
	Remux          bool
	Container      string // "mkv" (default) | "mp4"
	Transcode      bool
	Codec          string // "h264" (default) | "hevc"
	Quality        string // "high" | "balanced" (default) | "small"
	DeleteOriginal bool
	Commercials    CommercialMode
}

// rewrites reports whether the settings produce a new file.
func (s PostProcessSettings) rewrites() bool { return s.Remux || s.Transcode }

// PostProcessJob is one claimed job.
type PostProcessJob struct {
	RecordingID      uuid.UUID
	Kind             PostProcessJobKind
	Status           PostProcessStatus
	OriginalFilePath *string
}

// SwapFileParams commits a processed file. FileID is uuid.Nil when the
// recording has no library item (no DVR library configured).
type SwapFileParams struct {
	RecordingID  uuid.UUID
	FileID       uuid.UUID
	NewPath      string
	Size         int64
	Container    string
	VideoCodec   string
	AudioCodec   string
	DurationMS   int64
	OriginalPath *string // where the kept capture now lives; nil = deleted
}

// PostProcessStore is the persistence the post-processor needs. Implemented
// over sqlc in cmd/server.
type PostProcessStore interface {
	GetRecording(ctx context.Context, id uuid.UUID) (Recording, error)
	GetPostProcessJob(ctx context.Context, recordingID uuid.UUID) (PostProcessJob, bool, error)
	// EnqueuePostProcess returns false when a job is already queued/running.
	EnqueuePostProcess(ctx context.Context, recordingID uuid.UUID, kind PostProcessJobKind) (bool, error)
	// ClaimNextPostProcess returns ok=false when the queue is empty.
	ClaimNextPostProcess(ctx context.Context) (PostProcessJob, bool, error)
	SetPostProcessMessage(ctx context.Context, recordingID uuid.UUID, msg string) error
	FinishPostProcess(ctx context.Context, recordingID uuid.UUID, status PostProcessStatus, msg string) error
	FailInterruptedPostProcess(ctx context.Context) (int64, error)
	// FindMediaFileID returns uuid.Nil when no media_files row has path.
	FindMediaFileID(ctx context.Context, path string) (uuid.UUID, error)
	// SwapRecordingFile re-points the recording, its media_files row and the
	// job's kept-original path in ONE transaction.
	SwapRecordingFile(ctx context.Context, p SwapFileParams) error
	// ReplaceCommercialMarkers replaces every break stored for fileID.
	ReplaceCommercialMarkers(ctx context.Context, fileID uuid.UUID, source string, breaks []CommercialBreak) error
}

// PostProcessConfig wires the post-processor. Executable paths come only from
// the environment (see internal/config) — never from settings an admin can
// edit in the UI, which would make an admin session remote code execution.
type PostProcessConfig struct {
	FFmpegBin  string
	FFprobeBin string
	// ScriptPath is ONSCREEN_DVR_POSTPROCESS_SCRIPT; run with the final
	// recording path as argv[1]. "" = no script.
	ScriptPath    string
	ScriptTimeout time.Duration
	// ComskipPath / ComskipINI are ONSCREEN_COMSKIP_PATH / _INI. "" = use the
	// built-in heuristic.
	ComskipPath string
	ComskipINI  string
	// Settings returns the current admin settings; read at each job so a
	// change applies without a restart.
	Settings func(ctx context.Context) PostProcessSettings
	// Encoders returns the server's active encoder names (hardware first).
	// nil = software only.
	Encoders func() []string
	// Heuristic overrides DefaultHeuristicParams (tests use tiny media).
	Heuristic *HeuristicParams
	// PollInterval is how often an idle runner re-checks the queue (it is
	// also woken directly by Enqueue).
	PollInterval time.Duration
}

// OriginalsDirName is the folder, beside a recording, that kept captures move
// into after processing. The scanner skips it, so a kept capture never shows
// up as a second copy of the recording.
const OriginalsDirName = ".dvr-originals"

// partSuffix marks post-processing temp files. The scanner only indexes known
// media extensions, so a half-written ".part" is never picked up.
const partSuffix = ".part"

const defaultScriptTimeout = 30 * time.Minute

// PostProcessor runs post-processing jobs one at a time.
type PostProcessor struct {
	cfg    PostProcessConfig
	store  PostProcessStore
	logger structuredLogger
	wake   chan struct{}
}

// NewPostProcessor builds the runner; call Run in a goroutine.
func NewPostProcessor(cfg PostProcessConfig, store PostProcessStore, logger structuredLogger) *PostProcessor {
	if cfg.FFmpegBin == "" {
		cfg.FFmpegBin = "ffmpeg"
	}
	if cfg.FFprobeBin == "" {
		cfg.FFprobeBin = "ffprobe"
	}
	if cfg.ScriptTimeout <= 0 {
		cfg.ScriptTimeout = defaultScriptTimeout
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30 * time.Second
	}
	if cfg.Settings == nil {
		cfg.Settings = func(context.Context) PostProcessSettings { return PostProcessSettings{} }
	}
	return &PostProcessor{cfg: cfg, store: store, logger: logger, wake: make(chan struct{}, 1)}
}

// ScriptConfigured reports whether ONSCREEN_DVR_POSTPROCESS_SCRIPT is set.
func (p *PostProcessor) ScriptConfigured() bool { return p.cfg.ScriptPath != "" }

// QueueAfterCapture is called by the capture worker when a recording
// completes. It queues a full job only when the settings (or the script) ask
// for any work, so with everything off nothing changes.
func (p *PostProcessor) QueueAfterCapture(ctx context.Context, recordingID uuid.UUID) {
	s := p.cfg.Settings(ctx)
	if !s.rewrites() && s.Commercials != CommercialsMark && s.Commercials != CommercialsCut && p.cfg.ScriptPath == "" {
		return
	}
	if _, err := p.store.EnqueuePostProcess(ctx, recordingID, PostProcessFull); err != nil {
		p.logger.WarnContext(ctx, "dvr post-process: enqueue after capture", "recording_id", recordingID, "err", err)
		return
	}
	p.poke()
}

// Enqueue queues a job on request (the recording admin menu). A full re-run
// is only allowed for a recording whose last run failed or that never ran:
// re-processing a processed file would transcode it twice.
func (p *PostProcessor) Enqueue(ctx context.Context, recordingID uuid.UUID, kind PostProcessJobKind) error {
	rec, err := p.store.GetRecording(ctx, recordingID)
	if err != nil {
		return fmt.Errorf("get recording: %w", err)
	}
	if rec.Status != RecordingStatusCompleted || rec.FilePath == nil || *rec.FilePath == "" {
		return fmt.Errorf("%w: only a completed recording can be processed", ErrPostProcessIneligible)
	}
	if kind == PostProcessFull {
		job, ok, err := p.store.GetPostProcessJob(ctx, recordingID)
		if err != nil {
			return fmt.Errorf("get job: %w", err)
		}
		if ok && job.Status == PostProcessProcessed && job.Kind == PostProcessFull {
			return fmt.Errorf("%w: it has already been processed", ErrPostProcessIneligible)
		}
	}
	queued, err := p.store.EnqueuePostProcess(ctx, recordingID, kind)
	if err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	if !queued {
		return ErrPostProcessBusy
	}
	p.poke()
	return nil
}

func (p *PostProcessor) poke() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Run drains the queue until ctx is cancelled. Jobs left 'processing' by a
// previous process are failed first (their temp output is abandoned; the
// capture was never touched).
func (p *PostProcessor) Run(ctx context.Context) error {
	if n, err := p.store.FailInterruptedPostProcess(ctx); err != nil {
		p.logger.WarnContext(ctx, "dvr post-process: reconcile interrupted jobs", "err", err)
	} else if n > 0 {
		p.logger.WarnContext(ctx, "dvr post-process: failed jobs interrupted by restart", "count", n)
	}
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	for {
		for p.runOne(ctx) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		case <-p.wake:
		}
	}
}

// runOne claims and runs one job. Returns true when a job was run, so Run
// keeps draining.
func (p *PostProcessor) runOne(ctx context.Context) bool {
	job, ok, err := p.store.ClaimNextPostProcess(ctx)
	if err != nil {
		p.logger.WarnContext(ctx, "dvr post-process: claim", "err", err)
		return false
	}
	if !ok {
		return false
	}
	p.logger.InfoContext(ctx, "dvr post-process: start", "recording_id", job.RecordingID, "kind", job.Kind)
	started := time.Now()
	status, msg := p.process(ctx, job)
	if err := p.store.FinishPostProcess(ctx, job.RecordingID, status, msg); err != nil {
		p.logger.WarnContext(ctx, "dvr post-process: write result", "recording_id", job.RecordingID, "err", err)
	}
	p.logger.InfoContext(ctx, "dvr post-process: done", "recording_id", job.RecordingID,
		"status", status, "message", msg, "elapsed", time.Since(started).Round(time.Second))
	return true
}

func (p *PostProcessor) progress(ctx context.Context, recID uuid.UUID, msg string) {
	if err := p.store.SetPostProcessMessage(ctx, recID, msg); err != nil {
		p.logger.WarnContext(ctx, "dvr post-process: progress", "recording_id", recID, "err", err)
	}
}

// process runs one job to a terminal status and message.
func (p *PostProcessor) process(ctx context.Context, job PostProcessJob) (PostProcessStatus, string) {
	rec, err := p.store.GetRecording(ctx, job.RecordingID)
	if err != nil {
		return PostProcessFailed, "recording not found"
	}
	if rec.Status != RecordingStatusCompleted || rec.FilePath == nil || *rec.FilePath == "" {
		return PostProcessFailed, "recording is not a completed capture"
	}
	src := *rec.FilePath
	if fi, err := os.Stat(src); err != nil || fi.IsDir() {
		return PostProcessFailed, "recording file is missing"
	}
	srcInfo, err := p.probeMedia(ctx, src)
	if err != nil {
		return PostProcessFailed, "could not read the recording: " + err.Error()
	}
	fileID, err := p.store.FindMediaFileID(ctx, src)
	if err != nil {
		return PostProcessFailed, "look up library file: " + err.Error()
	}

	if job.Kind == PostProcessDetect {
		return p.processDetect(ctx, rec.ID, src, srcInfo, fileID)
	}
	return p.processFull(ctx, rec.ID, src, srcInfo, fileID)
}

// processDetect: commercial detection on the file as it is; breaks are
// stored whatever the commercial setting (the admin asked for them).
func (p *PostProcessor) processDetect(ctx context.Context, recID uuid.UUID, src string, info mediaInfo, fileID uuid.UUID) (PostProcessStatus, string) {
	if fileID == uuid.Nil {
		return PostProcessFailed, "recording has no library item to attach commercial markers to"
	}
	p.progress(ctx, recID, "Detecting commercials")
	breaks, source, err := p.detectCommercials(ctx, src, info)
	if err != nil {
		return PostProcessFailed, "commercial detection failed: " + err.Error()
	}
	if err := p.store.ReplaceCommercialMarkers(ctx, fileID, source, breaks); err != nil {
		return PostProcessFailed, "save commercial markers: " + err.Error()
	}
	return PostProcessProcessed, breaksNote(len(breaks), "marked")
}

func (p *PostProcessor) processFull(ctx context.Context, recID uuid.UUID, src string, srcInfo mediaInfo, fileID uuid.UUID) (PostProcessStatus, string) {
	set := p.cfg.Settings(ctx)
	var notes []string
	var temps []string
	defer func() {
		for _, t := range temps {
			_ = os.Remove(t)
		}
	}()

	work, workInfo := src, srcInfo
	if set.rewrites() {
		out := tempPath(src, "process", set.Container)
		temps = append(temps, out)
		note, err := p.rewrite(ctx, recID, src, out, set, srcInfo)
		if err != nil {
			return PostProcessFailed, err.Error()
		}
		info, err := p.probeMedia(ctx, out)
		if err == nil {
			err = verifyOutput(srcInfo, info, srcInfo.DurationSec)
		}
		if err != nil {
			return PostProcessFailed, "processed file failed verification: " + err.Error()
		}
		work, workInfo = out, info
		notes = append(notes, note)
	}

	// Commercial detection runs on the rewritten file (its timeline is the
	// one players will see) and before any cut.
	detect := set.Commercials == CommercialsMark || set.Commercials == CommercialsCut
	var breaks []CommercialBreak
	var source string
	detected, cut := false, false
	if detect {
		p.progress(ctx, recID, "Detecting commercials")
		b, src2, err := p.detectCommercials(ctx, work, workInfo)
		if err != nil {
			// Detection is an extra; a failure doesn't throw away a good remux.
			notes = append(notes, "commercial detection failed: "+err.Error())
		} else {
			breaks, source, detected = b, src2, true
		}
	}

	if detected && set.Commercials == CommercialsCut && len(breaks) > 0 {
		if !set.rewrites() {
			notes = append(notes, "commercials marked, not cut (cutting needs remux or transcode)")
		} else {
			out, info, removedSec, err := p.cut(ctx, recID, src, work, workInfo, breaks, set.Container)
			if out != "" {
				temps = append(temps, out)
			}
			if err != nil {
				notes = append(notes, "commercials marked, not cut: "+err.Error())
			} else {
				work, workInfo, cut = out, info, true
				notes = append(notes, fmt.Sprintf("%s removed (%s)", plural(len(breaks), "commercial break"), fmtDuration(removedSec)))
			}
		}
	}

	finalPath := src
	if work != src {
		p.progress(ctx, recID, "Saving")
		final, err := p.commit(ctx, recID, src, work, fileID, set, workInfo)
		if err != nil {
			return PostProcessFailed, "could not replace the recording: " + err.Error()
		}
		finalPath = final
	}

	if detected && fileID != uuid.Nil {
		stored := breaks
		if cut {
			stored = nil // the breaks are gone from the file
		}
		if err := p.store.ReplaceCommercialMarkers(ctx, fileID, source, stored); err != nil {
			notes = append(notes, "save commercial markers: "+err.Error())
		} else if !cut {
			notes = append(notes, breaksNote(len(breaks), "marked"))
		}
	} else if detected && !cut && len(breaks) > 0 {
		notes = append(notes, "commercials found but not saved: the recording has no library item")
	}

	if p.cfg.ScriptPath != "" {
		p.progress(ctx, recID, "Running post-processing script")
		if err := p.runScript(ctx, recID, finalPath); err != nil {
			notes = append(notes, "script failed: "+err.Error())
			return PostProcessFailed, strings.Join(notes, "; ")
		}
		notes = append(notes, "script ran")
	}
	if len(notes) == 0 {
		notes = append(notes, "nothing to do")
	}
	return PostProcessProcessed, strings.Join(notes, "; ")
}

// rewrite remuxes or transcodes src into out. Transcoding tries the server's
// hardware encoders for the codec first and falls back to software.
func (p *PostProcessor) rewrite(ctx context.Context, recID uuid.UUID, src, out string, set PostProcessSettings, srcInfo mediaInfo) (string, error) {
	label := containerLabel(set.Container)
	if !set.Transcode {
		p.progress(ctx, recID, "Remuxing to "+label)
		if err := p.runFFmpeg(ctx, remuxArgs(src, out, set.Container)); err != nil {
			return "", fmt.Errorf("remux failed: %w", err)
		}
		return "remuxed to " + label, nil
	}
	codec := set.Codec
	if codec != "hevc" {
		codec = "h264"
	}
	if !srcInfo.HasVideo {
		return "", errors.New("transcode failed: the recording has no video")
	}
	var active []string
	if p.cfg.Encoders != nil {
		active = p.cfg.Encoders()
	}
	var lastErr error
	for _, enc := range encoderCandidates(codec, active) {
		p.progress(ctx, recID, "Transcoding to "+codecLabel(codec)+" ("+enc+")")
		err := p.runFFmpeg(ctx, transcodeArgs(src, out, set.Container, enc, codec, set.Quality))
		if err == nil {
			return fmt.Sprintf("transcoded to %s %s with %s", codecLabel(codec), label, enc), nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		p.logger.WarnContext(ctx, "dvr post-process: encoder failed, trying next",
			"recording_id", recID, "encoder", enc, "err", err)
		lastErr = err
		_ = os.Remove(out)
	}
	return "", fmt.Errorf("transcode failed: %w", lastErr)
}

// cut stream-copies the programme segments of work into a new temp file
// (named after the capture, src) and verifies its length against what should
// remain.
func (p *PostProcessor) cut(ctx context.Context, recID uuid.UUID, src, work string, info mediaInfo, breaks []CommercialBreak, container string) (string, mediaInfo, float64, error) {
	keep := keepSegments(breaks, info.DurationSec)
	if len(keep) == 0 {
		return "", mediaInfo{}, 0, errors.New("nothing would be left")
	}
	expected := 0.0
	for _, k := range keep {
		expected += k.end - k.start
	}
	p.progress(ctx, recID, "Cutting commercials")
	parts, listPath, cleanup, err := writeCutList(work, keep)
	if err != nil {
		return "", mediaInfo{}, 0, err
	}
	defer cleanup()
	out := tempPath(src, "cut", container)
	if err := p.runFFmpeg(ctx, cutArgs(parts, listPath, out, container)); err != nil {
		return out, mediaInfo{}, 0, fmt.Errorf("cut failed: %w", err)
	}
	outInfo, err := p.probeMedia(ctx, out)
	if err == nil {
		err = verifyOutput(info, outInfo, expected)
	}
	if err != nil {
		return out, mediaInfo{}, 0, fmt.Errorf("cut file failed verification: %w", err)
	}
	return out, outInfo, info.DurationSec - expected, nil
}

// commit swaps the verified output in for the capture and re-points the
// database. The capture always leaves the library folder: kept captures move
// into OriginalsDirName (the scanner skips it), otherwise they are deleted —
// but only after the database points at the new file.
func (p *PostProcessor) commit(ctx context.Context, recID uuid.UUID, src, work string, fileID uuid.UUID, set PostProcessSettings, info mediaInfo) (string, error) {
	_, ext := containerFormat(set.Container)
	final := strings.TrimSuffix(src, filepath.Ext(src)) + ext
	if final != src {
		final = uniquePath(final)
	}

	var keptAt string
	if !set.DeleteOriginal {
		dir := filepath.Join(filepath.Dir(src), OriginalsDirName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("originals folder: %w", err)
		}
		keptAt = uniquePath(filepath.Join(dir, filepath.Base(src)))
		if err := os.Rename(src, keptAt); err != nil {
			return "", fmt.Errorf("move capture aside: %w", err)
		}
	}
	restoreOriginal := func() {
		if keptAt != "" {
			if err := os.Rename(keptAt, src); err != nil {
				p.logger.ErrorContext(ctx, "dvr post-process: could not move capture back",
					"recording_id", recID, "from", keptAt, "to", src, "err", err)
			}
		}
	}

	// final == src only when the container is unchanged; with delete-original
	// on, this rename replaces the capture in one step.
	if err := os.Rename(work, final); err != nil {
		restoreOriginal()
		return "", fmt.Errorf("move processed file into place: %w", err)
	}
	fi, err := os.Stat(final)
	if err != nil {
		return "", fmt.Errorf("stat processed file: %w", err)
	}
	params := SwapFileParams{
		RecordingID: recID,
		FileID:      fileID,
		NewPath:     final,
		Size:        fi.Size(),
		Container:   info.FormatName,
		VideoCodec:  info.VideoCodec,
		AudioCodec:  info.AudioCodec,
		DurationMS:  int64(info.DurationSec * 1000),
	}
	if keptAt != "" {
		params.OriginalPath = &keptAt
	}
	if err := p.store.SwapRecordingFile(ctx, params); err != nil {
		if final != src {
			// Undo the filesystem half so the database (still pointing at the
			// capture) is right again.
			if rbErr := os.Rename(final, work); rbErr != nil {
				p.logger.ErrorContext(ctx, "dvr post-process: rollback of processed file failed",
					"recording_id", recID, "path", final, "err", rbErr)
			}
			restoreOriginal()
		}
		return "", fmt.Errorf("update database: %w", err)
	}
	if set.DeleteOriginal && final != src {
		if err := os.Remove(src); err != nil && !os.IsNotExist(err) {
			p.logger.WarnContext(ctx, "dvr post-process: delete original", "recording_id", recID, "path", src, "err", err)
		}
	}
	return final, nil
}

// runScript runs the operator's script with the recording path as argv[1]
// (exec, never a shell), bounded by ScriptTimeout. Its output is logged.
func (p *PostProcessor) runScript(ctx context.Context, recID uuid.UUID, path string) error {
	sctx, cancel := context.WithTimeout(ctx, p.cfg.ScriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(sctx, p.cfg.ScriptPath, path)
	var out cappedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	p.logger.InfoContext(ctx, "dvr post-process: script finished",
		"recording_id", recID, "script", p.cfg.ScriptPath, "err", err, "output", out.String())
	if err != nil {
		if errors.Is(sctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("timed out after %s", p.cfg.ScriptTimeout)
		}
		return err
	}
	return nil
}

// tempPath names a temp output beside src: hidden, ".part"-suffixed (never
// scanned), on the same filesystem so the final rename is atomic.
func tempPath(src, step, container string) string {
	_, ext := containerFormat(container)
	stem := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	return filepath.Join(filepath.Dir(src), "."+stem+".onscreen-"+step+ext+partSuffix)
}

// uniquePath returns path, or path with " (2)", " (3)", … before the
// extension when something already exists there.
func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
	return fmt.Sprintf("%s (%s)%s", base, uuid.NewString()[:8], ext)
}

func containerLabel(c string) string {
	if c == "mp4" {
		return "MP4"
	}
	return "MKV"
}

func codecLabel(c string) string {
	if c == "hevc" {
		return "HEVC"
	}
	return "H.264"
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func breaksNote(n int, verb string) string {
	if n == 0 {
		return "no commercials found"
	}
	return plural(n, "commercial break") + " " + verb
}

func fmtDuration(sec float64) string {
	d := time.Duration(sec * float64(time.Second)).Round(time.Second)
	return d.String()
}
