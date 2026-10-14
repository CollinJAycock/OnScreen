package downloads

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/ffsafe"
	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/scanner"
	"github.com/onscreen/onscreen/internal/transcode"
)

// MediaSource reads the files a conversion encodes.
type MediaSource interface {
	GetFile(ctx context.Context, id uuid.UUID) (*media.File, error)
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
}

// SlotReserver hands out transcode session slots — the embedded worker's
// (transcode.Worker.TryReserveSlot) — so conversions share playback's
// concurrency limit instead of piling encodes on top of it.
type SlotReserver interface {
	TryReserveSlot(costCenti int) (release func(), ok bool)
}

// RunnerConfig is the runner's fixed configuration.
type RunnerConfig struct {
	// Root is the download cache directory (CACHE_PATH/downloads).
	Root string
	// OptimizedRoot is where optimized versions are written
	// (CACHE_PATH/optimized). Empty: optimize encodes fail with an error.
	OptimizedRoot string
	// MaxConcurrent is how many conversions run at once on this server
	// (DOWNLOAD_MAX_CONVERSIONS, 1 or 2).
	MaxConcurrent int
	// TTL is how long a finished file is kept after it was last used.
	TTL time.Duration
	// Encoder is the H.264 encoder from the server's transcode settings; a
	// hardware encoder falls back to libx264 if it fails.
	Encoder     transcode.Encoder
	EncoderOpts transcode.EncoderOpts
	// HasZscale enables the HDR→SDR tonemap (ffmpeg built with zimg).
	HasZscale bool
}

const (
	// heartbeatEvery is how often a running encode writes its progress — the
	// heartbeat that keeps it from being requeued, and the point at which a
	// cancelled encode (row gone) is noticed and stopped.
	heartbeatEvery = 3 * time.Second
	// staleAfter: a running encode with no heartbeat for this long belongs
	// to a server that died; it goes back to the queue.
	staleAfter = 2 * time.Minute
	// pollEvery: how often the runner looks for queued work without a wake.
	pollEvery = 10 * time.Second
)

// Runner claims queued conversions and encodes them on this server.
type Runner struct {
	q      *gen.Queries
	media  MediaSource
	cfg    RunnerConfig
	slots  SlotReserver
	logger *slog.Logger

	sem  chan struct{}
	wake chan struct{}

	mu      sync.Mutex
	running map[uuid.UUID]context.CancelFunc

	// ffmpeg is the binary; tests may point it elsewhere.
	ffmpeg string
}

// NewRunner builds a runner. Run starts it.
func NewRunner(pool *pgxpool.Pool, src MediaSource, cfg RunnerConfig, logger *slog.Logger) *Runner {
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = 1
	}
	if cfg.TTL <= 0 {
		cfg.TTL = 7 * 24 * time.Hour
	}
	if cfg.Encoder == "" {
		cfg.Encoder = transcode.EncoderSoftware
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		q:       gen.New(pool),
		media:   src,
		cfg:     cfg,
		logger:  logger,
		sem:     make(chan struct{}, cfg.MaxConcurrent),
		wake:    make(chan struct{}, 1),
		running: make(map[uuid.UUID]context.CancelFunc),
		ffmpeg:  "ffmpeg",
	}
}

// WithSlots makes every conversion hold a transcode slot while it runs. Nil
// (no embedded worker) leaves only MaxConcurrent as the limit.
func (r *Runner) WithSlots(s SlotReserver) *Runner {
	r.slots = s
	return r
}

// Wake nudges the runner to look for queued work now.
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Cancel stops the conversion's ffmpeg if it runs on this server. The
// heartbeat stops it anyway (its row is gone), this just does it at once.
func (r *Runner) Cancel(conversionID uuid.UUID) {
	r.mu.Lock()
	cancel := r.running[conversionID]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Run claims and runs conversions until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	if err := os.MkdirAll(r.cfg.Root, 0o755); err != nil {
		return fmt.Errorf("download cache dir: %w", err)
	}
	if r.cfg.OptimizedRoot != "" {
		if err := os.MkdirAll(r.cfg.OptimizedRoot, 0o755); err != nil {
			return fmt.Errorf("optimized versions dir: %w", err)
		}
	}
	r.requeueStale(ctx)
	lastStaleCheck := time.Now()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		if time.Since(lastStaleCheck) > time.Minute {
			r.requeueStale(ctx)
			lastStaleCheck = time.Now()
		}
		r.fill(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-r.wake:
		case <-ticker.C:
		}
	}
}

