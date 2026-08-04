package transcode

import (
	"context"
	"crypto/subtle"
	"errors"
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
	"github.com/onscreen/onscreen/internal/valkey"
)

var tracer = otel.Tracer("onscreen/transcode")

var segmentBaseDir = filepath.Join(os.TempDir(), "onscreen", "sessions")

// segmentWaitTimeout is how long the segment server blocks waiting for ffmpeg
// to produce a requested segment before telling the client to retry. Holding
// the request open keeps the player on its buffer instead of showing a spinner
// — the same strategy Jellyfin uses. A var so tests can shorten it.
var segmentWaitTimeout = 10 * time.Second

// sourceProbe carries what the worker needs to decide how to re-encode a
// rotated source: the display rotation (degrees) and the source's overall
// bitrate (kbps, 0 if unknown).
type sourceProbe struct {
	rotationDeg int
	bitrateKbps int
}

// parseSourceProbe pulls rotation and bitrate out of ffprobe default-format
// ("key=value") lines. Rotation comes from the display-matrix side data
// ("rotation=") or the legacy stream tag ("rotate="), normalized to [0,360);
// bitrate from the container's "bit_rate=" (bits/s → kbps). Split out so the
// parsing is unit-testable without invoking ffprobe.
func parseSourceProbe(out string) sourceProbe {
	var p sourceProbe
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key, val := line[:eq], strings.TrimSpace(line[eq+1:])
		switch key {
		case "rotation", "rotate":
			if deg, err := strconv.Atoi(val); err == nil {
				if d := ((deg % 360) + 360) % 360; d != 0 && p.rotationDeg == 0 {
					p.rotationDeg = d
				}
			}
		case "bit_rate":
			if bps, err := strconv.ParseInt(val, 10, 64); err == nil && bps > 0 {
				p.bitrateKbps = int(bps / 1000)
			}
		}
	}
	return p
}

// probeSource probes the input's primary video stream for a display-matrix
// rotation plus the container bitrate. Portrait phone clips are stored as a
// landscape frame plus a "rotate 90°" matrix; a stream-copy remux into MPEG-TS
// drops that matrix and the clip plays sideways, so the worker forces a software
// re-encode (autorotate bakes the rotation in) when rotationDeg != 0. Returns a
// zero value on any probe error/timeout so a flaky probe never blocks playback.
func probeSource(ctx context.Context, input string) sourceProbe {
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(pctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream_side_data=rotation:stream_tags=rotate:format=bit_rate",
		"-of", "default=nw=1",
		input,
	).Output()
	if err != nil {
		return sourceProbe{}
	}
	return parseSourceProbe(string(out))
}

const heartbeatInterval = 2 * time.Second

