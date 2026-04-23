package livetv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// hlsStreamLifetime is the soft cap on how long a single HLS session is
// kept alive after the last viewer disconnects. Prevents stream churn when
// users navigate between pages — the same channel pulled within this
// window reuses the existing tuner slot rather than re-tuning.
//
// Value chosen empirically: short enough that an unplugged TV releases the
// tuner in well under a minute; long enough that page-refresh / nav-away-
// nav-back doesn't burn a re-tune.
const hlsStreamLifetime = 30 * time.Second

// hlsSegmentDuration is the target HLS segment length passed to ffmpeg.
// 2s gives the player finer-grained buffering (less chance of underflow
// during a brief encoder stall) and cuts initial channel-change latency
// — most players start playback 3 segments behind the live edge, so
// 2s × 3 = 6s of startup vs 4s × 3 = 12s. Trade-off is more files on
// disk and slightly more request overhead, both negligible.
const hlsSegmentDuration = 2

// hlsListSize is the number of segments visible in the playlist (and
// kept on disk via delete_segments). 10 × 2s = 20s buffer — enough
// rollback room for client jitter without growing unboundedly.
const hlsListSize = 10

// keyframeIntervalFrames is how many encoded frames between forced
// keyframes. Must match segment duration × source FPS so each segment
// starts on a keyframe — otherwise ffmpeg either inserts extra
// keyframes (CPU+bitrate spikes) or some segments lack a keyframe at
// position 0 (player can't start playback at the live edge cleanly).
//
// Broadcast TV is 29.97/30 fps in the US and 25 fps in EU; 60 frames
// × ~33ms = ~2s, matching hlsSegmentDuration. Slightly off for 25 fps
// (60 / 25 = 2.4s) which the muxer absorbs gracefully via the
// EXT-X-TARGETDURATION upper bound.
const keyframeIntervalFrames = 60

// HLSConfig configures the proxy. Dir is where per-session subdirectories
// are created — must be writable by the server process. FFmpegBin is the
// ffmpeg binary path (defaults to "ffmpeg" if empty, picked up from PATH).
//
// VideoEncoder names the ffmpeg encoder for the video stream. Defaults to
// "libx264" — we cannot stream-copy because broadcast TV (US OTA in
// particular) is typically MPEG-2, which browsers can't decode. Set to
// "h264_nvenc" / "h264_amf" / "h264_qsv" when hardware acceleration is
// available; the embedded transcode subsystem already auto-detects these.
//
// AudioEncoder is similarly transcoded by default — broadcast audio is
// usually AC-3 (Dolby Digital) which Safari handles but Chrome/Firefox
// don't. Defaults to "aac" which every browser plays.
type HLSConfig struct {
	Dir           string
	FFmpegBin     string
	VideoEncoder  string
	AudioEncoder  string
}

// HLSSession represents one active per-channel session. The first viewer
// for a channel creates it; subsequent viewers increment refcount and
// share the same on-disk playlist + segments.
type HLSSession struct {
	channelID uuid.UUID
	dir       string

	mu        sync.Mutex
	refcount  int
	closing   *time.Timer // grace-period timer scheduled when refcount hits 0
	cmd       *exec.Cmd
	upstream  Stream
	cancel    context.CancelFunc
	closed    bool
	stderrBuf *ringBuffer // last few KB of ffmpeg stderr, for crash diagnostics
}

// PlaylistPath returns the absolute path to this session's master HLS
// playlist. The HTTP handler reads it and either streams the bytes back
// or 404s when the playlist hasn't been written yet (ffmpeg startup
// race).
func (s *HLSSession) PlaylistPath() string { return filepath.Join(s.dir, "playlist.m3u8") }

// SegmentPath returns the absolute path to a named segment inside this
// session's directory. Caller is responsible for sanitizing the name (the
// HTTP handler restricts it to ^seg-\d+\.ts$).
func (s *HLSSession) SegmentPath(name string) string { return filepath.Join(s.dir, name) }

// NewSessionForTest constructs an HLSSession backed by `dir` without
// spinning ffmpeg. Exists so handler tests in another package can hand
// the proxy stub a real session value (HLSSession's fields are
// unexported so cross-package tests can't synthesize one). NOT for
// production use.
func NewSessionForTest(channelID uuid.UUID, dir string) *HLSSession {
	return &HLSSession{channelID: channelID, dir: dir, refcount: 1}
}

// hlsStreamSource is the slice of *Service the proxy uses. Extracted as
// an interface so tests can substitute a stub instead of standing up the
// DB+driver layers.
type hlsStreamSource interface {
	OpenChannelStream(ctx context.Context, channelID uuid.UUID) (Stream, error)
}

