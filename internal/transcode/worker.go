package transcode

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("onscreen/transcode")

var segmentBaseDir = filepath.Join(os.TempDir(), "onscreen", "sessions")

const heartbeatInterval = 2 * time.Second

// Worker is a transcode worker that picks up jobs from the Valkey queue,
// runs FFmpeg, and serves HLS segments via a local HTTP server (ADR-008).
type Worker struct {
	id               string
	addr             string // "host:port" — advertised to the API for segment proxying
	store            *SessionStore
	encoders         []Encoder
	encoderLabels    map[string]string // encoder → human label, detected once at startup
	hasTonemapCuda   bool              // tonemap_cuda filter available in FFmpeg
	hasTonemapOpenCL bool              // tonemap_opencl filter available in FFmpeg
	hasZscale        bool              // zscale filter available (libzimg) for software tonemap
	encoderOpts      EncoderOpts       // per-deployment NVENC/maxrate tuning
	logger           *slog.Logger
	activeSessions   atomic.Int32
	maxSessions      int
	mu               sync.Mutex
	activeJobs       map[string]*os.Process // session_id → ffmpeg PID
}

// NewWorker creates a transcode Worker.
func NewWorker(id, addr string, store *SessionStore, encoders []Encoder, maxSessions int, encOpts EncoderOpts, logger *slog.Logger) *Worker {
	if maxSessions <= 0 {
		maxSessions = 4
	}
	// Detect GPU labels and filter capabilities while we have hardware access.
	ctx := context.Background()
	labels := make(map[string]string, len(encoders))
	for _, e := range encoders {
		labels[string(e)] = detectGPUName(ctx, e)
	}
	hasTonemap := ProbeFilter(ctx, "tonemap_cuda")
	hasTonemapOCL := ProbeFilter(ctx, "tonemap_opencl")
	hasZscale := ProbeFilter(ctx, "zscale")
	return &Worker{
		id:               id,
		addr:             addr,
		store:            store,
		encoders:         encoders,
		encoderLabels:    labels,
		hasTonemapCuda:   hasTonemap,
		hasTonemapOpenCL: hasTonemapOCL,
		hasZscale:        hasZscale,
		encoderOpts:      encOpts,
		maxSessions:      maxSessions,
		logger:           logger,
		activeJobs:       make(map[string]*os.Process),
	}
}

// Start runs the worker: registers, starts the HTTP segment server,
// runs the heartbeat loop, and processes the job queue until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) error {
	// Clean up any orphaned session directories from a prior crash.
	w.sweepOrphanedSessions()

	if err := w.register(ctx); err != nil {
		return fmt.Errorf("worker register: %w", err)
	}

	go w.heartbeatLoop(ctx)
	go w.startSegmentServer(ctx)

	w.logger.Info("transcode worker ready",
		"id", w.id,
		"addr", w.addr,
		"encoders", EncoderNames(w.encoders),
		"max_sessions", w.maxSessions,
		"tonemap_cuda", w.hasTonemapCuda,
		"tonemap_opencl", w.hasTonemapOpenCL,
		"zscale", w.hasZscale,
	)

	return w.jobLoop(ctx)
}

// register writes the worker registration record to Valkey.
func (w *Worker) register(ctx context.Context) error {
	return w.store.RegisterWorker(ctx, WorkerRegistration{
		ID:             w.id,
		Addr:           w.addr,
		Capabilities:   EncoderNames(w.encoders),
		EncoderLabels:  w.encoderLabels,
		MaxSessions:    w.maxSessions,
		ActiveSessions: int(w.activeSessions.Load()),
		RegisteredAt:   time.Now(),
	})
}

// heartbeatLoop refreshes the worker Valkey key every workerRefresh seconds.
func (w *Worker) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(workerRefresh)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.register(ctx); err != nil {
				w.logger.Warn("worker heartbeat failed", "err", err)
			}
		}
	}
}

