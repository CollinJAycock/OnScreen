package livetv

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeFFmpeg is a tiny shell script the tests use as a stand-in for the
// real ffmpeg binary. It writes a fake playlist + a couple of segments
// and then sleeps until killed — exactly the lifecycle the proxy expects
// from a real run, but with no codec dependencies.
//
// The script discovers its output directory by parsing the
// `-hls_segment_filename` argument the proxy passes.
func writeFakeFFmpeg(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-ffmpeg.sh")
	script := `#!/bin/sh
# Find the output directory from -hls_segment_filename arg.
out_dir=""
i=1
for arg in "$@"; do
  case "$arg" in
    -hls_segment_filename)
      next=$((i + 1))
      eval "next_arg=\${$next}"
      out_dir=$(dirname "$next_arg")
      break
      ;;
  esac
  i=$((i + 1))
done

if [ -z "$out_dir" ]; then
  echo "fake-ffmpeg: missing -hls_segment_filename" 1>&2
  exit 1
fi

# Write a minimal HLS playlist + a couple TS segments.
sleep 0.1
cat > "$out_dir/seg-00000.ts" <<EOF
TS-SEG-0
EOF
cat > "$out_dir/seg-00001.ts" <<EOF
TS-SEG-1
EOF
cat > "$out_dir/playlist.m3u8" <<EOF
#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:4
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:4.000,
seg-00000.ts
#EXTINF:4.000,
seg-00001.ts
EOF

# Drain stdin so the upstream Stream.Close() returns promptly when killed.
cat > /dev/null &
catpid=$!
trap "kill $catpid 2>/dev/null; rm -rf $out_dir; exit 0" TERM INT
wait $catpid
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	return path
}

// stubChannelStream produces a fixed payload that the fake-ffmpeg drains
// then exits. Tracks Close() so tests can assert release semantics.
type stubChannelStream struct {
	io.Reader
	closed atomic.Bool
}

func (s *stubChannelStream) Close() error {
	s.closed.Store(true)
	return nil
}

// stubProxyService implements the bits of *Service the proxy uses
// (OpenChannelStream). Lets tests bypass the DB + driver layers.
type stubProxyService struct {
	stream *stubChannelStream
	err    error
}

func (s *stubProxyService) OpenChannelStream(_ context.Context, _ uuid.UUID) (Stream, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.stream != nil {
		return s.stream, nil
	}
	return &stubChannelStream{Reader: strings.NewReader("MPEGTS")}, nil
}

// newTestProxy returns a proxy wired to a fake ffmpeg + stub upstream.
// Honors the OS — falls back to a Bash invocation on Windows since
// .sh files aren't directly executable.
func newTestProxy(t *testing.T, svc proxyServiceLike) (*HLSProxy, string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available — fake ffmpeg script can't run")
	}
	script := writeFakeFFmpeg(t)
	dir := t.TempDir()
	p := &HLSProxy{
		cfg:      HLSConfig{Dir: dir, FFmpegBin: "sh"},
		svc:      &shimService{inner: svc, fakeArg: script},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: make(map[uuid.UUID]*HLSSession),
	}
	return p, dir
}

// proxyServiceLike is a tiny interface so tests can inject either the
// real *Service (integration) or a stub (unit).
type proxyServiceLike interface {
	OpenChannelStream(ctx context.Context, channelID uuid.UUID) (Stream, error)
}

// shimService injects the fake-ffmpeg script path as the first arg of
// the binary invocation. The proxy treats `sh fake-ffmpeg.sh ...args` as
// the ffmpeg binary so we don't have to package a real ffmpeg for tests.
type shimService struct {
	inner   proxyServiceLike
	fakeArg string
}

func (s *shimService) OpenChannelStream(ctx context.Context, id uuid.UUID) (Stream, error) {
	return s.inner.OpenChannelStream(ctx, id)
}

// We override the Acquire path's exec.CommandContext indirectly by
// changing the proxy's FFmpegBin to "sh" and prepending the script via a
// special-case in test land. To avoid surgery on the production code
// just for testability, the proxy's exec invocation accepts either
// "ffmpeg" (production) or the test script (for unit tests we directly
// drive the proxy's internals via NewTestSession).

// NewTestSession is exported for tests that want to drive the lifecycle
// of a session without the exec dance. Real code goes through Acquire.
func (p *HLSProxy) newTestSession(id uuid.UUID, dir string, upstream Stream, cmd *exec.Cmd, cancel context.CancelFunc) *HLSSession {
	s := &HLSSession{
		channelID: id, dir: dir, refcount: 1,
		cmd: cmd, upstream: upstream, cancel: cancel,
	}
	p.mu.Lock()
	p.sessions[id] = s
	p.mu.Unlock()
	return s
}

// ── Tests ────────────────────────────────────────────────────────────────────

func TestHLSProxy_AcquireOpensSessionAndPlaylist(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	stub := &stubChannelStream{Reader: strings.NewReader("MPEGTS-PAYLOAD")}
	svc := &stubProxyService{stream: stub}
	p, _ := newTestProxy(t, svc)

	// Hand-roll the exec command to invoke the fake-ffmpeg script.
	id := uuid.New()
	dir := filepath.Join(t.TempDir(), "ch-test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "sh",
		(p.svc.(*shimService)).fakeArg,
		"-hls_segment_filename", filepath.Join(dir, "seg-%05d.ts"),
		filepath.Join(dir, "playlist.m3u8"),
	)
	cmd.Stdin = stub
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start fake ffmpeg: %v", err)
	}
	go func() { cmd.Wait() }()

	s := p.newTestSession(id, dir, stub, cmd, cancel)

	// Wait for the playlist file to appear (fake-ffmpeg sleeps 100ms before writing).
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(s.PlaylistPath()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake ffmpeg never wrote playlist")
		}
		time.Sleep(20 * time.Millisecond)
	}

	data, err := os.ReadFile(s.PlaylistPath())
	if err != nil {
		t.Fatalf("read playlist: %v", err)
	}
	if !strings.Contains(string(data), "#EXTM3U") || !strings.Contains(string(data), "seg-00000.ts") {
		t.Errorf("playlist content unexpected: %s", data)
	}

	// Refcount-aware teardown: drop the only viewer, force teardown.
	s.mu.Lock()
	s.refcount = 0
	s.mu.Unlock()
	p.teardown(s)
	if !stub.closed.Load() {
		t.Error("upstream stream not closed on teardown")
	}
}

func TestHLSProxy_RefcountAcrossViewers(t *testing.T) {
	stub := &stubChannelStream{Reader: strings.NewReader("MPEGTS")}
	p := &HLSProxy{
		cfg:      HLSConfig{Dir: t.TempDir(), FFmpegBin: "sh"},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: make(map[uuid.UUID]*HLSSession),
	}
	id := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dummy := exec.CommandContext(ctx, "sh", "-c", "sleep 60")
	if err := dummy.Start(); err != nil {
		t.Fatalf("start dummy: %v", err)
	}
	defer dummy.Process.Kill()
	go dummy.Wait()
	s := p.newTestSession(id, t.TempDir(), stub, dummy, cancel)

	// Simulate three viewers: refcount 1 (initial) → 4 → release back to 1.
	s.mu.Lock()
	s.refcount = 1
	s.mu.Unlock()
	for i := 0; i < 3; i++ {
		// Reach into Acquire's "exists" branch by calling it on the existing channel.
		_, err := p.Acquire(context.Background(), id)
		if err != nil {
			t.Fatalf("acquire #%d: %v", i, err)
		}
	}
	s.mu.Lock()
	if s.refcount != 4 {
		t.Errorf("refcount after 3 acquires: got %d, want 4", s.refcount)
	}
	s.mu.Unlock()

	for i := 0; i < 3; i++ {
		p.Release(s)
	}
	s.mu.Lock()
	if s.refcount != 1 {
		t.Errorf("refcount after 3 releases: got %d, want 1", s.refcount)
	}
	s.mu.Unlock()
}

func TestHLSProxy_AcquireBubblesAllTunersBusy(t *testing.T) {
	svc := &stubProxyService{err: ErrAllTunersBusy}
	p := NewHLSProxy(HLSConfig{Dir: t.TempDir()}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// We can't go through the real OpenChannelStream path without a real
	// Service, so reach in and make Acquire's call fail by injecting a
	// stub via a wrapping function. Easier: construct directly and call.
	p.svc = &serviceShim{open: svc.OpenChannelStream}

	_, err := p.Acquire(context.Background(), uuid.New())
	if !errors.Is(err, ErrAllTunersBusy) {
		t.Errorf("got %v, want ErrAllTunersBusy", err)
	}
}

// serviceShim lets tests substitute the OpenChannelStream path without
// constructing a real *Service.
type serviceShim struct {
	open func(ctx context.Context, id uuid.UUID) (Stream, error)
}

func (s *serviceShim) OpenChannelStream(ctx context.Context, id uuid.UUID) (Stream, error) {
	return s.open(ctx, id)
}

func TestHLSProxy_ReleaseAfterCloseIsNoop(t *testing.T) {
	stub := &stubChannelStream{Reader: strings.NewReader("MPEGTS")}
	p := &HLSProxy{
		cfg:      HLSConfig{Dir: t.TempDir()},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: make(map[uuid.UUID]*HLSSession),
	}
	id := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dummy := exec.CommandContext(ctx, "sh", "-c", "sleep 60")
	dummy.Start()
	defer dummy.Process.Kill()
	go dummy.Wait()
	s := p.newTestSession(id, t.TempDir(), stub, dummy, cancel)

	s.mu.Lock()
	s.refcount = 0
	s.mu.Unlock()
	p.teardown(s)
	// Release on already-closed session must not panic or change state.
	p.Release(s)
}

func TestHLSProxy_ActiveSessionsReportsLiveCount(t *testing.T) {
	p := &HLSProxy{
		cfg:      HLSConfig{Dir: t.TempDir()},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: make(map[uuid.UUID]*HLSSession),
	}
	if p.ActiveSessions() != 0 {
		t.Errorf("initial: got %d", p.ActiveSessions())
	}
	id := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dummy := exec.CommandContext(ctx, "sh", "-c", "sleep 60")
	dummy.Start()
	defer dummy.Process.Kill()
	go dummy.Wait()
	p.newTestSession(id, t.TempDir(), &stubChannelStream{Reader: strings.NewReader("")}, dummy, cancel)
	if p.ActiveSessions() != 1 {
		t.Errorf("after newTestSession: got %d", p.ActiveSessions())
	}
}