// Worker is a transcode worker that picks up jobs from the Valkey queue,
// runs FFmpeg, and serves HLS segments via a local HTTP server (ADR-008).
type Worker struct {
	id               string
	addr             string // "host:port" — advertised to the API for segment proxying
	store            *SessionStore
	encoders         []Encoder
	encoderLabels    map[string]string // encoder → human label, detected once at startup
	cudaTonemap      atomic.Bool       // all-VRAM CUDA HDR→SDR (scale_cuda+tonemap_cuda) works here; probed in the background (cold JIT), flips on when warmed
	hasTonemapOpenCL bool              // tonemap_opencl filter available in FFmpeg
	hasZscale        bool              // zscale filter available (libzimg) for software tonemap
	hasLibfdkAAC     bool              // libfdk_aac encoder available; preferred over native aac (far faster first segment on multichannel)
	hasLibplacebo    bool              // libplacebo+Vulkan HDR→SDR tonemap works (GPU, vendor-agnostic; preferred over zscale)
	openclDevices    []OpenCLDevice    // platform.device list for `-init_hw_device opencl=ocl:...`
	encoderOpts      EncoderOpts       // per-deployment NVENC/maxrate tuning
	qsvDecode        bool              // opt-in QSV hardware HEVC decode (TRANSCODE_QSV_DECODE)
	qsvVRAM          bool              // opt-in full-VRAM Intel QSV (TRANSCODE_QSV_VRAM): qsv decode→vpp_qsv→qsv encode, all in VA surfaces. Off by default; validate on Intel HW
	vaapiVRAM        bool              // full-VRAM VAAPI (TRANSCODE_VAAPI_VRAM): vaapi hw-decode→scale_vaapi→vaapi encode, all in VA surfaces. Default on; activates only on a VAAPI encoder (SDR)
	vaapiTonemap     bool              // all-VRAM VAAPI HDR→SDR (scale_vaapi→tonemap_vaapi on VA surfaces) works here (startup probe); keeps HDR tonemap on the GPU instead of CPU zscale
	cudaHevcDecode   bool              // NVDEC HEVC decode works here (startup probe); offloads 4K HEVC decode to the GPU (system memory)
	readrateCatchup  bool              // ffmpeg build supports -readrate_catchup (startup probe); lets a transcode that fell behind real time rebuild its lead instead of staying pinned at the live edge
	encoderFailover  bool              // fail a hardware ENCODER over to the next configured provider when it can't acquire the GPU (e.g. GeForce NVENC 8-session cap → Intel iGPU QSV). Default on (TRANSCODE_ENCODER_FAILOVER); only fires when the box has a second provider
	cudaScale        atomic.Bool       // full-VRAM chain (cuvid→scale_cuda→NVENC) works here; probed in the background (cold scale_cuda JIT can take ~90s), flips on when warmed
	nodeID           string            // NODE_ID (hostname default) — reported so the admin can find this node in Settings ▸ Nodes
	logger           *slog.Logger
	activeSessions   atomic.Int32
	activeCostCenti  atomic.Int64 // summed CostCenti of in-flight jobs; reported for weighted dispatch
	maxSessions      atomic.Int32 // concurrent-session cap; live-adjustable via SetMaxSessions
	mu               sync.Mutex
	activeJobs       map[string]*os.Process // session_id → ffmpeg PID
	segAuthToken     string                 // bearer required on the segment server (SECRET_KEY-derived); empty = open (tests / no SECRET_KEY)
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

// SetQSVVRAM enables the full-VRAM Intel QSV path (TRANSCODE_QSV_VRAM): QSV
// decodes into VA surfaces, vpp_qsv scales in GPU memory, and the QSV encoder
// reads those surfaces — the Intel analogue of the NVDEC→scale_cuda→NVENC path.
// Off by default. SDR-only, single-GPU (QSV decode + QSV encode); falls back to
// software if it fails before the first segment. Validate on Intel hardware
// before enabling.
func (w *Worker) SetQSVVRAM(v bool) { w.qsvVRAM = v }

// SetVAAPIVRAM enables the full-VRAM VAAPI path (TRANSCODE_VAAPI_VRAM): VAAPI
// hardware-decodes into VA surfaces, scale_vaapi scales in GPU memory, and the
// VAAPI encoder reads those surfaces (no software decode, no hwupload). Off by
// default. SDR-only, single-GPU; software fallback. Validate before enabling.
func (w *Worker) SetVAAPIVRAM(v bool) { w.vaapiVRAM = v }

// SetEncoderFailover toggles hardware-encoder fail-over (TRANSCODE_ENCODER_FAILOVER,
// default on). When on, a job whose hardware encoder can't acquire the GPU — most
// commonly a GeForce NVENC worker hitting the driver's 8-session cap — is retried
// on the next encode provider the box has configured (e.g. the Intel iGPU's QSV,
// then software) instead of failing the stream. No effect on single-provider boxes,
// where the fail-over chain is empty.
func (w *Worker) SetEncoderFailover(v bool) { w.encoderFailover = v }

// SetNodeID records this worker's NODE_ID so its registration advertises the
// node identity its per-node config (Settings ▸ Nodes) is keyed by.
func (w *Worker) SetNodeID(id string) { w.nodeID = id }

// SetSegmentAuthToken sets the bearer the segment HTTP server requires on
// /segments/ and /seghead/, closing the otherwise-open endpoint to
// unauthenticated peers on the network. The API server attaches the same
// token (derived from SECRET_KEY via SegmentProxyToken). Empty leaves the
// server open — preserving behavior when SECRET_KEY isn't configured.
func (w *Worker) SetSegmentAuthToken(t string) { w.segAuthToken = t }

// segAuthOK reports whether a segment-server request carries the required
// bearer. Always true when no token is configured.
func (w *Worker) segAuthOK(r *http.Request) bool {
	if w.segAuthToken == "" {
		return true
	}
	const pfx = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) <= len(pfx) || h[:len(pfx)] != pfx {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(h[len(pfx):]), []byte(w.segAuthToken)) == 1
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
	hasTonemapOCL := ProbeFilter(ctx, "tonemap_opencl")
	hasZscale := ProbeFilter(ctx, "zscale")
	// Prefer libfdk_aac over native aac — far faster first segment on
	// multichannel audio (see BuildArgs.HasLibfdkAAC).
	hasLibfdkAAC := ProbeEncoder(ctx, "libfdk_aac")
	// GPU HDR→SDR via libplacebo (Vulkan) — the preferred tonemap path. Real
	// end-to-end probe (not just filter presence) so a host with the filter but
	// no working Vulkan device falls back to software zscale.
	hasLibplacebo := ProbeLibplaceboVulkan(ctx)
	// NVDEC HEVC decode offload. Real round-trip probe (NVENC-encode a tiny
	// HEVC clip, then NVDEC-decode it) so it's enabled only where mainline
	// ffmpeg + the driver survive NVDEC HEVC. Skip entirely unless an NVENC
	// encoder is present — no point probing on a non-NVIDIA worker.
	cudaHevcDecode := false
	hasNVENC := false
	for _, e := range encoders {
		if IsNVENCEncoder(e) {
			hasNVENC = true
			cudaHevcDecode = ProbeCudaHevcDecode(ctx)
			break
		}
	}
	// All-VRAM VAAPI HDR→SDR (scale_vaapi→tonemap_vaapi) — real round-trip probe so
	// the GPU HDR path is enabled only where the iHD/Mesa driver's tonemap_vaapi
	// actually works; otherwise HDR keeps the software zscale tonemap. Skip unless a
	// VAAPI encoder is present (no point probing on a non-VAAPI worker). Fast (~2-3s,
	// no cold-JIT like the CUDA probes), so it runs synchronously here.
	vaapiTonemap := false
	for _, e := range encoders {
		if IsVAAPIEncoder(e) {
			vaapiTonemap = ProbeTonemapVAAPI(ctx)
			break
		}
	}
	// Whether this ffmpeg build supports -readrate_catchup. Cheap parse-time
	// probe, gated so an older build never receives the unknown flag (which
	// would abort every transcode).
	readrateCatchup := ProbeReadrateCatchup(ctx)
	// Probe OpenCL platforms once at worker startup. Diagnostic only now that
	// the tonemap_opencl pipeline is retired — it lands in the "worker ready"
	// log so an operator can see what OpenCL the host exposes. Skipped
	// entirely when the filter isn't built in, since there is nothing to say.
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
		hasTonemapOpenCL: hasTonemapOCL,
		hasZscale:        hasZscale,
		hasLibfdkAAC:     hasLibfdkAAC,
		hasLibplacebo:    hasLibplacebo,
		cudaHevcDecode:   cudaHevcDecode,
		readrateCatchup:  readrateCatchup,
		encoderFailover:  true,         // default on; opt out via TRANSCODE_ENCODER_FAILOVER
		qsvVRAM:          true,         // "run in VRAM when possible" default; only activates when a QSV encoder is selected, per-job software fallback covers sources/HW where it fails
		vaapiVRAM:        true,         // same, for VAAPI; activates only when a VAAPI encoder is selected
		vaapiTonemap:     vaapiTonemap, // GPU HDR tonemap (probe-gated); keeps HDR off the CPU on VAAPI boxes
		openclDevices:    openclDevices,
		encoderOpts:      encOpts,
		logger:           logger,
		activeJobs:       make(map[string]*os.Process),
	}
	w.maxSessions.Store(int32(maxSessions))
	// Probe the CUDA filter chains in the BACKGROUND. The first run on a cold
	// CUDA JIT cache compiles the scale_cuda / tonemap_cuda kernels for this GPU
	// arch, which is CPU-bound and can take ~60-90s; running it synchronously
	// here would delay the HTTP server (NewWorker is called before
	// ListenAndServe) and trip the container health check into a restart loop.
	// With CUDA_CACHE_PATH on a persistent volume that cost is paid once, then
	// warm boots return in ~2s. Until they complete, the capabilities stay false
	// and HDR/SDR fall back to libplacebo / system-memory / software paths.
	if hasNVENC {
		go w.warmCudaFilters()
	}
	return w
}

