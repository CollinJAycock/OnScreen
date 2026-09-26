package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/watchevent"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

// ── mocks ────────────────────────────────────────────────────────────────────

// captureWatch records the params of every watch event so a test can assert
// the decision Progress attributed.
type captureWatch struct{ events []watchevent.RecordParams }

func (m *captureWatch) GetState(_ context.Context, _, _ uuid.UUID) (watchevent.WatchState, error) {
	return watchevent.WatchState{}, nil
}
func (m *captureWatch) GetStates(_ context.Context, _ uuid.UUID, _ []uuid.UUID) (map[uuid.UUID]watchevent.WatchState, error) {
	return map[uuid.UUID]watchevent.WatchState{}, nil
}
func (m *captureWatch) Record(_ context.Context, p watchevent.RecordParams) error {
	m.events = append(m.events, p)
	return nil
}

func (m *captureWatch) lastDecision(t *testing.T) *string {
	t.Helper()
	if len(m.events) == 0 {
		t.Fatal("no watch event recorded")
	}
	return m.events[len(m.events)-1].Decision
}

// listingSessions is an ItemSessionCleaner that also implements the optional
// itemSessionLister, returning canned sessions for any (user, item).
type listingSessions struct {
	mockSessionCleaner
	sessions []transcode.Session
	lists    int
}

func (m *listingSessions) ListByUserItem(_ context.Context, _, _ uuid.UUID) ([]transcode.Session, error) {
	m.lists++
	return m.sessions, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func progressReq(t *testing.T, itemID, userID uuid.UUID, body string) *http.Request {
	t.Helper()
	req := withChiParam(httptest.NewRequest("PUT", "/", strings.NewReader(body)), "id", itemID.String())
	return req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: userID, Username: "user"}))
}

func runProgress(t *testing.T, h *ItemHandler, itemID, userID uuid.UUID, body string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Progress(rec, progressReq(t, itemID, userID, body))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("progress status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

// streamFileAs serves a real temp file through StreamFile as userID; the file
// belongs to itemID.
func streamFileAs(t *testing.T, h *ItemHandler, ms *mockItemMedia, itemID, userID uuid.UUID, userAgent string) {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "movie.mp4")
	if err := os.WriteFile(tmp, []byte("fake-mp4"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ms.file = &media.File{ID: uuid.New(), Status: "active", FilePath: tmp, MediaItemID: itemID}
	ms.item = &media.Item{ID: itemID, LibraryID: uuid.New(), Type: "movie", Title: "Test"}

	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", ms.file.ID.String())
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: userID, Username: "user"}))
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	rec := httptest.NewRecorder()
	h.StreamFile(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stream status: got %d, want 200", rec.Code)
	}
}

const heartbeatNoDecision = `{"view_offset_ms":30000,"duration_ms":120000,"state":"playing"}`

// ── Progress attribution ─────────────────────────────────────────────────────

func TestProgress_NoDecision_UsesNewestActiveSession(t *testing.T) {
	ws := &captureWatch{}
	now := time.Now()
	sessions := &listingSessions{sessions: []transcode.Session{
		{ID: "old", Decision: "transcode", CreatedAt: now.Add(-time.Minute)},
		{ID: "new", Decision: "remux", CreatedAt: now},
	}}
	// The recorder disagrees; a live session is more authoritative.
	rec := NewPlayDecisionRecorder(nil, slog.Default())
	userID, itemID := uuid.New(), uuid.New()
	rec.Record(context.Background(), userID, itemID, decisionDirectPlay)
	h := NewItemHandler(&mockItemMedia{}, ws, sessions, nil, nil, nil, nil, streaming.NewTracker(), slog.Default()).
		WithPlayDecisions(rec)

	runProgress(t, h, itemID, userID, heartbeatNoDecision)

	if d := ws.lastDecision(t); d == nil || *d != "remux" {
		t.Fatalf("decision: got %v, want remux (newest live session)", d)
	}
}

