package transcode

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/onscreen/onscreen/internal/observability"
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
	hasLibplacebo    bool              // libplacebo+Vulkan HDR→SDR tonemap works (GPU, vendor-agnostic; preferred over zscale)
	openclDevices    []OpenCLDevice    // platform.device list for `-init_hw_device opencl=ocl:...`
	encoderOpts      EncoderOpts       // per-deployment NVENC/maxrate tuning
	qsvDecode        bool              // opt-in QSV hardware HEVC decode (TRANSCODE_QSV_DECODE)
	cudaHevcDecode   bool              // NVDEC HEVC decode works here (startup probe); offloads 4K HEVC decode to the GPU (system memory)
	cudaHevcScale    bool              // full-VRAM HEVC chain (cuvid→scale_cuda→NVENC) works here (startup probe); offloads 4K SDR scale to the GPU
	nodeID           string            // NODE_ID (hostname default) — reported so the admin can find this node in Settings ▸ Nodes
	logger           *slog.Logger
	activeSessions   atomic.Int32
	activeCostCenti  atomic.Int64 // summed CostCenti of in-flight jobs; reported for weighted dispatch
	maxSessions      atomic.Int32 // concurrent-session cap; live-adjustable via SetMaxSessions
	mu               sync.Mutex
	activeJobs       map[string]*os.Process // session_id → ffmpeg PID
}

// SetMaxSessions adjusts the worker's concurrent-session cap at runtime. Takes
// effect on the next dequeue + the next heartbeat (so the dispatcher sees the
// new cap within ~workerRefresh). A non-positive value is ignored. Used by the
// fleet admin UI to retune the embedded worker without a restart.
func (w *Worker) SetMaxSessions(n int) {
	if n > 0 {
		w.maxSessions.Store(int32(n))
	}
}

// SetQSVDecode enables Intel QSV hardware HEVC decode on this worker
// (TRANSCODE_QSV_DECODE). Off by default; the worker falls back to software
// decode if a QSV-decode ffmpeg fails before producing its first segment.
func (w *Worker) SetQSVDecode(v bool) { w.qsvDecode = v }

// SetNodeID records this worker's NODE_ID so its registration advertises the
// node identity its per-node config (Settings ▸ Nodes) is keyed by.
func (w *Worker) SetNodeID(id string) { w.nodeID = id }

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
	// GPU HDR→SDR via libplacebo (Vulkan) — the preferred tonemap path. Real
	// end-to-end probe (not just filter presence) so a host with the filter but
	// no working Vulkan device falls back to software zscale.
	hasLibplacebo := ProbeLibplaceboVulkan(ctx)
	// NVDEC HEVC decode offload. Real round-trip probe (NVENC-encode a tiny
	// HEVC clip, then NVDEC-decode it) so it's enabled only where mainline
	// ffmpeg + the driver survive NVDEC HEVC. Skip entirely unless an NVENC
	// encoder is present — no point probing on a non-NVIDIA worker.
	cudaHevcDecode := false
	// Full-VRAM HEVC chain (cuvid→scale_cuda→NVENC). Used for 4K SDR re-encodes
	// so the downscale stays on the GPU; HDR keeps the system-memory decode
	// (cudaHevcDecode) + libplacebo tonemap. Separately probed because it's the
	// historically fragile mainline path.
	cudaHevcScale := false
	for _, e := range encoders {
		if IsNVENCEncoder(e) {
			cudaHevcDecode = ProbeCudaHevcDecode(ctx)
			cudaHevcScale = ProbeCudaHevcScale(ctx)
			break
		}
	}
	// Probe OpenCL platforms once at worker startup. Result is cached
	// for the worker's lifetime; ffmpeg arg-builder reads
	// PickOpenCLDevice(this list, encoder) at session-start to avoid
	// the bare-`opencl=ocl` auto-picker that fails (-19) when more
	// than one platform is visible. Only meaningful if tonemap_opencl
	// is in the available filters — otherwise the field stays nil.
	var openclDevices []OpenCLDevice
	if hasTonemapOCL {
		openclDevices = ListOpenCLDevices(ctx)
	}
	w := &Worker{
		id:               id,
		addr:             addr,
		store:            store,
		encoders:         encoders,
		encoderLabels:    labels,
		hasTonemapCuda:   hasTonemap,
		hasTonemapOpenCL: hasTonemapOCL,
		hasZscale:        hasZscale,
		hasLibplacebo:    hasLibplacebo,
		cudaHevcDecode:   cudaHevcDecode,
		cudaHevcScale:    cudaHevcScale,
		openclDevices:    openclDevices,
		encoderOpts:      encOpts,
		logger:           logger,
		activeJobs:       make(map[string]*os.Process),
	}
	w.maxSessions.Store(int32(maxSessions))
	return w
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

	openclSummary := make([]string, 0, len(w.openclDevices))
	for _, d := range w.openclDevices {
		openclSummary = append(openclSummary, d.Index+":"+d.PlatformName)
	}
	w.logger.Info("transcode worker ready",
		"id", w.id,
		"addr", w.addr,
		"encoders", EncoderNames(w.encoders),
		"max_sessions", int(w.maxSessions.Load()),
		"tonemap_cuda", w.hasTonemapCuda,
		"tonemap_opencl", w.hasTonemapOpenCL,
		"zscale", w.hasZscale,
		"libplacebo", w.hasLibplacebo,
		"nvdec_hevc", w.cudaHevcDecode,
		"cuda_scale", w.cudaHevcScale,
		"opencl_platforms", openclSummary,
	)

	return w.jobLoop(ctx)
}