// warmCudaFilters probes the GPU CUDA-filter chains (scale_cuda for SDR VRAM,
// scale_cuda+tonemap_cuda for HDR) and enables them if they work. Runs in its
// own goroutine (see NewWorker) so the cold CUDA JIT never blocks startup; each
// capability flips on once its kernels are compiled and cached. tonemap_cuda is
// probed for real because its kernels can fault on archs jellyfin hasn't
// validated (e.g. Blackwell) — there it stays off and HDR falls back.
func (w *Worker) warmCudaFilters() {
	start := time.Now()
	if ProbeCudaHevcScale(context.Background()) {
		w.cudaScale.Store(true)
		w.logger.Info("cuda_scale enabled (full-VRAM scale_cuda path)",
			"warmup", time.Since(start).Round(time.Second))
	} else {
		w.logger.Warn("cuda_scale unavailable; SDR HEVC/H.264 downscales stay on software scale",
			"elapsed", time.Since(start).Round(time.Second))
	}
	tm := time.Now()
	if ProbeTonemapCuda(context.Background()) {
		w.cudaTonemap.Store(true)
		w.logger.Info("tonemap_cuda enabled (all-VRAM CUDA HDR→SDR path)",
			"warmup", time.Since(tm).Round(time.Second))
	} else {
		w.logger.Warn("tonemap_cuda unavailable; HDR falls back to libplacebo / scale_cuda+zscale / software",
			"elapsed", time.Since(tm).Round(time.Second))
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

	openclSummary := make([]string, 0, len(w.openclDevices))
	for _, d := range w.openclDevices {
		openclSummary = append(openclSummary, d.Index+":"+d.PlatformName)
	}
	w.logger.Info("transcode worker ready",
		"id", w.id,
		"addr", w.addr,
		"encoders", EncoderNames(w.encoders),
		"max_sessions", int(w.maxSessions.Load()),
		"tonemap_cuda", w.cudaTonemap.Load(), // false at startup; probed async, flips on after the tonemap_cuda JIT warms
		"tonemap_vaapi", w.vaapiTonemap, // GPU HDR→SDR on VAAPI (probe-gated at startup)
		"tonemap_opencl", w.hasTonemapOpenCL,
		"zscale", w.hasZscale,
		"libfdk_aac", w.hasLibfdkAAC,
		"libplacebo", w.hasLibplacebo,
		"readrate_catchup", w.readrateCatchup,
		"nvdec_hevc", w.cudaHevcDecode,
		"cuda_scale", w.cudaScale.Load(), // false at startup; probed async, flips on after the scale_cuda JIT warms
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
		// hasTonemapOpenCL is deliberately absent: BuildHLS retired the
		// tonemap_opencl path, so a worker that has only that filter does its
		// HDR→SDR on the CPU via zscale. Advertising GPU tonemap on the
		// strength of it told the scheduler this node could take an HDR job
		// cheaply when it could not.
		HasGPUTonemap: w.hasLibplacebo || w.cudaTonemap.Load() || w.vaapiTonemap,
		RegisteredAt:  time.Now(),
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
	// server serves from this same path, so ffmpeg must write there too — using
	// job.SessionDir would scatter segments where the segment server can't find
	// them (and mismatch ffmpeg's working dir). On the embedded/single-box path
	// the two are identical, so this is a no-op there.
	//
	// Scoped to the job's INCARNATION so a restart that reuses its session ID
	// (every ABR rung seek does) gets a private directory instead of writing
	// seg00000 on top of the run it replaced. Incarnation 0 is the bare session
	// ID, so every other session keeps the historical path.
	sessionDir := SessionDirFor(job.SessionID, job.Incarnation)

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
	// enc is hoisted to function scope (rather than living inside the switch's
	// default case) so the encoder fail-over loop after the run can reassign it
	// and rebuild the ffmpeg args on the next provider. encFallbacks is the
	// ordered chain of alternate providers to try (empty unless this box has a
	// second one configured).
	var enc Encoder
	var encFallbacks []Encoder
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
	// cudaVRAMUsable: this run attempts the full-VRAM chain (cuvid→scale_cuda→
	// NVENC) for an SDR HEVC/H.264 re-encode. Same gating/fallback; also captured
	// by buildTranscodeArgs so the software fallback drops it from later runs.
	cudaVRAMUsable := false
	// qsvVRAMUsable / vaapiVRAMUsable: this run attempts the full-VRAM Intel path
	// (QSV: qsv decode→vpp_qsv→qsv encode; VAAPI: vaapi hw-decode→scale_vaapi→
	// vaapi encode). Opt-in + SDR-only + matching encoder; captured by
	// buildTranscodeArgs and cleared on fallback like the CUDA flags.
	qsvVRAMUsable := false
	vaapiVRAMUsable := false
	// vaapiTonemapUsable: all-VRAM VAAPI HDR (vaapi decode -> scale_vaapi ->
	// tonemap_vaapi -> vaapi encode) — the HDR analogue of vaapiVRAMUsable, keeping
	// HDR tonemap on the GPU. Same gating + per-job software fallback.
	vaapiTonemapUsable := false
	// cudaHDRUsable: this run attempts the GPU-assisted HDR chain (cuvid→
	// scale_cuda(p010)→hwdownload→zscale tonemap) — the fallback when tonemap_cuda
	// is unavailable. Same gating/fallback; also captured by buildTranscodeArgs.
	cudaHDRUsable := false
	// cudaTonemapUsable: this run attempts the all-VRAM CUDA HDR chain (cuvid→
	// scale_cuda→tonemap_cuda→NVENC) — the preferred HDR path on NVIDIA. Same
	// gating/fallback; also captured by buildTranscodeArgs.
	cudaTonemapUsable := false
	// Phone videos carry a rotation matrix (portrait footage stored as a
	// landscape frame + "rotate 90°"). A stream-copy remux into MPEG-TS drops
	// the matrix and the clip plays sideways, so force a full software re-encode:
	// ffmpeg's autorotate (on by default on the software-decode path) bakes the
	// rotation into the pixels. Encode at native (auto-rotated) resolution — no
	// scale/pad, so no pillarbox. Probe failures return 0 (treated as upright).
	probe := probeSource(ctx, input)
	rotated := probe.rotationDeg != 0
	decision := job.Decision
	if rotated {
		decision = "transcode"       // never stream-copy: that drops the rotation
		job.Width, job.Height = 0, 0 // encode at the auto-rotated source resolution
		// A remux job carries no target bitrate (stream copy needs none); supply
		// one for the forced re-encode from the source's own bitrate, with a
		// floor so a missing probe value can't starve the encode.
		if job.BitrateKbps <= 0 {
			job.BitrateKbps = probe.bitrateKbps
			if job.BitrateKbps <= 0 {
				job.BitrateKbps = 8000
			}
		}
		w.logger.Info("rotated source — forcing software re-encode to bake orientation",
			"session_id", job.SessionID, "rotation_deg", probe.rotationDeg, "bitrate_kbps", job.BitrateKbps)
	}
	switch decision {
	case "directStream":
		ffArgs = BuildDirectStream(input, sessionDir, job.StartOffsetSec)
	default:
		enc = Encoder(job.Encoder)
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

		// Rotated source: force the pure-software encoder so the whole pipeline
		// (decode + encode) runs on the CPU and ffmpeg's autorotate applies. A
		// hardware decode keeps frames in VRAM where autorotate can't run, so the
		// rotation would still be lost. libx264 on a 1080p phone clip is cheap.
		if rotated {
			enc = EncoderSoftware
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
			"tonemap_cuda", w.cudaTonemap.Load(),
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
				OutputTSOffsetSec:    job.OutputTSOffsetSec,
				Encoder:              enc,
				IsVAAPI:              enc == EncoderVAAPI || enc == EncoderHEVCVAAPI || enc == EncoderAV1VAAPI,
				IsHEVC:               job.IsHEVC,
				IsAV1:                job.IsAV1,
				IsH264:               job.IsH264,
				Width:                job.Width,
				Height:               job.Height,
				BitrateKbps:          bitrate,
				NeedsToneMap:         job.NeedsToneMap,
				CudaTonemap:          cudaTonemapUsable,
				HasZscale:            w.hasZscale,
				ForceFMP4:            job.ForceFMP4,
				HasLibfdkAAC:         w.hasLibfdkAAC,
				HasLibplacebo:        w.hasLibplacebo,
				AudioCodec:           job.AudioCodec,
				AudioChannels:        job.AudioChannels,
				AudioStreamIndex:     job.AudioStreamIndex,
				SubtitleStreams:      job.SubtitleStreams,
				EncoderOpts:          w.encoderOpts,
				QSVDecode:            useQSV,
				QSVVRAM:              qsvVRAMUsable,
				VAAPIVRAM:            vaapiVRAMUsable,
				VAAPITonemap:         vaapiTonemapUsable,
				CudaHevcDecode:       cudaHevcUsable,
				CudaVRAM:             cudaVRAMUsable,
				CudaHDRTonemap:       cudaHDRUsable,
				ReadRate:             1.0,
				ReadRateInitialBurst: 60,
				ReadrateCatchup:      readrateCatchupRate(w.readrateCatchup),
				SessionDir:           sessionDir,
				SegmentPrefix:        "seg",
			})
		}
		// QSV / NVDEC decode are HEVC-only and not used for stream-copy (remux).
		// A host has at most one (QSV=Intel iGPU, NVDEC=NVIDIA dGPU); BuildArgs
		// gates NVDEC behind !QSVDecode so both being set can't double-decode.
		qsvDecodeUsable = w.qsvDecode && job.IsHEVC && enc != "copy"
		// Full-VRAM Intel paths (opt-in, default off): SDR-only, and only when the
		// SAME Intel GPU both decodes and encodes — i.e. the encoder is a QSV
		// (resp. VAAPI) encoder. The decoder is driven off the source codec in
		// BuildArgs. Cleared on fallback below like the CUDA flags. qsvVRAM takes
		// the system-memory QSVDecode's place when set (BuildArgs gates QSVDecode
		// behind !QSVVRAM).
		qsvVRAMUsable = w.qsvVRAM && !job.NeedsToneMap && IsQSVEncoder(enc) &&
			(job.IsHEVC || job.IsH264 || job.IsAV1) && enc != "copy"
		vaapiVRAMUsable = w.vaapiVRAM && !job.NeedsToneMap && IsVAAPIEncoder(enc) &&
			(job.IsHEVC || job.IsH264 || job.IsAV1) && enc != "copy"
		// All-VRAM VAAPI HDR (the HDR counterpart of vaapiVRAMUsable): keep the
		// HDR→SDR tonemap on the GPU (scale_vaapi→tonemap_vaapi) instead of CPU
		// zscale. Probe-gated (w.vaapiTonemap); software-zscale fallback on failure.
		vaapiTonemapUsable = w.vaapiTonemap && job.NeedsToneMap && IsVAAPIEncoder(enc) &&
			(job.IsHEVC || job.IsH264 || job.IsAV1) && enc != "copy"
		cudaHevcUsable = w.cudaHevcDecode && job.IsHEVC && enc != "copy"
		// Full-VRAM scale_cuda path is SDR-only (HEVC or H.264): HDR re-encodes
		// need the libplacebo tonemap, which runs from the system-memory
		// (cudaHevcUsable) decode. BuildArgs picks VRAM over the system-memory
		// path when both are set, so the two don't conflict. H.264 shares the
		// HEVC-validated probe — h264_cuvid is at least as reliable.
		cudaVRAMUsable = w.cudaScale.Load() && (job.IsHEVC || job.IsH264) && !job.NeedsToneMap && enc != "copy"
		// All-VRAM CUDA HDR (preferred on NVIDIA): cuvid→scale_cuda→tonemap_cuda
		// →NVENC, no Vulkan. The HDR analogue of cudaVRAMUsable.
		cudaTonemapUsable = w.cudaTonemap.Load() && (job.IsHEVC || job.IsH264) && job.NeedsToneMap &&
			IsNVENCEncoder(enc) && enc != "copy"
		// GPU-assisted HDR fallback (scale_cuda + CPU zscale tonemap): used when
		// tonemap_cuda is unavailable AND libplacebo is unavailable. Requires the
		// scale_cuda capability + zscale + NVENC. tonemap_cuda and libplacebo both
		// take precedence (in BuildArgs), so gate this off when either applies.
		cudaHDRUsable = !cudaTonemapUsable && w.cudaScale.Load() && (job.IsHEVC || job.IsH264) && job.NeedsToneMap &&
			w.hasZscale && !w.hasLibplacebo && IsNVENCEncoder(enc) && enc != "copy"
		ffArgs = buildTranscodeArgs(qsvDecodeUsable, job.StartOffsetSec, 0, "")
		actualEncoder = enc
		// Build the encoder fail-over chain for this provider (NVENC→QSV→…→
		// software, same output codec family). Empty unless this box has a second
		// provider configured — so single-GPU workers behave exactly as before.
		if w.encoderFailover {
			encFallbacks = FallbackEncoders(enc, w.encoders)
		}
	}

	// Stamp the session with this worker's address and actual HEVC/AV1
	// output status. The API sets HEVCOutput / AV1Output based on
	// client preference, but the worker may have fallen back to a
	// different family if the requested encoder wasn't active.
	// Correct here so the playlist handler waits for the right segment
	// extension (.ts vs .m4s) — both fMP4 codec families use .m4s.
	//
	// This MUST use the same resolver BuildHLS uses. These flags are what the
	// API's playlist-readiness probe and the ABR segment proxy predict the
	// container from, so any disagreement with what ffmpeg actually wrote is a
	// playlist that never becomes ready. Remux is the case that breaks a naive
	// IsHEVCEncoder(encoder) — see videooutput.go.
	vout := ResolveVideoOutput(actualEncoder, job.IsHEVC, job.IsAV1, job.ForceFMP4)
	actualHEVC, actualAV1 := vout.HEVC, vout.AV1
	segExt := vout.SegExt()

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

		// Track PID for kill on session stop. Keyed by incarnation-scoped dir
		// name, not bare session ID: during a rung restart the old and new runs
		// briefly coexist under one session ID, and a shared key would let the
		// dying run's map cleanup erase the live successor's PID — leaving
		// KillSession with nothing to kill. KillSession matches all
		// incarnations of a session ID.
		w.mu.Lock()
		w.activeJobs[SessionDirName(job.SessionID, job.Incarnation)] = cmd.Process
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
				sess, err := w.store.Get(bg, job.SessionID)
				verdict := watchdogVerdict(sess, err, job.Incarnation, time.Now())
				switch verdict {
				case watchdogKeep:
					// fall through to the heartbeat write below
				case watchdogStoreError:
					w.logger.Warn("session read failed — keeping ffmpeg alive",
						"session_id", job.SessionID, "err", err)
					continue
				default:
					w.logger.Info("watchdog killing ffmpeg",
						"session_id", job.SessionID,
						"reason", verdict,
						"job_incarnation", job.Incarnation)
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
	if (qsvDecodeUsable || qsvVRAMUsable || vaapiVRAMUsable || vaapiTonemapUsable || cudaHevcUsable || cudaVRAMUsable || cudaHDRUsable || cudaTonemapUsable) && selfExited && exitErr != nil && HighestSegmentIndex(sessionDir, segExt) < 0 {
		w.logger.Warn("hardware decode produced no segments; retrying with software decode",
			"session_id", job.SessionID, "qsv", qsvDecodeUsable, "qsv_vram", qsvVRAMUsable, "vaapi_vram", vaapiVRAMUsable, "vaapi_tonemap", vaapiTonemapUsable, "nvdec", cudaHevcUsable, "cuda_scale", cudaVRAMUsable, "cuda_hdr", cudaHDRUsable, "tonemap_cuda", cudaTonemapUsable, "err", exitErr)
		// Clear any partial output so segment numbering restarts at 0.
		_ = os.RemoveAll(sessionDir)
		_ = os.MkdirAll(sessionDir, 0755)
		continuationUseQSV = false
		cudaHevcUsable = false
		cudaVRAMUsable = false
		cudaHDRUsable = false
		cudaTonemapUsable = false
		// Drop the full-VRAM Intel paths too — the retry keeps the HW encoder but
		// software-decodes (QSV/VAAPI default path; VAAPI HDR falls back to CPU
		// zscale tonemap), which is the safe fallback.
		qsvVRAMUsable = false
		vaapiVRAMUsable = false
		vaapiTonemapUsable = false
		exitErr, selfExited = runFFmpeg(buildTranscodeArgs(false, job.StartOffsetSec, 0, ""))
	}

	// Encoder fail-over: a hardware ENCODER that can't acquire the GPU self-exits
	// at init with no segment produced. The classic case is a GeForce NVENC worker
	// hitting the driver's 8-concurrent-session cap (the consumer-card limit), but a
	// QSV/VAAPI device momentarily out of surfaces looks identical. When this box
	// has another encode provider configured (e.g. the laptop's Intel iGPU QSV
	// alongside its RTX 4080), retry the job on the next provider instead of failing
	// the stream — same failure signature as the hardware-decode fallback above:
	// selfExited, non-nil err, zero segments on disk. A kill (session stop / idle /
	// ctx-cancel) clears selfExited, so a stopped session never triggers fail-over.
	// encFallbacks is empty on single-provider boxes, so the loop is a no-op there.
	for _, fb := range encFallbacks {
		if !(selfExited && exitErr != nil && HighestSegmentIndex(sessionDir, segExt) < 0) {
			break // produced output (or was killed) — keep the current encoder
		}
		w.logger.Warn("encoder could not acquire device; failing over to next provider",
			"session_id", job.SessionID, "from", enc, "to", fb, "err", exitErr)
		// Reset partial output so segment numbering restarts at 0.
		_ = os.RemoveAll(sessionDir)
		_ = os.MkdirAll(sessionDir, 0755)
		enc = fb
		// Conservative path on the fallback provider: software decode + hardware
		// encode, no full-VRAM chains (those are gated on the original GPU).
		continuationUseQSV = false
		// (qsvDecodeUsable isn't cleared here: the fallback run passes useQSV=false
		// to buildTranscodeArgs explicitly, so the flag is never re-read.)
		cudaHevcUsable = false
		cudaVRAMUsable = false
		cudaHDRUsable = false
		cudaTonemapUsable = false
		qsvVRAMUsable = false
		vaapiVRAMUsable = false
		vaapiTonemapUsable = false
		// Re-stamp the output codec family for the new encoder. FallbackEncoders
		// keeps the same family, so segExt is unchanged in practice — re-derive it
		// (and the session's HEVC/AV1 flags, via runFFmpeg → SetWorkerInfo)
		// defensively so the playlist handler waits on the right segment extension.
		actualEncoder = enc
		vout = ResolveVideoOutput(actualEncoder, job.IsHEVC, job.IsAV1, job.ForceFMP4)
		actualHEVC, actualAV1 = vout.HEVC, vout.AV1
		segExt = vout.SegExt()
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
	delete(w.activeJobs, SessionDirName(job.SessionID, job.Incarnation))
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
	//
	// Natural completion is special: with the ReadRateInitialBurst the
	// encoder finishes ~burst seconds AHEAD of the player (it front-loads
	// that many seconds, then paces at 1x, so it hits EOF before playback
	// does). A flat 30 s wipe would then delete the file's tail out from
	// under a client that's still draining its buffer — the player would
	// 404 in the final seconds of the movie. Give natural completion a
	// longer grace that comfortably exceeds the burst so the wipe lands
	// after the player has finished. Kills (supersede / idle / DELETE /
	// ctx) happen AT or BEHIND the player's position, so segments behind
	// the kill point are already consumed — their 30 s in-flight-drain
	// grace is unchanged.
	wipeDelay := 30 * time.Second
	if selfExited && exitErr == nil {
		wipeDelay = 120 * time.Second
	}
	go w.wipeSessionDir(job.SessionID, job.Incarnation, wipeDelay)

	return nil
}

// watchdogDecision is the per-heartbeat verdict on a running ffmpeg.
type watchdogDecision string

const (
	watchdogKeep       watchdogDecision = "keep"
	watchdogStoreError watchdogDecision = "store-error" // keep alive, log — store unreachable proves nothing
	watchdogGone       watchdogDecision = "session-gone"
	watchdogSuperseded watchdogDecision = "superseded"
	watchdogIdle       watchdogDecision = "client-idle"
)

// idleKillThreshold is how long a session may go without segment fetches (or,
// pre-first-segment, since creation) before its encoder is reaped. The Segment
// endpoint stamps LastActivityAt on every fetch — ~every 4 s of real playback.
const idleKillThreshold = 60 * time.Second

// watchdogVerdict decides what the heartbeat loop should do with its ffmpeg,
// given the current stored session (or the read error). Pure so every branch is
// testable without a process:
//
//   - Store errors other than not-found KEEP the encoder. Killing on any error
//     made Valkey a fleet-wide single point of failure: one restart or Sentinel
//     failover errored every concurrent Get and every worker shot every stream
//     on the deployment simultaneously. An unreachable store proves nothing
//     about whether the client is still watching.
//   - A definitive not-found kills: the client stopped the session.
//   - An incarnation ahead of the job's kills: the session ID was restarted (an
//     ABR rung seek) and THIS process is the superseded run. The key still
//     exists, so the not-found branch can never catch this.
//   - An idle session (no segment fetch for idleKillThreshold; CreatedAt
//     anchors the pre-first-segment window) kills: tab closed without DELETE.
func watchdogVerdict(sess *Session, getErr error, jobIncarnation int, now time.Time) watchdogDecision {
	if getErr != nil {
		if errors.Is(getErr, valkey.ErrNotFound) {
			return watchdogGone
		}
		return watchdogStoreError
	}
	if sess.Incarnation != jobIncarnation {
		return watchdogSuperseded
	}
	anchor := sess.LastActivityAt
	if anchor.IsZero() {
		anchor = sess.CreatedAt
	}
	if !anchor.IsZero() && now.Sub(anchor) > idleKillThreshold {
		return watchdogIdle
	}
	return watchdogKeep
}

// sessionDirWipeMaxWait bounds how long wipeSessionDir will keep deferring to a
// session that is still alive. Long enough that a viewer can pause near the end
// of a film and come back to a working stream; short enough that an abandoned
// directory doesn't outlive the day. Past it we reclaim the disk regardless —
// the session's own idle-kill fires at 60 s of no segment fetches, so anything
// still standing here has been checked in on repeatedly.
var sessionDirWipeMaxWait = 30 * time.Minute

// sessionDirWipePoll is how often wipeSessionDir re-checks a still-live session.
var sessionDirWipePoll = 60 * time.Second

// wipeSessionDir deletes one incarnation's segment directory once nothing needs
// it any more.
//
// It deliberately does NOT delete on a timer alone. ffmpeg exiting is not the
// same as playback ending: with the read-rate burst the encoder finishes well
// ahead of the viewer, so a flat post-completion delay deleted the tail of the
// film out from under anyone who paused for a few minutes — the stream came
// back 404ing on its last segments. And on an ABR rung the exiting run may have
// already been replaced by a newer incarnation, whose directory this must never
// touch.
//
// So: only wipe when the session is genuinely finished with — gone from the
// store, or moved on to another incarnation — and otherwise keep checking back
// until sessionDirWipeMaxWait.
func (w *Worker) wipeSessionDir(sessionID string, incarnation int, delay time.Duration) {
	dir := SessionDirFor(sessionID, incarnation)
	remove := func(reason string) {
		if err := os.RemoveAll(dir); err != nil {
			w.logger.Warn("session dir cleanup", "session_id", sessionID, "dir", dir, "err", err)
			return
		}
		w.logger.Debug("session dir wiped", "session_id", sessionID, "dir", dir, "reason", reason)
	}

	time.Sleep(delay)
	deadline := time.Now().Add(sessionDirWipeMaxWait)
	for {
		sess, err := w.store.Get(context.Background(), sessionID)
		switch {
		case errors.Is(err, valkey.ErrNotFound):
			// Client stopped, superseded and deleted, or reaped. Nobody can ask
			// for these segments again.
			remove("session gone")
			return
		case err != nil:
			// Store unreachable. Deleting on a read failure would turn a Valkey
			// blip into deleted segments for live streams, so wait it out.
			if time.Now().After(deadline) {
				w.logger.Warn("session dir cleanup deferred past deadline; leaving for restart sweep",
					"session_id", sessionID, "dir", dir, "err", err)
				return
			}
		case sess.Incarnation != incarnation:
			// A newer run owns the session ID now. Our directory is ours alone
			// (SessionDirName is incarnation-scoped), so removing it cannot
			// touch the successor's files.
			remove("superseded")
			return
		case time.Now().After(deadline):
			remove("max wait elapsed")
			return
		}
		time.Sleep(sessionDirWipePoll)
	}
}

// KillSession terminates every in-progress FFmpeg process for a session — the
// current incarnation and, during a restart window, the one it superseded.
// activeJobs is keyed by SessionDirName, so incarnation 0 is the bare ID and
// later ones carry the "-iN" suffix.
func (w *Worker) KillSession(sessionID string) {
	prefix := sessionID + "-i"
	w.mu.Lock()
	var procs []*os.Process
	for key, p := range w.activeJobs {
		if key == sessionID || strings.HasPrefix(key, prefix) {
			procs = append(procs, p)
		}
	}
	w.mu.Unlock()
	for _, p := range procs {
		_ = p.Kill()
	}
}

// segmentHandler serves files from segmentBaseDir/{session_id}/{filename}.
// Extracted from startSegmentServer so the not-ready status can be tested
// without standing up a listener.
func (w *Worker) segmentHandler() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		if !w.segAuthOK(r) {
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return
		}
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
		deadline := time.Now().Add(segmentWaitTimeout)
		for {
			if _, err := os.Stat(clean); err == nil {
				break
			}
			if time.Now().After(deadline) {
				// 503, NOT 404. A segment that ffmpeg has not reached yet is
				// TRANSIENT, and every other not-ready path in the tree says so
				// with 503 (playlist not ready, ABR init not ready, ABR segment
				// unavailable). This one said 404, which to an HLS player means
				// "this will never exist" — so instead of retrying, the player
				// skips the segment and leaves a gap. The visible result is a
				// stream that alternates picture and black as ready and
				// not-yet-ready segments interleave, with nothing in the logs to
				// suggest anything is wrong.
				//
				// Retry-After tells the player how long to back off; the
				// transcode produces roughly one segment per segment-duration,
				// so a short retry is right.
				rw.Header().Set("Retry-After", "1")
				http.Error(rw, "segment not ready", http.StatusServiceUnavailable)
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(250 * time.Millisecond):
			}
		}

		http.ServeFile(rw, r, clean)
	}
}