// fill starts conversions while there is a free runner slot, a free
// transcode slot and queued work.
func (r *Runner) fill(ctx context.Context) {
	for {
		select {
		case r.sem <- struct{}{}:
		default:
			return // at this server's conversion limit
		}
		release := func() {}
		if r.slots != nil {
			rel, ok := r.slots.TryReserveSlot(transcode.JobCostCenti(1920, 1080, "transcode"))
			if !ok {
				<-r.sem
				return // every transcode slot is busy: playback first
			}
			release = rel
		}
		conv, err := r.q.ClaimQueuedDownloadConversion(ctx)
		if err != nil {
			release()
			<-r.sem
			if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
				r.logger.WarnContext(ctx, "downloads: claim conversion", "err", err)
			}
			return
		}
		observability.SafeGo(r.logger, "downloads:convert", func() {
			defer func() { <-r.sem; r.Wake() }()
			defer release()
			r.execute(ctx, conv)
		})
	}
}

func (r *Runner) requeueStale(ctx context.Context) {
	n, err := r.q.RequeueStaleDownloadConversions(ctx, pgTime(time.Now().Add(-staleAfter)))
	if err != nil {
		if ctx.Err() == nil {
			r.logger.WarnContext(ctx, "downloads: requeue stale conversions", "err", err)
		}
		return
	}
	if n > 0 {
		r.logger.InfoContext(ctx, "downloads: requeued interrupted conversions", "count", n)
	}
}

// errCancelled: the conversion's row was deleted or left 'running' while it
// encoded (the last job pointing at it was deleted, or an admin deleted the
// optimized version).
var errCancelled = errors.New("conversion cancelled")

// rootFor is the directory a conversion's output belongs in: the download
// cache, or the optimized-versions directory.
func (r *Runner) rootFor(conv gen.DownloadConversion) string {
	if conv.Purpose == PurposeOptimize {
		return r.cfg.OptimizedRoot
	}
	return r.cfg.Root
}

// encodeResult is what a finished encode produced.
type encodeResult struct {
	size        int64
	width       int
	height      int
	bitrateKbps int
	durationMS  int64
}

// readyParams turns a result into the MarkDownloadConversionReady write.
func (r *Runner) readyParams(id uuid.UUID, res encodeResult) gen.MarkDownloadConversionReadyParams {
	p := gen.MarkDownloadConversionReadyParams{
		ID: id, SizeBytes: &res.size, TtlSeconds: int64(r.cfg.TTL.Seconds()),
	}
	if res.width > 0 && res.height > 0 {
		w, h := int32(res.width), int32(res.height)
		p.Width, p.Height = &w, &h
	}
	if res.bitrateKbps > 0 {
		b := int32(res.bitrateKbps)
		p.BitrateKbps = &b
	}
	if res.durationMS > 0 {
		d := res.durationMS
		p.DurationMs = &d
	}
	return p
}

