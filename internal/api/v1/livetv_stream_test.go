package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/livetv"
)

// stubStreamProxy implements LiveTVStreamProxy backed by a tempdir. The
// session is hand-built — we don't need to spin ffmpeg up for the
// handler tests.
type stubStreamProxy struct {
	mu         sync.Mutex
	sessions   map[uuid.UUID]*livetv.HLSSession
	dir        string
	playlist   []byte // if non-nil, written to playlist.m3u8 on Acquire
	segments   map[string][]byte
	acquireErr error
	acquires   atomic.Int32
	releases   atomic.Int32
}

func newStubStreamProxy(t *testing.T) *stubStreamProxy {
	return &stubStreamProxy{
		sessions: make(map[uuid.UUID]*livetv.HLSSession),
		dir:      t.TempDir(),
		segments: make(map[string][]byte),
	}
}

// Acquire is intentionally simplified vs the real implementation: it
// always creates a new session backed by a fresh subdir, writes seeded
// playlist + segments, and returns. The real proxy's refcount logic is
// unit-tested in the livetv package — here we just need the handler to
// see a valid session.
func (s *stubStreamProxy) Acquire(_ context.Context, id uuid.UUID) (*livetv.HLSSession, error) {
	s.acquires.Add(1)
	if s.acquireErr != nil {
		return nil, s.acquireErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.sessions[id]; ok {
		return existing, nil
	}
	subdir := filepath.Join(s.dir, id.String())
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		return nil, err
	}
	if s.playlist != nil {
		os.WriteFile(filepath.Join(subdir, "playlist.m3u8"), s.playlist, 0o644)
	}
	for name, body := range s.segments {
		os.WriteFile(filepath.Join(subdir, name), body, 0o644)
	}
	// Use the test hook the livetv package exposes via its newTestSession
	// helper — but we can't reach it from another package. Instead, build
	// a session via the production constructor: no constructor exists for
	// callers, so we use a nil-safe approach: the handler calls
	// session.PlaylistPath() which is a pure path join — we can fake the
	// session by reaching into the package's exported fields. Since
	// HLSSession's fields are unexported, the only safe option is to
	// invoke through the proxy directly. So this stub proxy returns a
	// session built by an internal helper exposed for tests.
	sess := livetv.NewSessionForTest(id, subdir)
	s.sessions[id] = sess
	return sess, nil
}

func (s *stubStreamProxy) Release(_ *livetv.HLSSession) {
	s.releases.Add(1)
}

func TestLiveTVStream_Playlist_NoProxyIs503(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default()) // no .WithStreamProxy
	req := httptest.NewRequest("GET", "/api/v1/tv/channels/"+uuid.New().String()+"/stream.m3u8", nil)
	req = withChiParam(req, "id", uuid.New().String())
	rec := httptest.NewRecorder()
	h.StreamPlaylist(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", rec.Code)
	}
}

