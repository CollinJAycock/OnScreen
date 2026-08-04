package v1

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/watchlimit"
	"github.com/onscreen/onscreen/internal/livetv"
)

// fakeWatchLimit is a canned ItemWatchLimit: a fixed policy + usage, counting
// AddTick calls.
type fakeWatchLimit struct {
	mu     sync.Mutex
	policy watchlimit.Policy
	used   int
	ticks  int
}

func (f *fakeWatchLimit) GetPolicy(context.Context, uuid.UUID) (watchlimit.Policy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.policy, nil
}

func (f *fakeWatchLimit) TodayUsageSeconds(context.Context, uuid.UUID, time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.used, nil
}

func (f *fakeWatchLimit) AddTick(context.Context, uuid.UUID, time.Time, time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ticks++
	return f.used, nil
}

func (f *fakeWatchLimit) tickCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ticks
}

// gateStreamProxy hands out one canned session whose dir the test controls.
type gateStreamProxy struct {
	session *livetv.HLSSession
}

func (s *gateStreamProxy) Acquire(context.Context, uuid.UUID) (*livetv.HLSSession, error) {
	return s.session, nil
}
func (s *gateStreamProxy) Lookup(uuid.UUID) (*livetv.HLSSession, bool) {
	return s.session, s.session != nil
}
func (s *gateStreamProxy) Release(*livetv.HLSSession) {}

// zeroLimit is an over-budget policy: daily limit 0 minutes.
func zeroLimit() watchlimit.Policy {
	zero := 0
	return watchlimit.Policy{DailyLimitMinutes: &zero}
}

func liveReq(t *testing.T, target string, channelID uuid.UUID) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", target, nil)
	req = withChiParam(req, "id", channelID.String())
	claims := &auth.Claims{
		UserID: uuid.New(), Username: "kid",
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	return req.WithContext(middleware.WithClaims(req.Context(), claims))
}

// liveSegReq builds a segment request carrying BOTH chi params (withChiParam
// replaces the route context wholesale, so calling it twice keeps only the
// second param).
func liveSegReq(t *testing.T, channelID uuid.UUID, name string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/tv/channels/x/segments/"+name, nil)
	req = withChiParams(req, "id", channelID.String(), "name", name)
	claims := &auth.Claims{
		UserID: uuid.New(), Username: "kid",
		IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}
	return req.WithContext(middleware.WithClaims(req.Context(), claims))
}

// liveHandlerWithSession builds a LiveTVHandler whose proxy serves from a real
// temp dir, so segment/playlist reads hit actual files.
func liveHandlerWithSession(t *testing.T, wl ItemWatchLimit) (*LiveTVHandler, string) {
	t.Helper()
	dir := t.TempDir()
	sess := livetv.NewSessionForTest(uuid.New(), dir)
	h := NewLiveTVHandler(stubLiveTVService{}, slog.Default()).WithStreamProxy(&gateStreamProxy{session: sess})
	if wl != nil {
		h = h.WithWatchLimit(wl)
	}
	return h, dir
}

// TestLiveTV_WatchLimitGates pins the parental gate on both live endpoints:
// live TV used to be entirely outside the watch-limit system — a capped
// profile just switched to the Live TV tab.
func TestLiveTV_WatchLimitGates(t *testing.T) {
	wl := &fakeWatchLimit{policy: zeroLimit(), used: 3600} // over budget
	h, dir := liveHandlerWithSession(t, wl)
	ch := uuid.New()

	// Playlist blocked.
	rec := httptest.NewRecorder()
	h.StreamPlaylist(rec, liveReq(t, "/api/v1/tv/channels/x/stream.m3u8", ch))
	if rec.Code != http.StatusForbidden {
		t.Errorf("playlist for an over-budget profile: status %d, want 403 PARENTAL_LIMIT", rec.Code)
	}

	// Segment blocked, even with the file on disk.
	if err := os.WriteFile(filepath.Join(dir, "seg-00000.ts"), []byte("TS"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	req := liveSegReq(t, ch, "seg-00000.ts")
	h.StreamSegment(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("segment for an over-budget profile: status %d, want 403", rec.Code)
	}
}

// TestLiveTV_SegmentFetchAccruesUsage pins the accounting half: live viewing
// must tick the daily budget. Before this, hours of live TV accrued zero.
func TestLiveTV_SegmentFetchAccruesUsage(t *testing.T) {
	limit := 120 // under budget: restricted but allowed
	wl := &fakeWatchLimit{policy: watchlimit.Policy{DailyLimitMinutes: &limit}}
	h, dir := liveHandlerWithSession(t, wl)
	ch := uuid.New()

	if err := os.WriteFile(filepath.Join(dir, "seg-00000.ts"), []byte("TS"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := liveSegReq(t, ch, "seg-00000.ts")
	h.StreamSegment(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("segment fetch failed: %d", rec.Code)
	}
	if wl.tickCount() == 0 {
		t.Error("segment fetch accrued no usage — live viewing is invisible to the daily budget")
	}
}

// TestRewriteLivePlaylistTokens pins the token rewrite: relative segment URIs
// drop the playlist's query on resolution, so a query-token client 401'd on
// every segment while the playlist itself worked.
func TestRewriteLivePlaylistTokens(t *testing.T) {
	playlist := "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:2.000,\nsegments/seg-00000.ts\n#EXTINF:2.000,\nsegments/seg-00001.ts\n"

	got := string(rewriteLivePlaylistTokens([]byte(playlist), "tok&x"))
	if !strings.Contains(got, "segments/seg-00000.ts?token=tok%26x") ||
		!strings.Contains(got, "segments/seg-00001.ts?token=tok%26x") {
		t.Errorf("segment URIs not tokenized (and escaped):\n%s", got)
	}
	if strings.Contains(got, "#EXTM3U?token") || strings.Contains(got, "#EXTINF:2.000,?token") {
		t.Errorf("directive lines must not be rewritten:\n%s", got)
	}

	// Cookie-authenticated requests carry no query token; the playlist must
	// pass through byte-identical.
	if string(rewriteLivePlaylistTokens([]byte(playlist), "")) != playlist {
		t.Error("tokenless playlist should be untouched")
	}
}

// stubLiveTVService satisfies LiveTVService for handler construction; the
// stream tests never reach it.
type stubLiveTVService struct{ LiveTVService }

// ── router-level: chi param helper reuse check ──────────────────────────────

var _ = chi.URLParam // keep the chi import anchored to its use in helpers