// HLSProxy multiplexes channel streams into per-channel HLS sessions with
// reference counting. Concurrency model: one mutex protects the sessions
// map; per-session state has its own mutex so two viewers on different
// channels don't contend.
type HLSProxy struct {
	cfg    HLSConfig
	svc    hlsStreamSource
	logger *slog.Logger

	mu       sync.Mutex
	sessions map[uuid.UUID]*HLSSession
}

// NewHLSProxy wires the proxy. The session directory is created on first
// use, not at construction, so an unconfigured live-TV deployment doesn't
// create stray directories.
func NewHLSProxy(cfg HLSConfig, svc *Service, logger *slog.Logger) *HLSProxy {
	if cfg.FFmpegBin == "" {
		cfg.FFmpegBin = "ffmpeg"
	}
	if cfg.VideoEncoder == "" {
		cfg.VideoEncoder = "libx264"
	}
	if cfg.AudioEncoder == "" {
		cfg.AudioEncoder = "aac"
	}
	return &HLSProxy{cfg: cfg, svc: svc, logger: logger, sessions: make(map[uuid.UUID]*HLSSession)}
}

// Acquire returns the active session for a channel, creating + starting
// it if none exists. Refcount is incremented; caller MUST call Release
// exactly once. Returns ErrAllTunersBusy verbatim from the driver so the
// HTTP handler can render the right error.
func (p *HLSProxy) Acquire(ctx context.Context, channelID uuid.UUID) (*HLSSession, error) {
	p.mu.Lock()
	if s, ok := p.sessions[channelID]; ok {
		s.mu.Lock()
		s.refcount++
		// Cancel any pending close timer — a new viewer arrived during the
		// grace period.
		if s.closing != nil {
			s.closing.Stop()
			s.closing = nil
		}
		s.mu.Unlock()
		p.mu.Unlock()
		return s, nil
	}
	p.mu.Unlock()

	// Session lifetime is decoupled from the request context: the
	// playlist GET that triggers Acquire returns in seconds, but the
	// underlying HDHomeRun HTTP body is the entire tune session. Using
	// the request ctx for upstream would close the upstream the moment
	// the first playlist response completes, killing ffmpeg's stdin —
	// no segments would ever be written.
	streamCtx, cancel := context.WithCancel(context.Background())

	// Open upstream against the session ctx so it lives as long as the
	// session does. Acquire's caller still gets request-scoped errors
	// because OpenChannelStream's HTTP request happens synchronously
	// and returns before the body is read.
	upstream, err := p.svc.OpenChannelStream(streamCtx, channelID)
	if err != nil {
		cancel()
		return nil, err
	}

	if err := os.MkdirAll(p.cfg.Dir, 0o755); err != nil {
		cancel()
		upstream.Close()
		return nil, fmt.Errorf("hls dir: %w", err)
	}
	dir, err := os.MkdirTemp(p.cfg.Dir, "ch-*")
	if err != nil {
		cancel()
		upstream.Close()
		return nil, fmt.Errorf("hls session dir: %w", err)
	}
	// We can't `-c copy` because broadcast TV is typically MPEG-2 video +
	// AC-3 audio — neither plays in browsers via HLS. Transcode to H.264
	// + AAC. NVENC/AMF/QSV when available drops CPU to near-zero per
	// stream; libx264 fallback is the floor at ~15-25% of one CPU core.
	//
	// Bitrate target 6 Mbps + 8 Mbps maxrate matches what cable providers
	// use for 1080p H.264 — preserves the visible quality of broadcast HD
	// without bloating segment files. NVENC's default of ~2 Mbps was
	// catastrophic on 1080p (the original "looks bad" complaint).
	//
	// Colorspace tagging is critical on HDR displays: without explicit
	// bt709 metadata the browser/compositor may interpret SDR pixels with
	// an HDR matrix, producing washed-out or oversaturated output. We
	// tag the encoded stream as Rec.709 SDR — which is what 99% of
	// broadcast TV actually is — so HDR monitors render it in the SDR
	// sub-range correctly.
	//
	// -bsf:a aac_adtstoasc fixes a common HLS muxer warning when AAC
	// audio crosses segment boundaries.
	// -sn drops subtitles; broadcast TS often carries closed-caption
	// streams ffmpeg can't mux into HLS.
	// -pix_fmt yuv420p ensures universal browser/HW decoder compatibility
	// (some broadcasts are 4:2:2 which Chromium can't decode).
	args := []string{
		"-fflags", "+genpts+discardcorrupt",
		"-i", "pipe:0",
		"-map", "0:v:0", // first video stream only
		"-map", "0:a:0?", // first audio stream if present
		"-sn",
		"-c:v", p.cfg.VideoEncoder,
		"-pix_fmt", "yuv420p",
		// Tag SDR Rec.709 colorspace so HDR-capable players render correctly.
		"-color_primaries", "bt709",
		"-color_trc", "bt709",
		"-colorspace", "bt709",
		"-color_range", "tv",
	}
	// -g and -keyint_min force a keyframe every N frames so segment
	// boundaries always land on keyframes — required for clean HLS
	// playback. Applied to all encoders.
	gop := fmt.Sprintf("%d", keyframeIntervalFrames)
	args = append(args,
		"-g", gop,
		"-keyint_min", gop,
		"-sc_threshold", "0", // disable scenecut keyframes (would break GOP alignment)
	)
	switch p.cfg.VideoEncoder {
	case "libx264":
		args = append(args,
			"-preset", "veryfast", "-tune", "zerolatency",
			"-profile:v", "high", "-level", "4.1",
			"-b:v", "6M", "-maxrate", "8M", "-bufsize", "12M",
		)
	case "h264_nvenc":
		// p4 is the realtime sweet spot — quality close to p5 but with
		// enough headroom that the GPU can keep encoding while also
		// servicing library transcodes. -tune ll (low-latency) drops
		// lookahead and B-frames so frames come out as fast as they go in.
		args = append(args,
			"-preset", "p4", "-tune", "ll", "-rc", "vbr",
			"-profile:v", "high", "-level", "4.1",
			"-b:v", "6M", "-maxrate", "8M", "-bufsize", "8M",
			// Force IDR at -g intervals (NVENC otherwise emits non-IDR I-frames).
			"-forced-idr", "1",
		)
	case "h264_amf":
		args = append(args,
			"-quality", "speed", "-rc", "vbr_peak",
			"-profile:v", "high",
			"-b:v", "6M", "-maxrate", "8M",
		)
	case "h264_qsv":
		args = append(args,
			"-preset", "veryfast", "-profile:v", "high",
			"-b:v", "6M", "-maxrate", "8M",
		)
	}
	args = append(args,
		"-c:a", p.cfg.AudioEncoder,
		"-ac", "2", // downmix surround → stereo (HLS+browsers expect ≤2ch AAC)
		"-b:a", "192k",
		"-bsf:a", "aac_adtstoasc",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", hlsSegmentDuration),
		"-hls_list_size", fmt.Sprintf("%d", hlsListSize),
		"-hls_flags", "delete_segments+omit_endlist+independent_segments",
		"-hls_segment_filename", filepath.Join(dir, "seg-%05d.ts"),
		// Prefix segment URLs in the playlist so the player's relative-URL
		// resolution from `/tv/channels/{id}/stream.m3u8` lands at our
		// `/segments/{name}` route. Without this, browsers request
		// `/tv/channels/{id}/seg-00000.ts` directly, miss the route, and
		// fall through to the SPA's index.html — segments "load" with 200
		// but contain HTML, so the player silently shows a black screen.
		"-hls_base_url", "segments/",
		filepath.Join(dir, "playlist.m3u8"),
	)
	cmd := exec.CommandContext(streamCtx, p.cfg.FFmpegBin, args...)
	cmd.Stdin = upstream
	// Capture ffmpeg stderr at warn level — on a healthy stream it's quiet,
	// on a failing one (codec mismatch, signal loss) it's the only signal.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		upstream.Close()
		os.RemoveAll(dir)
		return nil, fmt.Errorf("ffmpeg stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		upstream.Close()
		os.RemoveAll(dir)
		return nil, fmt.Errorf("ffmpeg start: %w", err)
	}

	// 64KB ring is plenty — ffmpeg's "command line / input format dump /
	// fatal error" footprint is rarely more than ~10KB even on weird inputs.
	stderrBuf := newRingBuffer(64 * 1024)
	go p.drainStderr(channelID, stderr, stderrBuf)
	// Reaper: when the process exits (either because we killed it via
	// cancel or because ffmpeg crashed), close the upstream and tear down
	// the session entry. Without this an upstream-side error would leak
	// the session map entry forever.
	go p.reaper(channelID, cmd, stderrBuf)

	s := &HLSSession{
		channelID: channelID,
		dir:       dir,
		refcount:  1,
		cmd:       cmd,
		upstream:  upstream,
		cancel:    cancel,
		stderrBuf: stderrBuf,
	}
	p.mu.Lock()
	p.sessions[channelID] = s
	p.mu.Unlock()
	return s, nil
}

