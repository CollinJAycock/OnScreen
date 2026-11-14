package v1

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
	"github.com/onscreen/onscreen/internal/valkey"
)

// valkeyTrips counts the Valkey round trips a client makes: one per command
// sent on its own, one per pipeline or MULTI/EXEC block. (The rate-limit
// middleware in front of these routes adds one more per request.)
type valkeyTrips struct{ n atomic.Int64 }

func (c *valkeyTrips) DialHook(next redis.DialHook) redis.DialHook { return next }
func (c *valkeyTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error { c.n.Add(1); return next(ctx, cmd) }
}
func (c *valkeyTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error { c.n.Add(1); return next(ctx, cmds) }
}

func (c *valkeyTrips) during(fn func()) int64 {
	before := c.n.Load()
	fn()
	return c.n.Load() - before
}

// fakeSegmentWorker is a worker segment server: every segNNNNN file up to
// head exists in every directory, and it counts the calls the API makes.
type fakeSegmentWorker struct {
	addr string
	head atomic.Int64

	mu    sync.Mutex
	calls map[string]int // "GET seg", "HEAD seg", "seghead"
}

func newFakeSegmentWorker(t *testing.T, head int) *fakeSegmentWorker {
	t.Helper()
	fw := &fakeSegmentWorker{calls: map[string]int{}}
	fw.head.Store(int64(head))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/seghead/"):
			fw.count("seghead")
			_, _ = io.WriteString(w, itoa(int(fw.head.Load())))
		case strings.HasPrefix(r.URL.Path, "/segments/"):
			fw.count(r.Method + " seg")
			_, _ = io.WriteString(w, "SEGMENT")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	fw.addr = u.Host
	return fw
}

func (fw *fakeSegmentWorker) count(k string) {
	fw.mu.Lock()
	fw.calls[k]++
	fw.mu.Unlock()
}