func (r *Runner) execute(parent context.Context, conv gen.DownloadConversion) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	r.mu.Lock()
	r.running[conv.ID] = cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.running, conv.ID)
		r.mu.Unlock()
	}()

	start := time.Now()
	root := r.rootFor(conv)
	log := r.logger.With("conversion_id", conv.ID, "file_id", conv.MediaFileID, "quality", conv.Quality, "purpose", conv.Purpose)
	log.InfoContext(ctx, "downloads: conversion started")
	res, err := r.convert(ctx, conv)
	switch {
	case err == nil:
		n, merr := r.q.MarkDownloadConversionReady(parent, r.readyParams(conv.ID, res))
		if merr != nil || n == 0 {
			// Cancelled in the last instant, or the write failed: either
			// way nothing points at the file.
			_ = os.Remove(OutputPath(root, conv.ID))
			if merr != nil {
				log.ErrorContext(parent, "downloads: mark ready", "err", merr)
			}
			return
		}
		log.InfoContext(parent, "downloads: conversion ready", "bytes", res.size, "took", time.Since(start).Round(time.Second))
	case errors.Is(err, errCancelled) || parent.Err() != nil:
		if root != "" {
			_ = os.Remove(partPath(root, conv.ID))
		}
		if parent.Err() == nil {
			log.InfoContext(parent, "downloads: conversion cancelled")
		}
		// On shutdown the row stays 'running'; its heartbeat stops and the
		// next start (here or on another server) requeues it.
	default:
		if root != "" {
			_ = os.Remove(partPath(root, conv.ID))
		}
		msg := err.Error()
		if len(msg) > 1000 {
			msg = msg[:1000]
		}
		if ferr := r.q.MarkDownloadConversionFailed(parent, gen.MarkDownloadConversionFailedParams{
			ID: conv.ID, Error: &msg,
		}); ferr != nil {
			log.ErrorContext(parent, "downloads: mark failed", "err", ferr)
		}
		log.WarnContext(parent, "downloads: conversion failed", "err", err)
	}
}

// source is what convert needs to know about the input.
type source struct {
	input      string
	parts      []ffsafe.ConcatPart
	durationMS int64
	file       *media.File
}

// resolveSource loads the file (and, for a stacked movie, every part) and
// applies the same refusals playback does: inactive or damaged files, files
// that are playlists/reference containers, Dolby Vision profile 5.
func (r *Runner) resolveSource(ctx context.Context, conv gen.DownloadConversion) (source, error) {
	f, err := r.media.GetFile(ctx, conv.MediaFileID)
	if err != nil {
		return source{}, fmt.Errorf("source file: %w", err)
	}
	if err := Convertible(f); err != nil {
		return source{}, err
	}
	src := source{input: f.FilePath, file: f}
	if f.DurationMS != nil {
		src.durationMS = *f.DurationMS
	}
	if f.PartIndex == nil {
		return src, nil
	}
	files, err := r.media.GetFiles(ctx, conv.MediaItemID)
	if err != nil {
		return source{}, fmt.Errorf("list parts: %w", err)
	}
	parts := media.Stack(files, *f)
	if parts == nil {
		return src, nil
	}
	src.durationMS = 0
	if d := media.StackDurationMS(parts); d != nil {
		src.durationMS = *d
	}
	for i := range parts {
		p := &parts[i]
		if err := Convertible(p); err != nil {
			return source{}, fmt.Errorf("part %d: %w", i+1, err)
		}
		var dur float64
		if p.DurationMS != nil {
			dur = float64(*p.DurationMS) / 1000
		}
		src.parts = append(src.parts, ffsafe.ConcatPart{Input: p.FilePath, DurationSec: dur})
	}
	return src, nil
}

// Convertible reports why a file can't be converted, or nil. The checks
// mirror transcode start: the integrity probe's damaged verdict, the
// playlist/reference-container refusal (stored container name plus a byte
// sniff, never an ffprobe), and Dolby Vision profile 5, whose IPT colour a
// re-encode turns green and purple.
func Convertible(f *media.File) error {
	if f.Status != "active" {
		return errors.New("the file is not available")
	}
	if f.VideoCodec == nil || *f.VideoCodec == "" {
		return errors.New("the file has no video")
	}
	if f.IntegrityStatus == media.IntegrityDamaged {
		return errors.New("the file is damaged")
	}
	if f.Container != nil && scanner.IsReferenceDemuxer(*f.Container) {
		return scanner.ErrUnsafeContainer
	}
	if scanner.LooksLikeReferenceContainer(f.FilePath) {
		return scanner.ErrUnsafeContainer
	}
	if f.HDRType != nil && strings.EqualFold(*f.HDRType, "dolby_vision") {
		return errors.New("Dolby Vision is not supported")
	}
	return nil
}