// Release decrements the session's refcount. When it hits 0, a grace-
// period timer is started; if no viewer reattaches within the window,
// the session is torn down. Safe to call after the session has already
// been closed (e.g. from the reaper) — it's a no-op in that case.
func (p *HLSProxy) Release(s *HLSSession) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.refcount--
	if s.refcount > 0 {
		s.mu.Unlock()
		return
	}
	// Last viewer left. Schedule a tear-down after the grace period.
	s.closing = time.AfterFunc(hlsStreamLifetime, func() {
		p.teardown(s)
	})
	s.mu.Unlock()
}

// teardown kills ffmpeg, closes the upstream, removes the session
// directory, and drops the entry from the proxy's session map. Idempotent.
func (p *HLSProxy) teardown(s *HLSSession) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	// If a new viewer arrived between the timer firing and this lock
	// acquisition, refcount will be > 0; bail without tearing down.
	if s.refcount > 0 {
		s.closing = nil
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	s.cancel()
	s.upstream.Close()
	if err := os.RemoveAll(s.dir); err != nil {
		p.logger.WarnContext(context.Background(), "hls session dir cleanup",
			"channel_id", s.channelID, "dir", s.dir, "err", err)
	}
	p.mu.Lock()
	if existing, ok := p.sessions[s.channelID]; ok && existing == s {
		delete(p.sessions, s.channelID)
	}
	p.mu.Unlock()
}