func (fw *fakeSegmentWorker) snapshot() map[string]int {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	out := make(map[string]int, len(fw.calls))
	for k, v := range fw.calls {
		out[k] = v
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

func segmentTestHandler(v *valkey.Client) (*NativeTranscodeHandler, *transcode.SessionStore, *transcode.SegmentTokenManager) {
	store := transcode.NewSessionStore(v)
	segTok := transcode.NewSegmentTokenManager(v)
	h := NewNativeTranscodeHandler(store, segTok, &mockTranscodeMedia{}, &config.Config{}, slog.Default())
	return h, store, segTok
}

func segmentTestServer(t *testing.T, h *NativeTranscodeHandler) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/sessions/{sid}/playlist.m3u8", h.Playlist)
	r.Get("/sessions/{sid}/seg/{name}", h.Segment)
	r.Get("/sessions/{sid}/abr/{rung}/seg/{name}", h.ABRVariantSegment)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func fetchStatus(t *testing.T, u string) int {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// A plain segment fetch used to cost eight Valkey round trips: the rate limit,
// a token GET plus two EXPIREs, a session GET, and a WATCH / GET / MULTI-SET-
// EXEC rewrite of the whole session to stamp activity. Now the token
// check, both TTL refreshes and the session read are one pipelined trip, and
// the stamp is written at most every few seconds per session, by whichever
// instance sees it due, in one conditional write.
func TestSegment_ValkeyRoundTrips(t *testing.T) {
	v := testvalkey.New(t)
	rt := &valkeyTrips{}
	v.Raw().AddHook(rt)
	worker := newFakeSegmentWorker(t, 100)
	ctx := context.Background()

	hA, store, segTok := segmentTestHandler(v)
	user, sid := uuid.New(), uuid.NewString()
	if err := store.Create(ctx, transcode.Session{
		ID: sid, UserID: user, WorkerAddr: worker.addr,
		CreatedAt: time.Now().Add(-time.Minute), LastActivityAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	tok, err := segTok.Issue(ctx, sid, user)
	if err != nil {
		t.Fatal(err)
	}
	srvA := segmentTestServer(t, hA)
	seg := func(srv *httptest.Server, i int) string {
		return srv.URL + "/sessions/" + sid + "/seg/" + fmt.Sprintf("seg%05d.ts", i) + "?token=" + url.QueryEscape(tok)
	}

	// The first fetch loads the scripts and makes the (stale) stamp.
	if code := fetchStatus(t, seg(srvA, 0)); code != http.StatusOK {
		t.Fatalf("first segment: %d", code)
	}
	if got, _ := store.Get(ctx, sid); time.Since(got.LastActivityAt) > 5*time.Second {
		t.Fatalf("a due stamp was not written: LastActivityAt = %v", got.LastActivityAt)
	}

	// Steady state on the same instance: the token check, and nothing else.
	if n := rt.during(func() {
		if code := fetchStatus(t, seg(srvA, 1)); code != http.StatusOK {
			t.Errorf("segment: %d", code)
		}
	}); n != 1 {
		t.Errorf("steady-state segment: %d Valkey round trips, want 1", n)
	}

	// Another API instance sees the first one's fresh stamp in the session it
	// read and doesn't write either.
	hB, _, _ := segmentTestHandler(v)
	srvB := segmentTestServer(t, hB)
	if n := rt.during(func() { _ = fetchStatus(t, seg(srvB, 2)) }); n != 1 {
		t.Errorf("segment on a second instance: %d round trips, want 1", n)
	}

	// Once the stamp is stale, the fetch writes it in one more trip.
	stale := `{"id":"` + sid + `","user_id":"` + user.String() + `","media_item_id":"` + uuid.Nil.String() +
		`","file_id":"` + uuid.Nil.String() + `","worker_id":"","worker_addr":"` + worker.addr +
		`","decision":"","file_path":"","position_ms":0,"created_at":"2026-01-01T00:00:00Z","last_activity_at":"2026-01-01T00:00:00Z"}`
	if err := v.Raw().Set(ctx, "transcode:session:"+sid, stale, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	hC, storeC, _ := segmentTestHandler(v)
	srvC := segmentTestServer(t, hC)
	if n := rt.during(func() { _ = fetchStatus(t, seg(srvC, 3)) }); n != 2 {
		t.Errorf("segment with a due stamp: %d round trips, want 2", n)
	}
	if got, _ := storeC.Get(ctx, sid); time.Since(got.LastActivityAt) > 5*time.Second {
		t.Errorf("due stamp not written: %v", got.LastActivityAt)
	}

	// Nothing is cached across a revoke: the next fetch is refused.
	if err := segTok.Revoke(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if code := fetchStatus(t, seg(srvA, 4)); code != http.StatusUnauthorized {
		t.Errorf("segment after revoke: %d, want 401", code)
	}
}

// An ABR segment cost seventeen round trips and three worker calls
// (/seghead, which lists the rung's whole directory, then a HEAD and the
// GET). In the steady state — a running child that covers the request, the
// rung unchanged, stamps fresh — it is now the one authorization trip, and
// the head this instance already saw past the request stands in for /seghead.
func TestABRVariantSegment_ValkeyRoundTrips(t *testing.T) {
	v := testvalkey.New(t)
	rt := &valkeyTrips{}
	v.Raw().AddHook(rt)
	worker := newFakeSegmentWorker(t, 40)
	ctx := context.Background()

	h, store, segTok := segmentTestHandler(v)
	parent, rung := abrTestParent(t, store)
	childID := abrChildID(parent.ID, rung.Label)
	if err := store.Create(ctx, transcode.Session{
		ID: childID, ParentID: parent.ID, UserID: parent.UserID, MediaItemID: parent.MediaItemID,
		WorkerAddr: worker.addr, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	tok, err := segTok.Issue(ctx, parent.ID, parent.UserID)
	if err != nil {
		t.Fatal(err)
	}
	srv := segmentTestServer(t, h)
	seg := func(i int) string {
		return srv.URL + "/sessions/" + parent.ID + "/abr/" + rung.Label + "/seg/" + itoa(i) + ".ts?token=" + url.QueryEscape(tok)
	}

	// The first fetch records the rung, stamps both sessions and reads the head.
	if code := fetchStatus(t, seg(0)); code != http.StatusOK {
		t.Fatalf("first ABR segment: %d", code)
	}
	if p, _ := store.Get(ctx, parent.ID); p.SelectedRendition != rung.Label {
		t.Errorf("SelectedRendition = %q, want %q", p.SelectedRendition, rung.Label)
	}

	before := worker.snapshot()
	if n := rt.during(func() {
		for i := 1; i <= 3; i++ {
			if code := fetchStatus(t, seg(i)); code != http.StatusOK {
				t.Errorf("segment %d: %d", i, code)
			}
		}
	}); n != 3 {
		t.Errorf("3 steady-state ABR segments: %d Valkey round trips, want 3", n)
	}
	after := worker.snapshot()
	if d := after["seghead"] - before["seghead"]; d != 0 {
		t.Errorf("%d /seghead calls for segments below the known head, want 0", d)
	}
	if d := after["HEAD seg"] - before["HEAD seg"]; d != 3 {
		t.Errorf("%d readiness probes, want one per segment", d)
	}
	if d := after["GET seg"] - before["GET seg"]; d != 3 {
		t.Errorf("%d segment GETs, want one per segment", d)
	}

	// Past the remembered head the worker is asked again (and the head moves).
	worker.head.Store(80)
	if code := fetchStatus(t, seg(50)); code != http.StatusOK {
		t.Fatalf("segment past the remembered head: %d", code)
	}
	if d := worker.snapshot()["seghead"] - after["seghead"]; d != 1 {
		t.Errorf("%d /seghead calls past the remembered head, want 1", d)
	}
	if n := countJobs(t, store); n != 0 {
		t.Errorf("%d rung jobs dispatched; the running child covered every request", n)
	}
}

// A playlist re-poll (hls.js reloads a live playlist every segment) read the
// session three times besides the token check, and stamped it.
func TestPlaylist_RepollValkeyRoundTrips(t *testing.T) {
	v := testvalkey.New(t)
	rt := &valkeyTrips{}
	v.Raw().AddHook(rt)
	ctx := context.Background()

	// A worker that also serves the session's index.m3u8.
	var gets atomic.Int32
	wsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "index.m3u8") {
			gets.Add(1)
			_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:4.000,\nseg00000.ts\n#EXTINF:4.000,\nseg00001.ts\n")
			return
		}
		_, _ = io.WriteString(w, "SEGMENT")
	}))
	t.Cleanup(wsrv.Close)
	wu, _ := url.Parse(wsrv.URL)

	h, store, segTok := segmentTestHandler(v)
	user, sid := uuid.New(), uuid.NewString()
	if err := store.Create(ctx, transcode.Session{ID: sid, UserID: user, WorkerAddr: wu.Host, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	tok, _ := segTok.Issue(ctx, sid, user)
	srv := segmentTestServer(t, h)
	pl := srv.URL + "/sessions/" + sid + "/playlist.m3u8?token=" + url.QueryEscape(tok)

	if code := fetchStatus(t, pl); code != http.StatusOK {
		t.Fatalf("first playlist: %d", code)
	}
	if n := rt.during(func() {
		if code := fetchStatus(t, pl); code != http.StatusOK {
			t.Errorf("playlist re-poll: %d", code)
		}
	}); n != 1 {
		t.Errorf("playlist re-poll: %d Valkey round trips, want 1", n)
	}
	if gets.Load() != 2 {
		t.Errorf("index fetched %d times, want once per playlist request", gets.Load())
	}
}