// textSubtitleStreams returns the absolute stream indexes of a file's text
// subtitle streams (those ffmpeg can carry as mov_text).
func textSubtitleStreams(subtitleStreamsJSON []byte) []int {
	if len(subtitleStreamsJSON) == 0 {
		return nil
	}
	var raw []struct {
		Index *int   `json:"index"`
		Codec string `json:"codec"`
	}
	if err := json.Unmarshal(subtitleStreamsJSON, &raw); err != nil {
		return nil
	}
	var out []int
	for _, s := range raw {
		if s.Index != nil && *s.Index >= 0 && transcode.IsTextSubtitleCodec(s.Codec) {
			out = append(out, *s.Index)
		}
	}
	return out
}

// planFor is the encode a conversion targets: its download rung, or its
// optimize profile worked out against the source.
func planFor(conv gen.DownloadConversion, f *media.File) (EncodePlan, error) {
	if conv.Purpose == PurposeOptimize {
		p, ok := OptimizeProfileByID(conv.Quality)
		if !ok {
			return EncodePlan{}, fmt.Errorf("unknown optimize profile %q", conv.Quality)
		}
		return p.Plan(f), nil
	}
	q, ok := QualityByID(conv.Quality)
	if !ok {
		return EncodePlan{}, fmt.Errorf("unknown quality %q", conv.Quality)
	}
	return EncodePlan{BitrateKbps: q.Profile.Bitrate, MaxWidth: q.Profile.MaxWidth, MaxHeight: q.Profile.MaxHeight}, nil
}

// convert encodes one conversion into its directory and describes the
// result.
func (r *Runner) convert(ctx context.Context, conv gen.DownloadConversion) (encodeResult, error) {
	root := r.rootFor(conv)
	if root == "" {
		return encodeResult{}, errors.New("optimized versions are not enabled on this server")
	}
	src, err := r.resolveSource(ctx, conv)
	if err != nil {
		return encodeResult{}, err
	}
	plan, err := planFor(conv, src.file)
	if err != nil {
		return encodeResult{}, err
	}
	input := src.input
	if len(src.parts) > 0 {
		listDir := filepath.Join(root, ".lists")
		if err := os.MkdirAll(listDir, 0o700); err != nil {
			return encodeResult{}, fmt.Errorf("concat list dir: %w", err)
		}
		input = filepath.Join(listDir, conv.ID.String()+".ffconcat")
		if err := ffsafe.WriteConcatList(input, src.parts); err != nil {
			return encodeResult{}, fmt.Errorf("concat list: %w", err)
		}
		defer func() { _ = os.Remove(input) }()
	}

	f := src.file
	args := transcode.ProgressiveArgs{
		InputPath:   input,
		ConcatParts: src.parts,
		OutputPath:  partPath(root, conv.ID),
		Encoder:     r.cfg.Encoder,
		BitrateKbps: plan.BitrateKbps,
		MaxWidth:    plan.MaxWidth,
		MaxHeight:   plan.MaxHeight,
		// HDR10 / HLG sources are tonemapped to SDR when zscale exists.
		NeedsToneMap: f.HDRType != nil && *f.HDRType != "",
		HasZscale:    r.cfg.HasZscale,
		EncoderOpts:  r.cfg.EncoderOpts,
	}
	if f.ResolutionW != nil && f.ResolutionH != nil {
		args.SourceWidth, args.SourceHeight = *f.ResolutionW, *f.ResolutionH
	}
	// Text subtitles ride along as mov_text — for a single file only: a
	// stacked movie's parts needn't carry the same subtitle streams.
	if len(src.parts) == 0 {
		args.TextSubtitleStreams = textSubtitleStreams(f.SubtitleStreams)
	}

	progress := &atomic.Int64{} // millionths
	hbCtx, stopHB := context.WithCancel(ctx)
	defer stopHB()
	cancelled := &atomic.Bool{}
	encodeCtx, cancelEncode := context.WithCancel(ctx)
	defer cancelEncode()
	go r.heartbeat(hbCtx, conv.ID, progress, func() { cancelled.Store(true); cancelEncode() })

	err = r.runFFmpeg(encodeCtx, transcode.BuildProgressiveMP4(args), src.durationMS, progress)
	if err != nil && !cancelled.Load() && ctx.Err() == nil && transcode.IsHardwareEncoder(args.Encoder) {
		// The hardware encoder couldn't do it (busy GPU, unsupported input):
		// one software retry, like the worker's encoder fail-over.
		r.logger.WarnContext(ctx, "downloads: hardware encode failed, retrying with libx264",
			"conversion_id", conv.ID, "encoder", args.Encoder, "err", err)
		args.Encoder = transcode.EncoderSoftware
		progress.Store(0)
		err = r.runFFmpeg(encodeCtx, transcode.BuildProgressiveMP4(args), src.durationMS, progress)
	}
	if cancelled.Load() {
		return encodeResult{}, errCancelled
	}
	if err != nil {
		return encodeResult{}, err
	}
	final := OutputPath(root, conv.ID)
	if err := os.Rename(args.OutputPath, final); err != nil {
		return encodeResult{}, fmt.Errorf("finalize output: %w", err)
	}
	fi, err := os.Stat(final)
	if err != nil {
		return encodeResult{}, fmt.Errorf("stat output: %w", err)
	}
	res := encodeResult{size: fi.Size(), durationMS: src.durationMS}
	res.width, res.height = transcode.FitWithin(args.SourceWidth, args.SourceHeight, args.MaxWidth, args.MaxHeight)
	res.bitrateKbps = averageKbps(res.size, res.durationMS)
	if res.bitrateKbps == 0 {
		res.bitrateKbps = plan.BitrateKbps + transcode.DownloadAudioBitrateKbps
	}
	return res, nil
}