// startSegmentServer runs a minimal HTTP server to serve HLS segments.
// The API proxy forwards segment requests to this server.
func (w *Worker) startSegmentServer(ctx context.Context) {
	mux := http.NewServeMux()

	// Serve files from /tmp/onscreen/sessions/{session_id}/
	mux.HandleFunc("/segments/", w.segmentHandler())

	// /seghead/{session_id}?ext=.ts — non-blocking; returns the highest
	// segment index produced so far (the encoder head), -1 if none. The API's
	// ABR reachability check uses this to decide wait-vs-restart for a remote
	// worker (the local-disk scan only works for a co-located embedded worker).
	mux.HandleFunc("/seghead/", func(rw http.ResponseWriter, r *http.Request) {
		if !w.segAuthOK(r) {
			http.Error(rw, "unauthorized", http.StatusUnauthorized)
			return
		}
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

// SessionDirName is the directory name — and the segment-server URL path
// element, which must match it — that one INCARNATION of a session writes to.
//
// Incarnation 0 is the bare session ID, so every non-ABR session and every
// first start keeps the historical layout byte-for-byte. Only an ABR rung
// restart (which reuses its session ID by design) gets a suffix, which is what
// stops a superseded ffmpeg and its deferred cleanup from colliding with the
// successor that took its ID. See Session.Incarnation.
func SessionDirName(sessionID string, incarnation int) string {
	if incarnation <= 0 {
		return sessionID
	}
	return sessionID + "-i" + strconv.Itoa(incarnation)
}

// SessionDirFor is SessionDir for a specific incarnation.
func SessionDirFor(sessionID string, incarnation int) string {
	return filepath.Join(segmentBaseDir, SessionDirName(sessionID, incarnation))
}

// DirName is the segment directory name for this session's current
// incarnation. The API uses it to build both local paths and worker
// segment-server URLs, so both sides agree on where a rung's files live.
func (s *Session) DirName() string {
	return SessionDirName(s.ID, s.Incarnation)
}

// Dir is the local filesystem path for this session's current incarnation.
func (s *Session) Dir() string {
	return SessionDirFor(s.ID, s.Incarnation)
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