// reaper waits for ffmpeg to exit and ensures the session is torn down.
// Only fires if ffmpeg crashes or is killed before normal teardown — the
// happy path teardown via Release/grace-period also calls cancel which
// reaches us. On crash, dumps the captured stderr ring so we can see why
// ffmpeg actually died (codec mismatches, missing PMT, signal loss, etc.).
func (p *HLSProxy) reaper(channelID uuid.UUID, cmd *exec.Cmd, stderr *ringBuffer) {
	err := cmd.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		p.logger.WarnContext(context.Background(), "hls ffmpeg exited",
			"channel_id", channelID, "err", err, "stderr", stderr.String())
	}
	// Force-teardown: even if there are still refs, the upstream is gone
	// so the session is dead. Reset refcount to 0 so teardown proceeds.
	p.mu.Lock()
	s, ok := p.sessions[channelID]
	p.mu.Unlock()
	if ok {
		s.mu.Lock()
		s.refcount = 0
		s.mu.Unlock()
		p.teardown(s)
	}
}

// drainStderr forwards ffmpeg stderr into the session's ring buffer (so
// crash diagnostics in the reaper have the last few KB) and also warn-
// logs lines that look like errors as they arrive — useful when ffmpeg
// is producing partial output but degraded (e.g. signal lock issues).
func (p *HLSProxy) drainStderr(channelID uuid.UUID, pipe io.ReadCloser, ring *ringBuffer) {
	buf := make([]byte, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			ring.Write(buf[:n])
			line := string(buf[:n])
			if containsAny(line, "Error", "error", "Failed", "failed") {
				p.logger.WarnContext(context.Background(), "ffmpeg stderr",
					"channel_id", channelID, "msg", line)
			}
		}
		if err != nil {
			return
		}
	}
}

// ringBuffer is a thread-safe fixed-size byte ring. Used to keep the
// most recent ffmpeg stderr around so the reaper can dump it on crash.
// Older bytes are silently dropped — crash diagnostics live in the tail.
type ringBuffer struct {
	mu   sync.Mutex
	buf  []byte
	full bool
	head int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, size)}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range p {
		r.buf[r.head] = b
		r.head++
		if r.head >= len(r.buf) {
			r.head = 0
			r.full = true
		}
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return string(r.buf[:r.head])
	}
	out := make([]byte, 0, len(r.buf))
	out = append(out, r.buf[r.head:]...)
	out = append(out, r.buf[:r.head]...)
	return string(out)
}

// Shutdown tears down every active session. Called on server stop so the
// session directories don't leak across restarts.
func (p *HLSProxy) Shutdown() {
	p.mu.Lock()
	sessions := make([]*HLSSession, 0, len(p.sessions))
	for _, s := range p.sessions {
		sessions = append(sessions, s)
	}
	p.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		s.refcount = 0
		if s.closing != nil {
			s.closing.Stop()
			s.closing = nil
		}
		s.mu.Unlock()
		p.teardown(s)
	}
}

// ActiveSessions returns the number of currently-live sessions. Useful
// for the admin dashboard and tests.
func (p *HLSProxy) ActiveSessions() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sessions)
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if len(n) > 0 && len(s) >= len(n) {
			for i := 0; i <= len(s)-len(n); i++ {
				if s[i:i+len(n)] == n {
					return true
				}
			}
		}
	}
	return false
}
