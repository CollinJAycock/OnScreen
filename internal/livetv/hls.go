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
// 4s is a reasonable balance: shorter = lower channel-change latency,
// longer = fewer files on disk and lower request overhead.
const hlsSegmentDuration = 4

// hlsListSize is the number of segments to keep in the playlist (and on
// disk, with delete_segments). 6 × 4s = 24s of seekable buffer, enough
// to absorb a brief network blip without going off the back of the
// playlist.
const hlsListSize = 6

// HLSConfig configures the proxy. Dir is where per-session subdirectories
// are created — must be writable by the server process. FFmpegBin is the
// ffmpeg binary path (defaults to "ffmpeg" if empty, picked up from PATH).
type HLSConfig struct {
	Dir       string
	FFmpegBin string
}

// HLSSession represents one active per-channel session. The first viewer
// for a channel creates it; subsequent viewers increment refcount and
// share the same on-disk playlist + segments.
type HLSSession struct {
	channelID uuid.UUID
	dir       string

	mu       sync.Mutex
	refcount int
	closing  *time.Timer // grace-period timer scheduled when refcount hits 0
	cmd      *exec.Cmd
	upstream Stream
	cancel   context.CancelFunc
	closed   bool
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

	// Create on a separate code path to avoid holding the proxy mutex
	// across an upstream tune (which can block for ~10s on first lock).
	upstream, err := p.svc.OpenChannelStream(ctx, channelID)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(p.cfg.Dir, 0o755); err != nil {
		upstream.Close()
		return nil, fmt.Errorf("hls dir: %w", err)
	}
	dir, err := os.MkdirTemp(p.cfg.Dir, "ch-*")
	if err != nil {
		upstream.Close()
		return nil, fmt.Errorf("hls session dir: %w", err)
	}

	// ffmpeg lifecycle is tied to a context separate from the request — a
	// disconnecting client must not kill the stream while other viewers
	// are still attached.
	streamCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(streamCtx, p.cfg.FFmpegBin,
		"-fflags", "+genpts+discardcorrupt",
		"-i", "pipe:0",
		"-map", "0",
		// Stream-copy keeps CPU near zero. Broadcast TS is already
		// H.264 + AAC for everything but ATSC 3.0 (deferred to Phase D).
		"-c", "copy",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", hlsSegmentDuration),
		"-hls_list_size", fmt.Sprintf("%d", hlsListSize),
		"-hls_flags", "delete_segments+omit_endlist+independent_segments",
		"-hls_segment_filename", filepath.Join(dir, "seg-%05d.ts"),
		filepath.Join(dir, "playlist.m3u8"),
	)
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

	// Drain stderr into the logger so the pipe doesn't block ffmpeg.
	go p.drainStderr(channelID, stderr)
	// Reaper: when the process exits (either because we killed it via
	// cancel or because ffmpeg crashed), close the upstream and tear down
	// the session entry. Without this an upstream-side error would leak
	// the session map entry forever.
	go p.reaper(channelID, cmd)

	s := &HLSSession{
		channelID: channelID,
		dir:       dir,
		refcount:  1,
		cmd:       cmd,
		upstream:  upstream,
		cancel:    cancel,
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
// reaches us.
func (p *HLSProxy) reaper(channelID uuid.UUID, cmd *exec.Cmd) {
	err := cmd.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		p.logger.WarnContext(context.Background(), "hls ffmpeg exited",
			"channel_id", channelID, "err", err)
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

func (p *HLSProxy) drainStderr(channelID uuid.UUID, pipe io.ReadCloser) {
	buf := make([]byte, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			// ffmpeg is verbose; only log lines that look like errors.
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