// jobLoop blocks on the Valkey queue and processes jobs sequentially.
// Multiple workers run concurrently in separate goroutines.
func (w *Worker) jobLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if int(w.activeSessions.Load()) >= w.maxSessions {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(500 * time.Millisecond):
				continue
			}
		}

		job, err := w.store.DequeueJob(ctx, w.addr, 5*time.Second)
		if err != nil {
			w.logger.Warn("dequeue error", "err", err)
			continue
		}
		if job == nil {
			continue // timeout, loop again
		}

		w.activeSessions.Add(1)
		w.store.AckDispatch(ctx, w.addr)
		go func(j TranscodeJob) {
			defer w.activeSessions.Add(-1)
			if err := w.runJob(ctx, j); err != nil {
				w.logger.Error("transcode job failed",
					"session_id", j.SessionID, "err", err)
			}
		}(*job)
	}
}

// runJob executes a single transcode job.
func (w *Worker) runJob(ctx context.Context, job TranscodeJob) (err error) {
	ctx, span := tracer.Start(ctx, "transcode.run_job", trace.WithAttributes(
		attribute.String("session.id", job.SessionID),
		attribute.String("decision", job.Decision),
		attribute.String("encoder", job.Encoder),
		attribute.Int("width", job.Width),
		attribute.Int("height", job.Height),
		attribute.Int("bitrate_kbps", job.BitrateKbps),
		attribute.Bool("prefer_hevc", job.PreferHEVC),
		attribute.Bool("needs_tonemap", job.NeedsToneMap),
	))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	// Ensure session directory exists.
	if err := os.MkdirAll(job.SessionDir, 0755); err != nil {
		return fmt.Errorf("mkdir session dir: %w", err)
	}

	var ffArgs []string
	var actualEncoder Encoder
	switch job.Decision {
	case "directStream":
		ffArgs = BuildDirectStream(job.FilePath, job.SessionDir, job.StartOffsetSec)
	default:
		enc := Encoder(job.Encoder)
		if enc == "" {
			enc = BestEncoder(w.encoders)
		}
		// Use HEVC output encoder when requested and available.
		// If HEVC was requested but no HEVC encoder exists, restore the
		// H.264-grade bitrate — the API already scaled it down for HEVC.
		if job.PreferHEVC && !IsHEVCEncoder(enc) {
			if hevc := BestHEVCEncoder(w.encoders); hevc != "" {
				enc = hevc
			} else if HEVCBitrateRatio > 0 {
				job.BitrateKbps = int(float64(job.BitrateKbps) / HEVCBitrateRatio)
			}
		}

		bitrate := job.BitrateKbps
		w.logger.Info("starting ffmpeg",
			"session_id", job.SessionID,
			"encoder", enc,
			"width", job.Width, "height", job.Height,
			"bitrate_kbps", bitrate,
			"tonemap", job.NeedsToneMap,
			"prefer_hevc", job.PreferHEVC,
			"tonemap_cuda", w.hasTonemapCuda,
			"tonemap_opencl", w.hasTonemapOpenCL,
			"zscale", w.hasZscale,
		)

		ffArgs = BuildHLS(BuildArgs{
			InputPath:        job.FilePath,
			StartOffset:      job.StartOffsetSec,
			Encoder:          enc,
			IsVAAPI:          enc == EncoderVAAPI,
			IsHEVC:           job.IsHEVC,
			Width:            job.Width,
			Height:           job.Height,
			BitrateKbps:      bitrate,
			NeedsToneMap:     job.NeedsToneMap,
			HasTonemapCuda:   w.hasTonemapCuda,
			HasTonemapOpenCL: w.hasTonemapOpenCL,
			HasZscale:        w.hasZscale,
			AudioCodec:       job.AudioCodec,
			AudioChannels:    job.AudioChannels,
			AudioStreamIndex: job.AudioStreamIndex,
			SubtitleStreams:  job.SubtitleStreams,
			EncoderOpts:      w.encoderOpts,
			SessionDir:       job.SessionDir,
			SegmentPrefix:    "seg",
		})
		actualEncoder = enc
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", ffArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// Stamp the session with this worker's address and actual HEVC output status.
	// The API sets HEVCOutput based on client preference, but the worker may fall
	// back to H.264 if no HEVC encoder is active. Correct it here so the playlist
	// handler looks for the right segment extension (.ts vs .m4s).
	actualHEVC := IsHEVCEncoder(actualEncoder)
	if err := w.store.SetWorkerInfo(ctx, job.SessionID, w.id, w.addr, actualHEVC); err != nil {
		w.logger.Warn("set worker info on session", "session_id", job.SessionID, "err", err)
	}

	// Track PID for kill on session stop.
	w.mu.Lock()
	w.activeJobs[job.SessionID] = cmd.Process
	w.mu.Unlock()

	// Heartbeat loop while FFmpeg runs.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()

loop:
	for {
		select {
		case err := <-done:
			if err != nil {
				w.logger.Warn("ffmpeg exited with error",
					"session_id", job.SessionID, "err", err)
			} else {
				w.logger.Info("ffmpeg completed", "session_id", job.SessionID)
			}
			break loop
		case <-t.C:
			bg := context.Background()
			// If the session no longer exists in Valkey (client stopped it), kill FFmpeg.
			if _, err := w.store.Get(bg, job.SessionID); err != nil {
				w.logger.Info("session deleted — killing ffmpeg", "session_id", job.SessionID)
				_ = cmd.Process.Kill()
				break loop
			}
			if err := w.store.SetHeartbeat(bg, job.SessionID); err != nil {
				w.logger.Warn("heartbeat write failed",
					"session_id", job.SessionID, "err", err)
			}
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			break loop
		}
	}

	w.mu.Lock()
	delete(w.activeJobs, job.SessionID)
	w.mu.Unlock()

	return nil
}

// KillSession terminates an in-progress FFmpeg process for a session.
func (w *Worker) KillSession(sessionID string) {
	w.mu.Lock()
	p, ok := w.activeJobs[sessionID]
	w.mu.Unlock()
	if ok {
		_ = p.Kill()
	}
}

// startSegmentServer runs a minimal HTTP server to serve HLS segments.
// The API proxy forwards segment requests to this server.
func (w *Worker) startSegmentServer(ctx context.Context) {
	mux := http.NewServeMux()

	// Serve files from /tmp/onscreen/sessions/{session_id}/
	mux.HandleFunc("/segments/", func(rw http.ResponseWriter, r *http.Request) {
		// Path: /segments/{session_id}/{filename}
		rel := r.URL.Path[len("/segments/"):]
		abs := filepath.Join(segmentBaseDir, rel)

		// Basic path traversal check.
		clean := filepath.Clean(abs)
		base := filepath.Clean(segmentBaseDir) + string(os.PathSeparator)
		if clean != filepath.Clean(segmentBaseDir) && !strings.HasPrefix(clean, base) {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}

		// Block until the segment exists (up to 10 s). For slow transcodes
		// (e.g. 4K HDR at ~1x speed) HLS.js may request the next segment
		// before FFmpeg has produced it. Blocking here keeps the HTTP request
		// pending while the player continues from its buffer — the same
		// strategy Jellyfin uses to avoid buffering spinners.
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(clean); err == nil {
				break
			}
			if time.Now().After(deadline) {
				http.Error(rw, "segment not ready", http.StatusNotFound)
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}

		http.ServeFile(rw, r, clean)
	})

	srv := &http.Server{
		Addr:         w.addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		w.logger.Error("segment server error", "err", err)
	}
}

// sweepOrphanedSessions removes session directories left by a prior crash.
func (w *Worker) sweepOrphanedSessions() {
	entries, err := os.ReadDir(segmentBaseDir)
	if err != nil {
		return // base dir doesn't exist yet — fine
	}
	for _, e := range entries {
		if e.IsDir() {
			dir := filepath.Join(segmentBaseDir, e.Name())
			w.logger.Info("sweeping orphaned session dir", "dir", dir)
			_ = os.RemoveAll(dir)
		}
	}
}

// SessionDir returns the local filesystem path for a session's HLS segments.
func SessionDir(sessionID string) string {
	return filepath.Join(segmentBaseDir, sessionID)
}

// WorkerID generates a stable UUID-based worker ID.
func WorkerID() string {
	return uuid.New().String()
}

// EncoderNames returns the string names of the given encoders.
func EncoderNames(encoders []Encoder) []string {
	names := make([]string, len(encoders))
	for i, e := range encoders {
		names[i] = string(e)
	}
	return names
}