func TestProgress_NoDecision_DirectPlayAfterStreamFile(t *testing.T) {
	ws := &captureWatch{}
	ms := &mockItemMedia{}
	h := NewItemHandler(ms, ws, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default()).
		WithPlayDecisions(NewPlayDecisionRecorder(nil, slog.Default()))
	userID, itemID := uuid.New(), uuid.New()

	streamFileAs(t, h, ms, itemID, userID, "ExoPlayer/2.19")
	runProgress(t, h, itemID, userID, `{"view_offset_ms":120000,"duration_ms":120000,"state":"stopped"}`)

	if d := ws.lastDecision(t); d == nil || *d != decisionDirectPlay {
		t.Fatalf("decision: got %v, want directPlay", d)
	}

	// Another user's heartbeat on the same item must not inherit it.
	runProgress(t, h, itemID, uuid.New(), heartbeatNoDecision)
	if d := ws.lastDecision(t); d != nil {
		t.Errorf("other user's decision: got %q, want nil", *d)
	}
}

func TestProgress_NoDecision_StaysNullWithoutEvidence(t *testing.T) {
	ws := &captureWatch{}
	h := NewItemHandler(&mockItemMedia{}, ws, &listingSessions{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default()).
		WithPlayDecisions(NewPlayDecisionRecorder(nil, slog.Default()))

	runProgress(t, h, uuid.New(), uuid.New(), heartbeatNoDecision)

	if d := ws.lastDecision(t); d != nil {
		t.Errorf("decision: got %q, want nil (no session, nothing recorded)", *d)
	}
}

func TestProgress_ClientDecisionWinsWithoutLookup(t *testing.T) {
	ws := &captureWatch{}
	sessions := &listingSessions{sessions: []transcode.Session{{Decision: "transcode", CreatedAt: time.Now()}}}
	h := NewItemHandler(&mockItemMedia{}, ws, sessions, nil, nil, nil, nil, streaming.NewTracker(), slog.Default()).
		WithPlayDecisions(NewPlayDecisionRecorder(nil, slog.Default()))

	runProgress(t, h, uuid.New(), uuid.New(),
		`{"view_offset_ms":1000,"duration_ms":120000,"state":"playing","decision":"directStream"}`)

	if d := ws.lastDecision(t); d == nil || *d != "directStream" {
		t.Fatalf("decision: got %v, want the client's directStream", d)
	}
	if sessions.lists != 0 {
		t.Errorf("a client-supplied decision must not cost a session lookup; lists=%d", sessions.lists)
	}
}

func TestProgress_JunkDecisionFallsBackToServer(t *testing.T) {
	ws := &captureWatch{}
	sessions := &listingSessions{sessions: []transcode.Session{{Decision: "transcode", CreatedAt: time.Now()}}}
	h := NewItemHandler(&mockItemMedia{}, ws, sessions, nil, nil, nil, nil, streaming.NewTracker(), slog.Default())

	runProgress(t, h, uuid.New(), uuid.New(),
		`{"view_offset_ms":1000,"duration_ms":120000,"state":"playing","decision":"<script>"}`)

	if d := ws.lastDecision(t); d == nil || *d != "transcode" {
		t.Fatalf("decision: got %v, want transcode from the session", d)
	}
}

// ── StreamFile: what is and isn't a direct play ──────────────────────────────

func TestStreamFile_WorkerSourcePullIsNotDirectPlay(t *testing.T) {
	ms := &mockItemMedia{}
	rec := NewPlayDecisionRecorder(nil, slog.Default())
	h := NewItemHandler(ms, &captureWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default()).
		WithPlayDecisions(rec)
	userID, itemID := uuid.New(), uuid.New()
	rec.Record(context.Background(), userID, itemID, "transcode")

	// A storage-less worker's ffmpeg pulling the source with the viewer's
	// stream token (after the session is already gone).
	streamFileAs(t, h, ms, itemID, userID, "Lavf/61.7.100")

	if got := rec.Last(context.Background(), userID, itemID); got != "transcode" {
		t.Errorf("worker pull overwrote the decision: got %q, want transcode", got)
	}
}

func TestStreamFile_LiveSessionIsNotDirectPlay(t *testing.T) {
	ms := &mockItemMedia{}
	rec := NewPlayDecisionRecorder(nil, slog.Default())
	sessions := &listingSessions{sessions: []transcode.Session{{Decision: "remux", CreatedAt: time.Now()}}}
	h := NewItemHandler(ms, &captureWatch{}, sessions, nil, nil, nil, nil, streaming.NewTracker(), slog.Default()).
		WithPlayDecisions(rec)
	userID, itemID := uuid.New(), uuid.New()

	streamFileAs(t, h, ms, itemID, userID, "")

	if got := rec.Last(context.Background(), userID, itemID); got != "" {
		t.Errorf("bytes served during a live session recorded %q; want nothing", got)
	}
}

// ── transcode Start records the session decision ────────────────────────────

// startWithRecorder runs a successful Start and returns a FRESH recorder on the
// same Valkey (another API instance answering the progress beacon), the
// session store, and the (user, item) it started.
func startWithRecorder(t *testing.T, cfg *config.Config, file media.File, req transcodeStartRequest) (*PlayDecisionRecorder, *transcode.SessionStore, uuid.UUID, uuid.UUID) {
	t.Helper()
	v := testvalkey.New(t)
	itemID, userID := uuid.New(), uuid.New()
	file.ID = uuid.New()
	file.MediaItemID = itemID
	store := transcode.NewSessionStore(v)
	h := NewNativeTranscodeHandler(store, transcode.NewSegmentTokenManager(v), &mockTranscodeMedia{
		item:  &media.Item{ID: itemID, Type: "movie", Title: "Test"},
		files: []media.File{file},
	}, cfg, slog.Default()).WithVerifySource(fakeSourceOK).WithPlayDecisions(NewPlayDecisionRecorder(v, slog.Default()))

	body, _ := json.Marshal(req)
	r := withChiParam(httptest.NewRequest("POST", "/api/v1/items/"+itemID.String()+"/transcode", bytes.NewReader(body)), "id", itemID.String())
	r = r.WithContext(middleware.WithClaims(r.Context(), &auth.Claims{UserID: userID, Username: "user"}))
	w := httptest.NewRecorder()
	h.Start(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Start: %d %s", w.Code, w.Body.String())
	}
	return NewPlayDecisionRecorder(v, slog.Default()), store, userID, itemID
}

func TestStart_RecordsSessionDecision(t *testing.T) {
	file := media.File{FilePath: "/media/test.mkv", VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac")}
	for _, tc := range []struct {
		name string
		cfg  *config.Config
		file media.File
		req  transcodeStartRequest
		want string
	}{
		{"remux", &config.Config{TranscodeMaxHeight: 2160}, file, transcodeStartRequest{VideoCopy: true}, "remux"},
		{"transcode", &config.Config{TranscodeMaxHeight: 2160}, file, transcodeStartRequest{Height: 720}, "transcode"},
		{"abr", &config.Config{TranscodeMaxHeight: 2160, TranscodeABR: true}, media.File{
			FilePath: "/media/test.mkv", VideoCodec: strPtr("h264"), AudioCodec: strPtr("aac"),
			DurationMS: pdPtr(int64(3_600_000)), ResolutionW: pdPtr(1920), ResolutionH: pdPtr(1080),
		}, transcodeStartRequest{}, "transcode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, store, userID, itemID := startWithRecorder(t, tc.cfg, tc.file, tc.req)
			if got := rec.Last(context.Background(), userID, itemID); got != tc.want {
				t.Errorf("recorded decision: got %q, want %q", got, tc.want)
			}
			sessions, err := store.ListByUserItem(context.Background(), userID, itemID)
			if err != nil || len(sessions) != 1 || sessions[0].ABR != (tc.name == "abr") {
				t.Errorf("expected one session with ABR=%v; got %+v (err %v)", tc.name == "abr", sessions, err)
			}
		})
	}
}

// ── PlayDecisionRecorder ─────────────────────────────────────────────────────

func TestPlayDecisionRecorder_MemoryTTL(t *testing.T) {
	now := time.Date(2026, 9, 26, 20, 0, 0, 0, time.UTC)
	rec := NewPlayDecisionRecorder(nil, slog.Default())
	rec.now = func() time.Time { return now }
	ctx := context.Background()
	userID, itemID := uuid.New(), uuid.New()

	rec.Record(ctx, userID, itemID, "bogus")
	if got := rec.Last(ctx, userID, itemID); got != "" {
		t.Fatalf("non-allowlisted decision recorded: %q", got)
	}

	rec.Record(ctx, userID, itemID, "transcode")
	now = now.Add(playDecisionTTL - time.Second)
	if got := rec.Last(ctx, userID, itemID); got != "transcode" {
		t.Fatalf("inside TTL: got %q, want transcode", got)
	}
	now = now.Add(2 * time.Second)
	if got := rec.Last(ctx, userID, itemID); got != "" {
		t.Fatalf("past TTL: got %q, want expired", got)
	}

	// The next write sweeps the expired entry out of the map.
	rec.Record(ctx, uuid.New(), uuid.New(), decisionDirectPlay)
	rec.mu.Lock()
	_, stale := rec.local[playDecisionKey{userID, itemID}]
	n := len(rec.local)
	rec.mu.Unlock()
	if stale || n != 1 {
		t.Errorf("expired entry not swept: stale=%v entries=%d", stale, n)
	}
}

func TestPlayDecisionRecorder_RefreshThrottle(t *testing.T) {
	now := time.Now()
	rec := NewPlayDecisionRecorder(nil, slog.Default())
	rec.now = func() time.Time { return now }
	userID, itemID := uuid.New(), uuid.New()

	rec.Record(context.Background(), userID, itemID, decisionDirectPlay)
	if !rec.recentlyRecorded(userID, itemID, decisionDirectPlay) {
		t.Fatal("fresh record should be recent")
	}
	if rec.recentlyRecorded(userID, itemID, "transcode") {
		t.Error("a different decision is never 'recent'")
	}
	now = now.Add(playDecisionRefreshEvery)
	if rec.recentlyRecorded(userID, itemID, decisionDirectPlay) {
		t.Error("record older than the refresh window should be rewritten")
	}
}

func TestPlayDecisionRecorder_ValkeySharedWithTTL(t *testing.T) {
	v := testvalkey.New(t)
	ctx := context.Background()
	userID, itemID := uuid.New(), uuid.New()

	NewPlayDecisionRecorder(v, slog.Default()).Record(ctx, userID, itemID, "remux")

	if got := NewPlayDecisionRecorder(v, slog.Default()).Last(ctx, userID, itemID); got != "remux" {
		t.Fatalf("other instance: got %q, want remux", got)
	}
	ttl, err := v.Raw().TTL(ctx, playDecisionRedisKey(userID, itemID)).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= playDecisionTTL-time.Minute || ttl > playDecisionTTL {
		t.Errorf("key TTL: got %v, want ~%v", ttl, playDecisionTTL)
	}
}

func TestPlayDecisionRecorder_ValkeyDownFallsBackToLocal(t *testing.T) {
	v := testvalkey.New(t)
	rec := NewPlayDecisionRecorder(v, slog.Default())
	_ = v.Close() // every Valkey call now errors
	ctx := context.Background()
	userID, itemID := uuid.New(), uuid.New()

	rec.Record(ctx, userID, itemID, "transcode")
	if got := rec.Last(ctx, userID, itemID); got != "transcode" {
		t.Errorf("valkey down: got %q, want the in-process transcode", got)
	}
}

func TestPlayDecisionRecorder_NilIsNoop(t *testing.T) {
	var rec *PlayDecisionRecorder
	rec.Record(context.Background(), uuid.New(), uuid.New(), "transcode")
	if got := rec.Last(context.Background(), uuid.New(), uuid.New()); got != "" {
		t.Errorf("nil recorder: got %q", got)
	}
}

func pdPtr[T any](v T) *T { return &v }