// register writes the worker registration record to Valkey.
func (w *Worker) register(ctx context.Context) error {
	return w.store.RegisterWorker(ctx, WorkerRegistration{
		ID:              w.id,
		Addr:            w.addr,
		NodeID:          w.nodeID,
		Capabilities:    EncoderNames(w.encoders),
		EncoderLabels:   w.encoderLabels,
		MaxSessions:     int(w.maxSessions.Load()),
		ActiveSessions:  int(w.activeSessions.Load()),
		ActiveCostCenti: int(w.activeCostCenti.Load()),
		HasGPUTonemap:   w.hasLibplacebo || w.hasTonemapCuda || w.hasTonemapOpenCL,
		RegisteredAt:    time.Now(),
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

		if int(w.activeSessions.Load()) >= int(w.maxSessions.Load()) {
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

		// Jobs routed via DispatchJob arrive pre-stamped; those pushed onto the
		// global queue (no registered workers at enqueue time) don't, so derive
		// the cost here to keep load accounting honest either way.
		if job.CostCenti == 0 {
			job.CostCenti = JobCostCenti(job.Width, job.Height, job.Decision)
		}
		w.activeSessions.Add(1)
		w.activeCostCenti.Add(int64(job.CostCenti))
		w.store.AckDispatch(ctx, w.addr, job.CostCenti)
		// SafeGo so a panic inside runJob (bad source, malformed
		// ffmpeg output, surprise nil-map) decrements the counters
		// and is logged with a stack — without it the entire worker
		// process tears down on one bad job.
		j := *job
		observability.SafeGo(w.logger, "transcode-worker:run-job", func() {
			defer w.activeSessions.Add(-1)
			defer w.activeCostCenti.Add(-int64(j.CostCenti))
			if err := w.runJob(ctx, j); err != nil {
				w.logger.Error("transcode job failed",
					"session_id", j.SessionID, "err", err)
			}
		})
	}
}

// redactURLToken masks the token query value in a source URL so stream
// tokens don't land in worker logs. Everything from "token=" onward is
// replaced; the host/path stays visible for debugging.
func redactURLToken(raw string) string {
	if i := strings.Index(raw, "token="); i >= 0 {
		return raw[:i+len("token=")] + "REDACTED"
	}
	return raw
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

	// Resolve the session dir LOCALLY rather than trusting job.SessionDir.
	// job.SessionDir is computed on the dispatching server; on a remote worker
	// whose temp path differs (different OS user, OS, or TMPDIR) it points at a
	// directory that doesn't belong to this machine. The worker's own segment
	// server serves from SessionDir(id), so ffmpeg must write there too — using
	// job.SessionDir would scatter segments where the segment server can't find
	// them (and mismatch ffmpeg's working dir). On the embedded/single-box path
	// the two are identical, so this is a no-op there.
	sessionDir := SessionDir(job.SessionID)

	// Ensure session directory exists.
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("mkdir session dir: %w", err)
	}

	// Resolve the ffmpeg input. Prefer the local path (shared storage or the
	// embedded worker); fall back to the HTTP source URL when the file isn't
	// reachable on this worker's filesystem (a remote worker with no shared
	// mount). The primary serves it from /media/stream with a stream token,
	// and ffmpeg seeks via HTTP Range — see buildSourceURL in the API.
	input := job.FilePath
	if _, statErr := os.Stat(job.FilePath); statErr != nil && job.SourceURL != "" {
		input = job.SourceURL
		w.logger.Info("source not local; pulling over HTTP",
			"session_id", job.SessionID, "source", redactURLToken(job.SourceURL))
	}

	var ffArgs []string
	var actualEncoder Encoder
	// buildTranscodeArgs rebuilds the re-encode argv with QSV decode on/off and
	// an overridable start offset / segment number / playlist filename, so the
	// QSV-decode path can retry with software decode on an early failure and
	// the auto-continue path can resume past a premature EOF. nil for the
	// directStream branch (no QSV, no fallback, no auto-continue).
	var buildTranscodeArgs func(useQSV bool, startOffset float64, startNumber int, playlistName string) []string
	// qsvDecodeUsable: this run actually attempts QSV hardware decode (HEVC
	// re-encode on a QSV-enabled worker). Gates the fallback below.
	qsvDecodeUsable := false
	// cudaHevcUsable: this run attempts NVDEC HEVC hardware decode. Same gating
	// and software fallback as qsvDecodeUsable. Captured by buildTranscodeArgs
	// below, so clearing it on fallback also drops NVDEC from continuation runs.
	cudaHevcUsable := false
	// cudaHevcVRAMUsable: this run attempts the full-VRAM HEVC chain (cuvid→
	// scale_cuda→NVENC) for an SDR re-encode. Same gating/fallback; also captured
	// by buildTranscodeArgs so the software fallback drops it from later runs.
	cudaHevcVRAMUsable := false
	switch job.Decision {
	case "directStream":
		ffArgs = BuildDirectStream(input, sessionDir, job.StartOffsetSec)
	default:
		enc := Encoder(job.Encoder)
		if enc == "" {
			enc = BestEncoder(w.encoders)
		}
		// AV1 takes priority over HEVC when requested — the natural
		// trigger is "source is AV1, client supports AV1, we have an
		// AV1 encoder," which means re-encoding to AV1 is the
		// efficient choice (no AV1→HEVC waste). Fall back to HEVC,
		// then H.264, when no AV1 encoder is active. The bitrate the
		// API computed assumes HEVC; AV1 is roughly the same
		// efficiency tier so we leave it as-is.
		if job.PreferAV1 && !IsAV1Encoder(enc) {
			if av1 := BestAV1Encoder(w.encoders); av1 != "" {
				enc = av1
			}
		}
		// Use HEVC output encoder when requested and available.
		// If HEVC was requested but no HEVC encoder exists, restore the
		// H.264-grade bitrate — the API already scaled it down for HEVC.
		// Skip if AV1 already took the slot above.
		if job.PreferHEVC && !IsHEVCEncoder(enc) && !IsAV1Encoder(enc) {
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
			"start_offset_sec", job.StartOffsetSec,
			"tonemap", job.NeedsToneMap,
			"prefer_hevc", job.PreferHEVC,
			"prefer_av1", job.PreferAV1,
			"tonemap_cuda", w.hasTonemapCuda,
			"tonemap_opencl", w.hasTonemapOpenCL,
			"zscale", w.hasZscale,
			"libplacebo", w.hasLibplacebo,
		)

		buildTranscodeArgs = func(useQSV bool, startOffset float64, startNumber int, playlistName string) []string {
			return BuildHLS(BuildArgs{
				InputPath:            input,
				StartOffset:          startOffset,
				StartNumber:          startNumber,
				PlaylistName:         playlistName,
				Encoder:              enc,
				IsVAAPI:              enc == EncoderVAAPI || enc == EncoderHEVCVAAPI || enc == EncoderAV1VAAPI,
				IsHEVC:               job.IsHEVC,
				IsAV1:                job.IsAV1,
				Width:                job.Width,
				Height:               job.Height,
				BitrateKbps:          bitrate,
				NeedsToneMap:         job.NeedsToneMap,
				HasTonemapCuda:       w.hasTonemapCuda,
				HasTonemapOpenCL:     w.hasTonemapOpenCL,
				HasZscale:            w.hasZscale,
				HasLibplacebo:        w.hasLibplacebo,
				OpenCLDevice:         PickOpenCLDevice(w.openclDevices, enc),
				AudioCodec:           job.AudioCodec,
				AudioChannels:        job.AudioChannels,
				AudioStreamIndex:     job.AudioStreamIndex,
				SubtitleStreams:      job.SubtitleStreams,
				EncoderOpts:          w.encoderOpts,
				QSVDecode:            useQSV,
				CudaHevcDecode:       cudaHevcUsable,
				CudaHevcVRAM:         cudaHevcVRAMUsable,
				ReadRate:             1.0,
				ReadRateInitialBurst: 30,
				SessionDir:           sessionDir,
				SegmentPrefix:        "seg",
			})
		}
		// QSV / NVDEC decode are HEVC-only and not used for stream-copy (remux).
		// A host has at most one (QSV=Intel iGPU, NVDEC=NVIDIA dGPU); BuildArgs
		// gates NVDEC behind !QSVDecode so both being set can't double-decode.
		qsvDecodeUsable = w.qsvDecode && job.IsHEVC && enc != "copy"
		cudaHevcUsable = w.cudaHevcDecode && job.IsHEVC && enc != "copy"
		// Full-VRAM HEVC scale_cuda path is SDR-only: HDR re-encodes need the
		// libplacebo tonemap, which runs from the system-memory (cudaHevcUsable)
		// decode. BuildArgs picks VRAM over the system-memory path when both are
		// set, so the two don't conflict.
		cudaHevcVRAMUsable = w.cudaHevcScale && job.IsHEVC && !job.NeedsToneMap && enc != "copy"
		ffArgs = buildTranscodeArgs(qsvDecodeUsable, job.StartOffsetSec, 0, "")
		actualEncoder = enc
	}

	// Stamp the session with this worker's address and actual HEVC/AV1
	// output status. The API sets HEVCOutput / AV1Output based on
	// client preference, but the worker may have fallen back to a
	// different family if the requested encoder wasn't active.
	// Correct here so the playlist handler waits for the right segment
	// extension (.ts vs .m4s) — both fMP4 codec families use .m4s.
	actualHEVC := IsHEVCEncoder(actualEncoder)
	actualAV1 := IsAV1Encoder(actualEncoder)
	// AV1 source remux (`-c:v copy` of an AV1 stream) also lands in
	// fMP4 segments — mpegts has no AV1 stream type, so BuildHLS
	// switches the container regardless of the encoder string. Mark
	// the session so the playlist handler waits for seg00001.m4s.
	if string(actualEncoder) == "copy" && job.IsAV1 {
		actualAV1 = true
	}
	segExt := ".ts"
	if actualHEVC || actualAV1 {
		segExt = ".m4s"
	}

	// runFFmpeg execs ffmpeg with the given args and monitors it until exit or
	// kill. Returns the exit error and whether ffmpeg exited on its own
	// (selfExited) vs. we killed it for session-stop / idle / ctx-cancel.
	// Extracted so the QSV-decode path can retry with software decode.
	runFFmpeg := func(ffArgs []string) (exitErr error, selfExited bool) {
		w.logger.Info("ffmpeg args",
			"session_id", job.SessionID,
			"args", strings.Join(ffArgs, " "),
		)
		cmd := exec.CommandContext(ctx, "ffmpeg", ffArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		// Anchor ffmpeg's working dir to the session dir so bare
		// filenames in HLS muxer options (notably -hls_fmp4_init_filename)
		// land where the segment server expects them. Without this, the
		// fMP4 init segment is written to the server's launch dir and
		// the segment proxy 502s on every init fetch.
		cmd.Dir = sessionDir

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start ffmpeg: %w", err), true
		}

		if err := w.store.SetWorkerInfo(ctx, job.SessionID, w.id, w.addr, actualHEVC, actualAV1); err != nil {
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

		for {
			select {
			case err := <-done:
				if err != nil {
					w.logger.Warn("ffmpeg exited with error",
						"session_id", job.SessionID, "err", err)
				} else {
					w.logger.Info("ffmpeg completed", "session_id", job.SessionID)
				}
				return err, true
			case <-t.C:
				bg := context.Background()
				// If the session no longer exists in Valkey (client stopped it), kill FFmpeg.
				sess, err := w.store.Get(bg, job.SessionID)
				if err != nil {
					w.logger.Info("session deleted — killing ffmpeg", "session_id", job.SessionID)
					_ = cmd.Process.Kill()
					return nil, false
				}
				// Idle-kill: a client that closes its tab without firing
				// DELETE leaves ffmpeg encoding for the full 4 h session
				// TTL with `-readrate 1.0`. The Segment endpoint stamps
				// LastActivityAt on every segment fetch (~every 4 s of
				// playback), so a 60 s gap means the client crashed,
				// network dropped, or the user navigated away. Kill the
				// process to free the GPU and stop disk-fill.
				//
				// Grace period for the start-of-session window: until the
				// player has fetched its first segment, LastActivityAt is
				// zero. Use CreatedAt as the anchor so we don't kill a
				// session that's still buffering seg 0.
				const idleKillThreshold = 60 * time.Second
				anchor := sess.LastActivityAt
				if anchor.IsZero() {
					anchor = sess.CreatedAt
				}
				if !anchor.IsZero() && time.Since(anchor) > idleKillThreshold {
					w.logger.Info("client idle — killing ffmpeg",
						"session_id", job.SessionID,
						"last_activity_at", sess.LastActivityAt,
						"idle_for", time.Since(anchor).Round(time.Second))
					_ = cmd.Process.Kill()
					return nil, false
				}
				if err := w.store.SetHeartbeat(bg, job.SessionID); err != nil {
					w.logger.Warn("heartbeat write failed",
						"session_id", job.SessionID, "err", err)
				}
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				return ctx.Err(), false
			}
		}
	}

	exitErr, selfExited := runFFmpeg(ffArgs)
	// continuationUseQSV tracks whether an auto-continue run should use QSV
	// decode — true unless the QSV→software fallback below fired, in which
	// case QSV can't decode this source and the continuation must stay on
	// software too (the continuation path has no retry of its own).
	continuationUseQSV := qsvDecodeUsable
	// Hardware-decode fallback: if a hardware-decode run (QSV or NVDEC) died on
	// its own before producing any segment (the historical HW-decode failure
	// mode — ffmpeg aborts at decode init), retry once with software decode so a
	// source the GPU can't handle still plays. A kill (session stop / idle /
	// ctx) is not a decode failure, so selfExited gates this. Clearing
	// cudaHevcUsable (captured by buildTranscodeArgs) and continuationUseQSV also
	// drops hardware decode from the retry and any later continuation runs.
	if (qsvDecodeUsable || cudaHevcUsable || cudaHevcVRAMUsable) && selfExited && exitErr != nil && HighestSegmentIndex(sessionDir, segExt) < 0 {
		w.logger.Warn("hardware decode produced no segments; retrying with software decode",
			"session_id", job.SessionID, "qsv", qsvDecodeUsable, "nvdec", cudaHevcUsable, "cuda_scale", cudaHevcVRAMUsable, "err", exitErr)
		// Clear any partial output so segment numbering restarts at 0.
		_ = os.RemoveAll(sessionDir)
		_ = os.MkdirAll(sessionDir, 0755)
		continuationUseQSV = false
		cudaHevcUsable = false
		cudaHevcVRAMUsable = false
		exitErr, selfExited = runFFmpeg(buildTranscodeArgs(false, job.StartOffsetSec, 0, ""))
	}

	// Auto-continue: a clean ffmpeg exit (code 0) that stops short of the
	// source duration means the input had a defect — a corrupt cluster or
	// non-monotonic DTS — that made a linear `-c:v copy` demux hit a premature
	// EOF. Seeking past the bad spot reads the remainder fine, so we extend
	// the same HLS playlist rather than leaving the client stalled at the
	// buffer edge. directStream uses a different builder, so scope to BuildHLS.
	if job.Decision != "directStream" && buildTranscodeArgs != nil && selfExited && exitErr == nil {
		w.continueShortSession(ctx, job, sessionDir, segExt, input, continuationUseQSV, buildTranscodeArgs, runFFmpeg)
	}

	w.mu.Lock()
	delete(w.activeJobs, job.SessionID)
	w.mu.Unlock()

	// Clean up the session directory now that ffmpeg has exited.
	// Three paths land here: client DELETE (API also calls RemoveAll —
	// idempotent), ffmpeg natural completion, and ctx cancel from a
	// worker shutdown. The API's per-session DELETE is the common
	// case but isn't guaranteed to fire (browser tab closed, network
	// drop before the unload handler ran), and without this the
	// session's m4s/ts segments — hundreds of MB at 4K — sit on
	// disk until the next worker restart sweeps them.
	//
	// Delay the wipe so any in-flight client prefetches against the
	// just-killed session get a chance to drain. Without this, the
	// player happily fetches segments from the previous session for
	// a few hundred ms after a supersede and would 404 on a wiped
	// dir instead of failing cleanly via the revoked seg token.
	sessID := job.SessionID
	go func() {
		time.Sleep(30 * time.Second)
		if err := os.RemoveAll(SessionDir(sessID)); err != nil {
			w.logger.Warn("session dir cleanup",
				"session_id", sessID, "err", err)
		}
	}()

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

	// /seghead/{session_id}?ext=.ts — non-blocking; returns the highest
	// segment index produced so far (the encoder head), -1 if none. The API's
	// ABR reachability check uses this to decide wait-vs-restart for a remote
	// worker (the local-disk scan only works for a co-located embedded worker).
	mux.HandleFunc("/seghead/", func(rw http.ResponseWriter, r *http.Request) {
		sid := filepath.Base(r.URL.Path[len("/seghead/"):])
		if sid == "." || sid == ".." || sid == "" {
			http.Error(rw, "bad session", http.StatusBadRequest)
			return
		}
		ext := r.URL.Query().Get("ext")
		if ext == "" {
			ext = ".ts"
		}
		head := HighestSegmentIndex(filepath.Join(segmentBaseDir, sid), ext)
		fmt.Fprintf(rw, "%d", head)
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

// HighestSegmentIndex returns the largest segNNNNN<ext> index present in dir
// (the encoder's current head), or -1 if none. The ABR segment handler uses
// it — locally or via the worker's /seghead endpoint — to decide whether a
// requested segment is one the running child will reach soon (wait) or a
// forward seek past it (restart).
func HighestSegmentIndex(dir, ext string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return -1
	}
	hi := -1
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, "seg") || !strings.HasSuffix(n, ext) {
			continue
		}
		if idx, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(n, "seg"), ext)); err == nil && idx > hi {
			hi = idx
		}
	}
	return hi
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
