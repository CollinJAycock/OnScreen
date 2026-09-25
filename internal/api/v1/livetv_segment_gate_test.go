package v1

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/livetv"
)

// countingChannelSvc counts GetChannel calls so the segment gate's memo can
// be observed.
type countingChannelSvc struct {
	*mockLiveTVService
	gets atomic.Int32
}

func (c *countingChannelSvc) GetChannel(ctx context.Context, id uuid.UUID) (livetv.Channel, error) {
	c.gets.Add(1)
	return c.mockLiveTVService.GetChannel(ctx, id)
}

// segmentGateFixture registers a channel with the given enabled flag and a
// RUNNING session serving seg-00000.ts — the state an admin previewing a
// disabled channel leaves behind.
func segmentGateFixture(t *testing.T, enabled bool) (*LiveTVHandler, *countingChannelSvc, *stubStreamProxy, uuid.UUID) {
	t.Helper()
	svc := &countingChannelSvc{mockLiveTVService: newMockLiveTVService()}
	proxy := newStubStreamProxy(t)
	proxy.segments = map[string][]byte{"seg-00000.ts": []byte("TS-PAYLOAD-0")}
	h := NewLiveTVHandler(svc, slog.Default()).WithStreamProxy(proxy)
	id := uuid.New()
	svc.channels[id] = livetv.Channel{Enabled: enabled}
	if _, err := proxy.Acquire(context.Background(), id); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return h, svc, proxy, id
}

func segmentRequest(id uuid.UUID, claims *auth.Claims) *http.Request {
	req := httptest.NewRequest("GET", "/api/v1/tv/channels/"+id.String()+"/segments/seg-00000.ts", nil)
	req = withChiParams(req, "id", id.String(), "name", "seg-00000.ts")
	if claims != nil {
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	}
	return req
}

// StreamPlaylist 404s a disabled channel for non-admins, but StreamSegment
// only looked the session up — so while any session for the channel ran, a
// non-admin could pull its (sequentially named) segments directly.
func TestLiveTVStream_Segment_DisabledChannelIs404ForNonAdmin(t *testing.T) {
	h, _, proxy, id := segmentGateFixture(t, false)

	rec := httptest.NewRecorder()
	h.StreamSegment(rec, segmentRequest(id, &auth.Claims{UserID: uuid.New()}))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-admin segment of a disabled channel: got %d, want 404", rec.Code)
	}
	if proxy.lookups.Load() != 0 {
		t.Error("the disabled-channel check must run before the session is touched")
	}

	// An admin previewing the disabled channel still gets its segments.
	rec = httptest.NewRecorder()
	h.StreamSegment(rec, segmentRequest(id, &auth.Claims{UserID: uuid.New(), IsAdmin: true}))
	if rec.Code != http.StatusOK || rec.Body.String() != "TS-PAYLOAD-0" {
		t.Errorf("admin segment of a disabled channel: got %d %q, want 200", rec.Code, rec.Body.String())
	}
}

func TestLiveTVStream_Segment_UnknownChannelIs404ForNonAdmin(t *testing.T) {
	h, svc, _, id := segmentGateFixture(t, true)
	svc.mu.Lock()
	delete(svc.channels, id) // session still running, channel row gone
	svc.mu.Unlock()

	rec := httptest.NewRecorder()
	h.StreamSegment(rec, segmentRequest(id, &auth.Claims{UserID: uuid.New()}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("non-admin segment of an unknown channel: got %d, want 404", rec.Code)
	}
}

// The gate runs on every segment of every viewer, so it is memoized — but a
// disable must still bite once the memo expires.
func TestLiveTVStream_Segment_EnabledCheckIsMemoizedButExpires(t *testing.T) {
	h, svc, _, id := segmentGateFixture(t, true)
	viewer := &auth.Claims{UserID: uuid.New()}

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.StreamSegment(rec, segmentRequest(id, viewer))
		if rec.Code != http.StatusOK {
			t.Fatalf("segment %d of an enabled channel: got %d, want 200", i, rec.Code)
		}
	}
	if got := svc.gets.Load(); got != 1 {
		t.Errorf("GetChannel called %d times for 3 segments, want 1 (memoized)", got)
	}

	// Admin disables the channel; once the memo lapses the viewer is cut off.
	if err := svc.SetChannelEnabled(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	h.chanEnabled.mu.Lock()
	e := h.chanEnabled.entries[id]
	e.expires = time.Now().Add(-time.Second)
	h.chanEnabled.entries[id] = e
	h.chanEnabled.mu.Unlock()

	rec := httptest.NewRecorder()
	h.StreamSegment(rec, segmentRequest(id, viewer))
	if rec.Code != http.StatusNotFound {
		t.Errorf("segment after the channel was disabled: got %d, want 404", rec.Code)
	}
}