func TestLiveTVStream_Playlist_InvalidIDIs400(t *testing.T) {
	svc := newMockLiveTVService()
	proxy := newStubStreamProxy(t)
	h := NewLiveTVHandler(svc, slog.Default()).WithStreamProxy(proxy)
	req := httptest.NewRequest("GET", "/api/v1/tv/channels/not-a-uuid/stream.m3u8", nil)
	req = withChiParam(req, "id", "not-a-uuid")
	rec := httptest.NewRecorder()
	h.StreamPlaylist(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestLiveTVStream_Playlist_AllTunersBusy(t *testing.T) {
	svc := newMockLiveTVService()
	proxy := newStubStreamProxy(t)
	proxy.acquireErr = livetv.ErrAllTunersBusy
	h := NewLiveTVHandler(svc, slog.Default()).WithStreamProxy(proxy)
	req := httptest.NewRequest("GET", "/api/v1/tv/channels/"+uuid.New().String()+"/stream.m3u8", nil)
	req = withChiParam(req, "id", uuid.New().String())
	rec := httptest.NewRecorder()
	h.StreamPlaylist(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ALL_TUNERS_BUSY") {
		t.Errorf("body should carry stable error code; got %s", rec.Body.String())
	}
}

func TestLiveTVStream_Playlist_ChannelNotFoundIs404(t *testing.T) {
	svc := newMockLiveTVService()
	proxy := newStubStreamProxy(t)
	proxy.acquireErr = livetv.ErrNotFound
	h := NewLiveTVHandler(svc, slog.Default()).WithStreamProxy(proxy)
	req := httptest.NewRequest("GET", "/api/v1/tv/channels/"+uuid.New().String()+"/stream.m3u8", nil)
	req = withChiParam(req, "id", uuid.New().String())
	rec := httptest.NewRecorder()
	h.StreamPlaylist(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestLiveTVStream_Playlist_OtherErrorIs500(t *testing.T) {
	svc := newMockLiveTVService()
	proxy := newStubStreamProxy(t)
	proxy.acquireErr = errors.New("disk full")
	h := NewLiveTVHandler(svc, slog.Default()).WithStreamProxy(proxy)
	req := httptest.NewRequest("GET", "/api/v1/tv/channels/"+uuid.New().String()+"/stream.m3u8", nil)
	req = withChiParam(req, "id", uuid.New().String())
	rec := httptest.NewRecorder()
	h.StreamPlaylist(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rec.Code)
	}
}

func TestLiveTVStream_Playlist_ServesM3U8(t *testing.T) {
	svc := newMockLiveTVService()
	proxy := newStubStreamProxy(t)
	proxy.playlist = []byte("#EXTM3U\n#EXT-X-VERSION:3\nseg-00000.ts\n")
	h := NewLiveTVHandler(svc, slog.Default()).WithStreamProxy(proxy)

	id := uuid.New()
	req := httptest.NewRequest("GET", "/api/v1/tv/channels/"+id.String()+"/stream.m3u8", nil)
	req = withChiParam(req, "id", id.String())
	rec := httptest.NewRecorder()
	h.StreamPlaylist(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type: got %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "#EXTM3U") {
		t.Errorf("body unexpected: %s", rec.Body.String())
	}
	// Each playlist GET must Acquire and Release exactly once for refcount
	// correctness — that's the lifecycle anchor.
	if proxy.acquires.Load() != 1 || proxy.releases.Load() != 1 {
		t.Errorf("acquire/release: got %d/%d, want 1/1",
			proxy.acquires.Load(), proxy.releases.Load())
	}
}

func TestLiveTVStream_Segment_RejectsTraversal(t *testing.T) {
	svc := newMockLiveTVService()
	proxy := newStubStreamProxy(t)
	h := NewLiveTVHandler(svc, slog.Default()).WithStreamProxy(proxy)

	id := uuid.New()
	for _, name := range []string{"../etc/passwd", "/etc/passwd", "seg-../foo.ts", "playlist.m3u8"} {
		req := httptest.NewRequest("GET",
			"/api/v1/tv/channels/"+id.String()+"/segments/"+name, nil)
		req = withChiParams(req, "id", id.String(), "name", name)
		rec := httptest.NewRecorder()
		h.StreamSegment(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("traversal attempt %q should 404; got %d", name, rec.Code)
		}
	}
}

func TestLiveTVStream_Segment_ServesTSContent(t *testing.T) {
	svc := newMockLiveTVService()
	proxy := newStubStreamProxy(t)
	proxy.segments = map[string][]byte{
		"seg-00000.ts": []byte("TS-PAYLOAD-0"),
	}
	h := NewLiveTVHandler(svc, slog.Default()).WithStreamProxy(proxy)

	id := uuid.New()
	req := httptest.NewRequest("GET",
		"/api/v1/tv/channels/"+id.String()+"/segments/seg-00000.ts", nil)
	req = withChiParams(req, "id", id.String(), "name", "seg-00000.ts")
	rec := httptest.NewRecorder()
	h.StreamSegment(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp2t" {
		t.Errorf("Content-Type: got %q", ct)
	}
	if rec.Body.String() != "TS-PAYLOAD-0" {
		t.Errorf("body: got %q", rec.Body.String())
	}
}

func TestLiveTVStream_Segment_MissingFileIs404(t *testing.T) {
	svc := newMockLiveTVService()
	proxy := newStubStreamProxy(t)
	h := NewLiveTVHandler(svc, slog.Default()).WithStreamProxy(proxy)

	id := uuid.New()
	req := httptest.NewRequest("GET",
		"/api/v1/tv/channels/"+id.String()+"/segments/seg-99999.ts", nil)
	req = withChiParams(req, "id", id.String(), "name", "seg-99999.ts")
	rec := httptest.NewRecorder()
	h.StreamSegment(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing segment should 404; got %d", rec.Code)
	}
}

func TestLiveTVStream_Segment_NoProxyIs503(t *testing.T) {
	svc := newMockLiveTVService()
	h := NewLiveTVHandler(svc, slog.Default())
	req := httptest.NewRequest("GET",
		"/api/v1/tv/channels/"+uuid.New().String()+"/segments/seg-00000.ts", nil)
	req = withChiParams(req, "id", uuid.New().String(), "name", "seg-00000.ts")
	rec := httptest.NewRecorder()
	h.StreamSegment(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", rec.Code)
	}
}