// averageKbps is a file's overall bitrate from its size and duration; 0 when
// the duration is unknown.
func averageKbps(size, durationMS int64) int {
	if size <= 0 || durationMS <= 0 {
		return 0
	}
	return int(size * 8 / durationMS) // bytes·8 / ms = kbit/s
}

// heartbeat writes progress every heartbeatEvery; when the write finds the
// row gone or no longer running, the conversion was cancelled.
func (r *Runner) heartbeat(ctx context.Context, id uuid.UUID, progress *atomic.Int64, onCancelled func()) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		p := float32(progress.Load()) / 1e6
		n, err := r.q.UpdateDownloadConversionProgress(ctx, gen.UpdateDownloadConversionProgressParams{ID: id, Progress: p})
		if err != nil {
			if ctx.Err() == nil {
				r.logger.WarnContext(ctx, "downloads: heartbeat", "conversion_id", id, "err", err)
			}
			continue
		}
		if n == 0 {
			onCancelled()
			return
		}
	}
}

// runFFmpeg runs one encode, feeding -progress output into progress
// (millionths of durationMS; left at 0 when the duration is unknown).
func (r *Runner) runFFmpeg(ctx context.Context, args []string, durationMS int64, progress *atomic.Int64) error {
	cmd := exec.CommandContext(ctx, r.ffmpeg, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, n: 4000}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	readProgress(stdout, durationMS, progress)
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// readProgress consumes ffmpeg's -progress key=value stream. out_time_us is
// the output position; progress is that over the source duration, capped
// just under 1 (the faststart rewrite still follows the last frame).
func readProgress(rd io.Reader, durationMS int64, progress *atomic.Int64) {
	sc := bufio.NewScanner(rd)
	for sc.Scan() {
		line := sc.Text()
		v, ok := strings.CutPrefix(line, "out_time_us=")
		if !ok {
			continue
		}
		us, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil || us < 0 || durationMS <= 0 {
			continue
		}
		frac := float64(us) / float64(durationMS*1000)
		frac = math.Min(frac, 0.99)
		progress.Store(int64(frac * 1e6))
	}
}

// limitedWriter keeps the first n bytes written (ffmpeg's error summary).
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		_, _ = l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}
