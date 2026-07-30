package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/artwork"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/watchevent"
	"github.com/onscreen/onscreen/internal/domain/watchlimit"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/streaming"
)

// ── mock media service ───────────────────────────────────────────────────────

type mockItemMedia struct {
	item     *media.Item
	itemErr  error
	file     *media.File
	fileErr  error
	files    []media.File
	filesErr error
	children []media.Item
	childErr error
}

func (m *mockItemMedia) GetItem(_ context.Context, _ uuid.UUID) (*media.Item, error) {
	if m.itemErr != nil {
		return nil, m.itemErr
	}
	return m.item, nil
}
func (m *mockItemMedia) GetFile(_ context.Context, _ uuid.UUID) (*media.File, error) {
	if m.fileErr != nil {
		return nil, m.fileErr
	}
	return m.file, nil
}
func (m *mockItemMedia) GetFiles(_ context.Context, _ uuid.UUID) ([]media.File, error) {
	if m.filesErr != nil {
		return nil, m.filesErr
	}
	return m.files, nil
}
func (m *mockItemMedia) ListChildren(_ context.Context, _ uuid.UUID) ([]media.Item, error) {
	if m.childErr != nil {
		return nil, m.childErr
	}
	return m.children, nil
}

func (m *mockItemMedia) GetPhotoMetadata(_ context.Context, _ uuid.UUID) (*media.PhotoMetadata, error) {
	return nil, media.ErrNotFound
}
func (m *mockItemMedia) UpdateItemMetadata(_ context.Context, p media.UpdateItemMetadataParams) (*media.Item, error) {
	return &media.Item{ID: p.ID, Title: p.Title, SortTitle: p.SortTitle, Summary: p.Summary, OriginallyAvailableAt: p.OriginallyAvailableAt}, nil
}
func (m *mockItemMedia) RenameItemFile(_ context.Context, _ uuid.UUID, _ string) (string, bool, error) {
	return "", false, nil
}
func (m *mockItemMedia) TouchItemFileMtime(_ context.Context, _ uuid.UUID, _ time.Time) error {
	return nil
}

// ── mock watch service ───────────────────────────────────────────────────────

type mockItemWatch struct {
	state     watchevent.WatchState
	stateErr  error
	recorded  bool
	recordErr error
}

func (m *mockItemWatch) GetState(_ context.Context, _, _ uuid.UUID) (watchevent.WatchState, error) {
	return m.state, m.stateErr
}
func (m *mockItemWatch) GetStates(_ context.Context, _ uuid.UUID, _ []uuid.UUID) (map[uuid.UUID]watchevent.WatchState, error) {
	return map[uuid.UUID]watchevent.WatchState{}, nil
}
func (m *mockItemWatch) Record(_ context.Context, _ watchevent.RecordParams) error {
	m.recorded = true
	return m.recordErr
}

// ── mock session cleaner ─────────────────────────────────────────────────────

type mockSessionCleaner struct{}

func (m *mockSessionCleaner) UpdatePositionByMedia(_ context.Context, _ uuid.UUID, _ int64) error {
	return nil
}
func (m *mockSessionCleaner) DeleteByMedia(_ context.Context, _ uuid.UUID) error { return nil }

// ── mock enricher ────────────────────────────────────────────────────────────

type mockEnricher struct{ called bool }

func (m *mockEnricher) EnrichItem(_ context.Context, _ uuid.UUID) error {
	m.called = true
	return nil
}
func (m *mockEnricher) MatchItem(_ context.Context, _ uuid.UUID, _ int) error {
	return nil
}

// ── mock webhook dispatcher ──────────────────────────────────────────────────

type mockWebhooks struct{ dispatched string }

func (m *mockWebhooks) Dispatch(eventType string, _, _ uuid.UUID) {
	m.dispatched = eventType
}

// ── helpers ──────────────────────────────────────────────────────────────────

func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func newItemHandler(ms *mockItemMedia) *ItemHandler {
	return NewItemHandler(ms, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default())
}

// ── Get item ─────────────────────────────────────────────────────────────────

func TestItemGet_Success(t *testing.T) {
	id := uuid.New()
	fileID := uuid.New()
	ms := &mockItemMedia{
		item:  &media.Item{ID: id, Title: "Test Movie", Type: "movie", Genres: []string{"Action"}},
		files: []media.File{{ID: fileID, Status: "active", FilePath: "/test.mkv"}},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/api/v1/items/"+id.String(), nil), "id", id.String())
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data ItemDetailResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Title != "Test Movie" {
		t.Errorf("title: got %q, want %q", resp.Data.Title, "Test Movie")
	}
	if len(resp.Data.Files) != 1 {
		t.Errorf("files: got %d, want 1", len(resp.Data.Files))
	}
}

func TestItemGet_NotFound(t *testing.T) {
	ms := &mockItemMedia{itemErr: media.ErrNotFound}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	id := uuid.New()
	req := withChiParam(httptest.NewRequest("GET", "/api/v1/items/"+id.String(), nil), "id", id.String())
	h.Get(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestItemGet_InvalidID(t *testing.T) {
	h := newItemHandler(&mockItemMedia{})

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/api/v1/items/bad", nil), "id", "not-a-uuid")
	h.Get(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestItemGet_FiltersInactiveFiles(t *testing.T) {
	id := uuid.New()
	ms := &mockItemMedia{
		item: &media.Item{ID: id, Title: "Movie", Type: "movie"},
		files: []media.File{
			{ID: uuid.New(), Status: "active", FilePath: "/a.mkv"},
			{ID: uuid.New(), Status: "missing", FilePath: "/b.mkv"},
			{ID: uuid.New(), Status: "deleted", FilePath: "/c.mkv"},
		},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", id.String())
	h.Get(rec, req)

	var resp struct {
		Data ItemDetailResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data.Files) != 1 {
		t.Errorf("files: got %d, want 1 (only active)", len(resp.Data.Files))
	}
}

func TestItemGet_WithViewOffset(t *testing.T) {
	id := uuid.New()
	ms := &mockItemMedia{
		item:  &media.Item{ID: id, Title: "Movie", Type: "movie"},
		files: []media.File{},
	}
	ws := &mockItemWatch{
		state: watchevent.WatchState{Status: "in_progress", PositionMS: 45000},
	}
	h := NewItemHandler(ms, ws, &mockSessionCleaner{}, nil, nil, nil, nil, nil, slog.Default())

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", id.String())
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "user"})
	req = req.WithContext(ctx)
	h.Get(rec, req)

	var resp struct {
		Data ItemDetailResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data.ViewOffsetMS != 45000 {
		t.Errorf("view_offset_ms: got %d, want 45000", resp.Data.ViewOffsetMS)
	}
}

// ── Children ─────────────────────────────────────────────────────────────────

func TestItemChildren_Success(t *testing.T) {
	parentID := uuid.New()
	ms := &mockItemMedia{
		item: &media.Item{ID: parentID, Title: "Show", Type: "show"},
		children: []media.Item{
			{ID: uuid.New(), Title: "S01E01", Type: "episode"},
			{ID: uuid.New(), Title: "S01E02", Type: "episode"},
		},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", parentID.String())
	h.Children(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestItemChildren_InvalidID(t *testing.T) {
	h := newItemHandler(&mockItemMedia{})

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", "bad")
	h.Children(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// perChildWatch returns a canned WatchState per media ID for verifying the
// Children handler's watch-state lookup branches.
type perChildWatch struct {
	states map[uuid.UUID]watchevent.WatchState
}

func (p *perChildWatch) GetState(_ context.Context, _, mediaID uuid.UUID) (watchevent.WatchState, error) {
	if s, ok := p.states[mediaID]; ok {
		return s, nil
	}
	return watchevent.WatchState{Status: "unwatched"}, nil
}

func (p *perChildWatch) GetStates(_ context.Context, _ uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]watchevent.WatchState, error) {
	out := make(map[uuid.UUID]watchevent.WatchState, len(ids))
	for _, id := range ids {
		if s, ok := p.states[id]; ok {
			out[id] = s
		}
	}
	return out, nil
}

func (p *perChildWatch) Record(_ context.Context, _ watchevent.RecordParams) error { return nil }

func TestItemChildren_WatchStatePopulated(t *testing.T) {
	parentID := uuid.New()
	inProgressID := uuid.New()
	watchedID := uuid.New()
	unwatchedID := uuid.New()

	ms := &mockItemMedia{
		item: &media.Item{ID: parentID, Title: "Show", Type: "show"},
		children: []media.Item{
			{ID: inProgressID, Title: "E1", Type: "episode"},
			{ID: watchedID, Title: "E2", Type: "episode"},
			{ID: unwatchedID, Title: "E3", Type: "episode"},
		},
	}
	ws := &perChildWatch{states: map[uuid.UUID]watchevent.WatchState{
		inProgressID: {Status: "in_progress", PositionMS: 12345},
		watchedID:    {Status: "watched"},
	}}
	h := NewItemHandler(ms, ws, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default())

	rec := httptest.NewRecorder()
	req := withClaims(withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String()))
	h.Children(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data []ChildItemResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 3 {
		t.Fatalf("len(data): got %d, want 3", len(resp.Data))
	}
	byID := map[string]ChildItemResponse{}
	for _, c := range resp.Data {
		byID[c.ID] = c
	}
	if got := byID[inProgressID.String()]; got.ViewOffsetMS != 12345 || got.Watched {
		t.Errorf("in_progress: got offset=%d watched=%v, want offset=12345 watched=false", got.ViewOffsetMS, got.Watched)
	}
	if got := byID[watchedID.String()]; !got.Watched || got.ViewOffsetMS != 0 {
		t.Errorf("watched: got offset=%d watched=%v, want offset=0 watched=true", got.ViewOffsetMS, got.Watched)
	}
	if got := byID[unwatchedID.String()]; got.Watched || got.ViewOffsetMS != 0 {
		t.Errorf("unwatched: got offset=%d watched=%v, want offset=0 watched=false", got.ViewOffsetMS, got.Watched)
	}
}

func TestItemChildren_WatchStateSkippedWithoutClaims(t *testing.T) {
	parentID := uuid.New()
	childID := uuid.New()
	ms := &mockItemMedia{
		item:     &media.Item{ID: parentID, Title: "Show", Type: "show"},
		children: []media.Item{{ID: childID, Title: "E1", Type: "episode"}},
	}
	ws := &perChildWatch{states: map[uuid.UUID]watchevent.WatchState{
		childID: {Status: "watched"},
	}}
	h := NewItemHandler(ms, ws, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default())

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	h.Children(rec, req)

	var resp struct {
		Data []ChildItemResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len(data): got %d, want 1", len(resp.Data))
	}
	if resp.Data[0].Watched {
		t.Errorf("anonymous request: watched should be false, got true")
	}
}

// ── Progress ─────────────────────────────────────────────────────────────────

func TestProgress_Success(t *testing.T) {
	id := uuid.New()
	ws := &mockItemWatch{}
	h := NewItemHandler(&mockItemMedia{}, ws, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default())

	rec := httptest.NewRecorder()
	body := `{"view_offset_ms":30000,"duration_ms":120000,"state":"playing"}`
	req := withChiParam(httptest.NewRequest("PUT", "/", strings.NewReader(body)), "id", id.String())
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "user"})
	req = req.WithContext(ctx)
	h.Progress(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
	if !ws.recorded {
		t.Error("expected watch event to be recorded")
	}
}

func TestProgress_Unauthorized(t *testing.T) {
	h := newItemHandler(&mockItemMedia{})

	rec := httptest.NewRecorder()
	body := `{"view_offset_ms":30000,"duration_ms":120000,"state":"playing"}`
	req := withChiParam(httptest.NewRequest("PUT", "/", strings.NewReader(body)), "id", uuid.New().String())
	// No claims in context.
	h.Progress(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestProgress_WebhookDispatchOnPause(t *testing.T) {
	id := uuid.New()
	wh := &mockWebhooks{}
	h := NewItemHandler(&mockItemMedia{}, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, wh, nil, streaming.NewTracker(), slog.Default())

	rec := httptest.NewRecorder()
	body := `{"view_offset_ms":30000,"duration_ms":120000,"state":"paused"}`
	req := withChiParam(httptest.NewRequest("PUT", "/", strings.NewReader(body)), "id", id.String())
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "user"})
	req = req.WithContext(ctx)
	h.Progress(rec, req)

	if wh.dispatched != "pause" {
		t.Errorf("webhook event: got %q, want %q", wh.dispatched, "pause")
	}
}

func TestProgress_WebhookDispatchOnStop(t *testing.T) {
	id := uuid.New()
	wh := &mockWebhooks{}
	h := NewItemHandler(&mockItemMedia{}, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, wh, nil, streaming.NewTracker(), slog.Default())

	rec := httptest.NewRecorder()
	body := `{"view_offset_ms":120000,"duration_ms":120000,"state":"stopped"}`
	req := withChiParam(httptest.NewRequest("PUT", "/", strings.NewReader(body)), "id", id.String())
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "user"})
	req = req.WithContext(ctx)
	h.Progress(rec, req)

	if wh.dispatched != "stop" {
		t.Errorf("webhook event: got %q, want %q", wh.dispatched, "stop")
	}
}

func TestProgress_NoWebhookOnPlaying(t *testing.T) {
	id := uuid.New()
	wh := &mockWebhooks{}
	h := NewItemHandler(&mockItemMedia{}, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, wh, nil, streaming.NewTracker(), slog.Default())

	rec := httptest.NewRecorder()
	body := `{"view_offset_ms":30000,"duration_ms":120000,"state":"playing"}`
	req := withChiParam(httptest.NewRequest("PUT", "/", strings.NewReader(body)), "id", id.String())
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "user"})
	req = req.WithContext(ctx)
	h.Progress(rec, req)

	if wh.dispatched != "" {
		t.Errorf("webhook should not dispatch on 'playing', got %q", wh.dispatched)
	}
}

// ── Progress: parental watch-limit gate + accounting ─────────────────────────

// mockItemWatchLimit is an in-memory ItemWatchLimit for Progress enforcement
// tests. addTickCalls counts accruals so a test can assert a 'playing'
// heartbeat accrued exactly once (and a blocked / non-playing one didn't).
type mockItemWatchLimit struct {
	policy       watchlimit.Policy
	policyErr    error
	used         int
	usedErr      error
	addErr       error
	addTickCalls int
}

var _ ItemWatchLimit = (*mockItemWatchLimit)(nil)

func (m *mockItemWatchLimit) GetPolicy(_ context.Context, _ uuid.UUID) (watchlimit.Policy, error) {
	return m.policy, m.policyErr
}
func (m *mockItemWatchLimit) TodayUsageSeconds(_ context.Context, _ uuid.UUID, _ time.Time) (int, error) {
	return m.used, m.usedErr
}
func (m *mockItemWatchLimit) AddTick(_ context.Context, _ uuid.UUID, _, _ time.Time) (int, error) {
	m.addTickCalls++
	return m.used, m.addErr
}

func TestProgress_WatchLimitBlocked(t *testing.T) {
	id := uuid.New()
	ws := &mockItemWatch{}
	// 60-min daily cap, already 120 min used → over cap → blocked.
	wl := &mockItemWatchLimit{
		policy: watchlimit.Policy{DailyLimitMinutes: wlIntPtr(60)},
		used:   120 * 60,
	}
	h := NewItemHandler(&mockItemMedia{}, ws, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default()).
		WithWatchLimit(wl)

	rec := httptest.NewRecorder()
	body := `{"view_offset_ms":30000,"duration_ms":120000,"state":"playing"}`
	req := withChiParam(httptest.NewRequest("PUT", "/", strings.NewReader(body)), "id", id.String())
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "user"}))
	h.Progress(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error.Code != "PARENTAL_LIMIT" {
		t.Errorf("error code: got %q, want PARENTAL_LIMIT", resp.Error.Code)
	}
	if resp.Error.Message != watchlimit.ReasonDailyLimit {
		t.Errorf("reason: got %q, want %q", resp.Error.Message, watchlimit.ReasonDailyLimit)
	}
	// A blocked heartbeat must neither record a watch event nor accrue usage.
	if ws.recorded {
		t.Error("blocked heartbeat must not record a watch event")
	}
	if wl.addTickCalls != 0 {
		t.Errorf("blocked heartbeat must not accrue usage; addTickCalls=%d", wl.addTickCalls)
	}
}

func TestProgress_WatchLimitAllowedAccrues(t *testing.T) {
	id := uuid.New()
	ws := &mockItemWatch{}
	// Restricted (60-min cap) but only 10 min used → allowed, and the
	// 'playing' heartbeat should accrue exactly one tick.
	wl := &mockItemWatchLimit{
		policy: watchlimit.Policy{DailyLimitMinutes: wlIntPtr(60)},
		used:   10 * 60,
	}
	h := NewItemHandler(&mockItemMedia{}, ws, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default()).
		WithWatchLimit(wl)

	rec := httptest.NewRecorder()
	body := `{"view_offset_ms":30000,"duration_ms":120000,"state":"playing"}`
	req := withChiParam(httptest.NewRequest("PUT", "/", strings.NewReader(body)), "id", id.String())
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "user"}))
	h.Progress(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if wl.addTickCalls != 1 {
		t.Errorf("expected exactly one accrual tick, got %d", wl.addTickCalls)
	}
	if !ws.recorded {
		t.Error("expected watch event to be recorded")
	}
}

func TestProgress_WatchLimitSkippedOnPause(t *testing.T) {
	id := uuid.New()
	ws := &mockItemWatch{}
	// A policy that WOULD block (zero cap), but a 'paused' report must
	// bypass the gate entirely so the resume position still saves.
	wl := &mockItemWatchLimit{policy: watchlimit.Policy{DailyLimitMinutes: wlIntPtr(0)}}
	h := NewItemHandler(&mockItemMedia{}, ws, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default()).
		WithWatchLimit(wl)

	rec := httptest.NewRecorder()
	body := `{"view_offset_ms":30000,"duration_ms":120000,"state":"paused"}`
	req := withChiParam(httptest.NewRequest("PUT", "/", strings.NewReader(body)), "id", id.String())
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "user"}))
	h.Progress(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if wl.addTickCalls != 0 {
		t.Errorf("pause must not accrue usage; addTickCalls=%d", wl.addTickCalls)
	}
	if !ws.recorded {
		t.Error("pause report should still be recorded")
	}
}

func TestProgress_WatchLimitFailOpen(t *testing.T) {
	id := uuid.New()
	ws := &mockItemWatch{}
	// GetPolicy errors → fail open: playback proceeds (204) rather than
	// breaking for everyone on a transient DB hiccup. No accrual either.
	wl := &mockItemWatchLimit{policyErr: errors.New("db down")}
	h := NewItemHandler(&mockItemMedia{}, ws, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default()).
		WithWatchLimit(wl)

	rec := httptest.NewRecorder()
	body := `{"view_offset_ms":30000,"duration_ms":120000,"state":"playing"}`
	req := withChiParam(httptest.NewRequest("PUT", "/", strings.NewReader(body)), "id", id.String())
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "user"}))
	h.Progress(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if !ws.recorded {
		t.Error("fail-open: watch event should still be recorded")
	}
	if wl.addTickCalls != 0 {
		t.Errorf("fail-open path must not accrue usage; addTickCalls=%d", wl.addTickCalls)
	}
}

// ── Enrich ───────────────────────────────────────────────────────────────────

func TestEnrich_NoEnricher(t *testing.T) {
	h := NewItemHandler(&mockItemMedia{}, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil, nil, slog.Default())

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", uuid.New().String())
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "admin", IsAdmin: true})
	req = req.WithContext(ctx)
	h.Enrich(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestEnrich_Success(t *testing.T) {
	enricher := &mockEnricher{}
	h := NewItemHandler(&mockItemMedia{}, &mockItemWatch{}, &mockSessionCleaner{}, enricher, nil, nil, nil, nil, slog.Default())

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", uuid.New().String())
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "admin", IsAdmin: true})
	req = req.WithContext(ctx)
	h.Enrich(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestEnrich_NonAdminForbidden(t *testing.T) {
	enricher := &mockEnricher{}
	h := NewItemHandler(&mockItemMedia{}, &mockItemWatch{}, &mockSessionCleaner{}, enricher, nil, nil, nil, nil, slog.Default())

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", uuid.New().String())
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), Username: "user"})
	req = req.WithContext(ctx)
	h.Enrich(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// ── StreamFile ───────────────────────────────────────────────────────────────

func TestStreamFile_NotFound(t *testing.T) {
	ms := &mockItemMedia{fileErr: media.ErrNotFound}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	h.StreamFile(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestStreamFile_InactiveFile(t *testing.T) {
	ms := &mockItemMedia{
		file: &media.File{ID: uuid.New(), Status: "missing", FilePath: "/gone.mkv"},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	h.StreamFile(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestStreamFile_Success_FullBody guards the direct-play happy path:
// active file + an item the user can see → 200 with the file body.
// Without this we only had the 404 negative tests; nothing exercised
// the actual http.ServeFile call against a real file on disk.
func TestStreamFile_Success_FullBody(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "movie.mp4")
	body := []byte("fake-mp4-data\x00\x01\x02\x03\x04")
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ms := &mockItemMedia{
		file: &media.File{ID: uuid.New(), Status: "active", FilePath: tmp, MediaItemID: uuid.New()},
		item: &media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "Test"},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	h.StreamFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.Bytes(); !bytes.Equal(got, body) {
		t.Errorf("body: got %q, want %q", got, body)
	}
}

// TestStreamFile_Range guards http.ServeFile's byte-range support — a
// browser scrubbing a long video sends `Range: bytes=N-` repeatedly,
// and a 200-not-206 reply tanks playback (forces full re-download).
// Asserts both 206 + correct partial content + correct Content-Range
// header for a tail-range request.
func TestStreamFile_Range(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "movie.mp4")
	body := bytes.Repeat([]byte("0123456789"), 100) // 1000 bytes
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ms := &mockItemMedia{
		file: &media.File{ID: uuid.New(), Status: "active", FilePath: tmp, MediaItemID: uuid.New()},
		item: &media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "Test"},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	req.Header.Set("Range", "bytes=100-199")
	h.StreamFile(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status: got %d, want 206 PartialContent", rec.Code)
	}
	if got := rec.Body.Len(); got != 100 {
		t.Errorf("body length: got %d, want 100", got)
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 100-199/1000" {
		t.Errorf("Content-Range: got %q, want %q", cr, "bytes 100-199/1000")
	}
	if !bytes.Equal(rec.Body.Bytes(), body[100:200]) {
		t.Errorf("body slice mismatch")
	}
}

// ── DownloadFile ─────────────────────────────────────────────────────────────
//
// Mirrors the StreamFile coverage but layers Content-Disposition checks
// so a regression that drops the attachment header (and silently
// reverts to inline playback) gets caught here.

// stubDownloadGate satisfies DownloadGate with a fixed bool — lets tests
// flip the admin toggle without dragging the full settings stack in.
type stubDownloadGate struct{ enabled bool }

func (s *stubDownloadGate) WebDownloadsEnabled(_ context.Context) bool { return s.enabled }

// TestDownloadFile_GateDisabled_Returns403 locks the admin toggle: when
// downloads are disabled at the server level the handler must short-
// circuit with 403 + a clear error code BEFORE doing any DB or
// filesystem work, so a client that tries to bypass the hidden UI
// button gets a sensible refusal.
func TestDownloadFile_GateDisabled_Returns403(t *testing.T) {
	// Media stub is ready but should never be touched: gate fires first.
	ms := &mockItemMedia{
		file: &media.File{ID: uuid.New(), Status: "active", FilePath: "/should-not-read.mkv", MediaItemID: uuid.New()},
		item: &media.Item{ID: uuid.New(), Type: "movie", Title: "Test"},
	}
	h := newItemHandler(ms).WithDownloadGate(&stubDownloadGate{enabled: false})

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	h.DownloadFile(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403 when downloads are disabled", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "DOWNLOADS_DISABLED") {
		t.Errorf("body should carry the DOWNLOADS_DISABLED code so clients can render a specific message: %s", rec.Body.String())
	}
}

// TestDownloadFile_GateEnabled_Allows confirms the gate permits
// downloads when the admin has flipped the toggle on. Same fixture
// as the headers/body test, just with an explicit gate=true rather
// than the nil-gate default that the plain handler uses.
func TestDownloadFile_GateEnabled_Allows(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(tmp, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ms := &mockItemMedia{
		file: &media.File{ID: uuid.New(), Status: "active", FilePath: tmp, MediaItemID: uuid.New()},
		item: &media.Item{ID: uuid.New(), Type: "movie", Title: "Test"},
	}
	h := newItemHandler(ms).WithDownloadGate(&stubDownloadGate{enabled: true})

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	h.DownloadFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 when gate is enabled", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition still missing attachment prefix: %q", cd)
	}
}

func TestDownloadFile_NotFound(t *testing.T) {
	ms := &mockItemMedia{fileErr: media.ErrNotFound}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	h.DownloadFile(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDownloadFile_InactiveFile(t *testing.T) {
	ms := &mockItemMedia{
		file: &media.File{ID: uuid.New(), Status: "missing", FilePath: "/gone.mkv"},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	h.DownloadFile(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestDownloadFile_Success_HeadersAndBody(t *testing.T) {
	// Active file + viewable item → 200 with the file body AND a
	// Content-Disposition: attachment header. The disposition bit is
	// the new contract — it's the difference between this route and
	// StreamFile, and the regression to guard against is "developer
	// re-uses StreamFile internally and forgets the header."
	tmp := filepath.Join(t.TempDir(), "movie.mkv")
	body := []byte("fake-mkv-data\x00\x01\x02")
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ms := &mockItemMedia{
		file: &media.File{ID: uuid.New(), Status: "active", FilePath: tmp, MediaItemID: uuid.New()},
		item: &media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "The Movie"},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	h.DownloadFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	if !bytes.Equal(rec.Body.Bytes(), body) {
		t.Errorf("body: got %q, want %q", rec.Body.Bytes(), body)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition must start with `attachment;`; got %q", cd)
	}
	// Filename derives from the item title + the on-disk extension.
	if !strings.Contains(cd, `filename="The Movie.mkv"`) {
		t.Errorf("Content-Disposition filename: got %q, want it to contain `filename=\"The Movie.mkv\"`", cd)
	}
	// UTF-8 form must be present too so non-ASCII titles survive.
	if !strings.Contains(cd, "filename*=UTF-8''") {
		t.Errorf("Content-Disposition must include filename*= form; got %q", cd)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type: got %q, want application/octet-stream", ct)
	}
}

func TestDownloadFile_Range(t *testing.T) {
	// Mobile download managers (Android Download Manager, iOS Safari)
	// use Range to resume an interrupted download. http.ServeFile
	// honours Range when called normally, but if the handler ever
	// gets refactored to write the body itself this test catches a
	// regression to 200-not-206.
	tmp := filepath.Join(t.TempDir(), "movie.mp4")
	body := bytes.Repeat([]byte("0123456789"), 100)
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ms := &mockItemMedia{
		file: &media.File{ID: uuid.New(), Status: "active", FilePath: tmp, MediaItemID: uuid.New()},
		item: &media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "Test"},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	req.Header.Set("Range", "bytes=500-599")
	h.DownloadFile(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status: got %d, want 206 PartialContent", rec.Code)
	}
	if rec.Body.Len() != 100 {
		t.Errorf("body length: got %d, want 100", rec.Body.Len())
	}
}

func TestDownloadFilename(t *testing.T) {
	// Direct unit test for the filename helper. The handler test above
	// exercises the happy path — these cover the sanitisation rules in
	// isolation (header injection, OS-illegal chars, fallbacks).
	cases := []struct {
		name       string
		title      string
		sourcePath string
		want       string
	}{
		{
			name:       "title preferred over basename",
			title:      "The Matrix",
			sourcePath: "/data/abc123.mkv",
			want:       "The Matrix.mkv",
		},
		{
			name:       "empty title falls back to basename without extension",
			title:      "",
			sourcePath: "/data/Movie.Name.2024.mkv",
			want:       "Movie.Name.2024.mkv",
		},
		{
			name:       "CRLF stripped (header injection guard); colon also Windows-illegal so it goes too",
			title:      "Evil\r\nX-Injected: bad",
			sourcePath: "/data/x.mp4",
			want:       "EvilX-Injected_ bad.mp4",
		},
		{
			name:       "quote and backslash stripped (quoted-string termination)",
			title:      `Some "quoted" \name`,
			sourcePath: "/data/x.mp4",
			want:       "Some quoted name.mp4",
		},
		{
			name:       "Windows-illegal chars replaced with underscore",
			title:      "a/b:c*d?e<f>g|h",
			sourcePath: "/data/x.mp4",
			want:       "a_b_c_d_e_f_g_h.mp4",
		},
		{
			name:       "200-char cap",
			title:      strings.Repeat("a", 250),
			sourcePath: "/data/x.mp4",
			want:       strings.Repeat("a", 200) + ".mp4",
		},
		{
			name:       "empty title with dotfile source — base trims ext to empty, falls through to `download`",
			title:      "",
			sourcePath: "/data/.hidden",
			// filepath.Ext(".hidden") = ".hidden" (whole basename), so
			// trimming the extension yields "" → "download" fallback.
			want: "download.hidden",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := downloadFilename(tc.title, tc.sourcePath)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ── itemStateToEventType ─────────────────────────────────────────────────────

func TestItemStateToEventType(t *testing.T) {
	tests := []struct {
		state string
		want  string
	}{
		{"paused", "pause"},
		{"stopped", "stop"},
		{"playing", "play"},
		{"unknown", "play"},
		{"", "play"},
	}
	for _, tt := range tests {
		if got := itemStateToEventType(tt.state); got != tt.want {
			t.Errorf("itemStateToEventType(%q) = %q, want %q", tt.state, got, tt.want)
		}
	}
}

// ── JSONB parsing ────────────────────────────────────────────────────────────

func TestParseJSONBAudioStreams_Empty(t *testing.T) {
	got := parseJSONBAudioStreams(nil)
	if len(got) != 0 {
		t.Errorf("want empty, got %d", len(got))
	}
}

func TestParseJSONBAudioStreams_Valid(t *testing.T) {
	data := []byte(`[{"index":0,"codec":"aac","channels":2,"language":"eng","title":"Stereo"}]`)
	got := parseJSONBAudioStreams(data)
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if got[0].Codec != "aac" {
		t.Errorf("codec: got %q, want %q", got[0].Codec, "aac")
	}
	if got[0].Channels != 2 {
		t.Errorf("channels: got %d, want 2", got[0].Channels)
	}
}

func TestParseJSONBSubtitleStreams_Valid(t *testing.T) {
	data := []byte(`[{"index":0,"codec":"srt","language":"eng","title":"English","forced":true}]`)
	got := parseJSONBSubtitleStreams(data)
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if !got[0].Forced {
		t.Error("expected forced=true")
	}
}

func TestParseJSONBSubtitleStreams_InvalidJSON(t *testing.T) {
	got := parseJSONBSubtitleStreams([]byte("not json"))
	if len(got) != 0 {
		t.Errorf("want empty for invalid JSON, got %d", len(got))
	}
}

// ── Content rating filtering ────────────────────────────────────────────────

func TestItemGet_ContentRating_Blocked(t *testing.T) {
	id := uuid.New()
	rating := "R"
	ms := &mockItemMedia{
		item:  &media.Item{ID: id, Title: "Adult Movie", Type: "movie", ContentRating: &rating},
		files: []media.File{},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", id.String())
	// User restricted to PG-13.
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{
		UserID:           uuid.New(),
		Username:         "child",
		MaxContentRating: "PG-13",
	})
	req = req.WithContext(ctx)
	h.Get(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d (R content blocked by PG-13 profile)", rec.Code, http.StatusForbidden)
	}
}

func TestItemGet_ContentRating_Allowed(t *testing.T) {
	id := uuid.New()
	rating := "PG"
	ms := &mockItemMedia{
		item:  &media.Item{ID: id, Title: "Family Movie", Type: "movie", ContentRating: &rating},
		files: []media.File{},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", id.String())
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{
		UserID:           uuid.New(),
		Username:         "child",
		MaxContentRating: "PG-13",
	})
	req = req.WithContext(ctx)
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d (PG allowed by PG-13 profile)", rec.Code, http.StatusOK)
	}
}

func TestItemGet_ContentRating_NoRestriction(t *testing.T) {
	id := uuid.New()
	rating := "NC-17"
	ms := &mockItemMedia{
		item:  &media.Item{ID: id, Title: "Unrestricted Movie", Type: "movie", ContentRating: &rating},
		files: []media.File{},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", id.String())
	// User with no content rating restriction.
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{
		UserID:   uuid.New(),
		Username: "admin",
	})
	req = req.WithContext(ctx)
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d (no restriction)", rec.Code, http.StatusOK)
	}
}

func TestItemGet_ContentRating_UnratedContentBlocked(t *testing.T) {
	id := uuid.New()
	ms := &mockItemMedia{
		item:  &media.Item{ID: id, Title: "Unrated Movie", Type: "movie"}, // nil ContentRating
		files: []media.File{},
	}
	h := newItemHandler(ms)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", id.String())
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{
		UserID:           uuid.New(),
		Username:         "child",
		MaxContentRating: "G",
	})
	req = req.WithContext(ctx)
	h.Get(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want %d (unrated content blocked — treated as rank 4)", rec.Code, http.StatusForbidden)
	}
}

// TestSnapToChapterStart covers the audiobook resume-snap helper. The
// invariants under test:
//
//   - Position before the first chapter passes through unchanged
//     (caller intent is "start from there", not "reset to chapter 1").
//   - Position inside a chapter snaps to that chapter's start.
//   - Position exactly on a chapter boundary returns that boundary
//     (idempotent; a previously-snapped position survives a re-snap).
//   - Empty / nil chapter slice is a no-op.
func TestSnapToChapterStart(t *testing.T) {
	chapters := []ChapterJSON{
		{Title: "Ch 1", StartMS: 0, EndMS: 60_000},
		{Title: "Ch 2", StartMS: 60_000, EndMS: 180_000},
		{Title: "Ch 3", StartMS: 180_000, EndMS: 360_000},
	}

	cases := []struct {
		name string
		pos  int64
		want int64
	}{
		{"pre-first chapter passes through", -100, -100},
		{"inside chapter 1 snaps to 0", 30_000, 0},
		{"on chapter 2 boundary stays at 60000", 60_000, 60_000},
		{"inside chapter 2 snaps to 60000", 100_000, 60_000},
		{"inside chapter 3 snaps to 180000", 250_000, 180_000},
		{"past last chapter snaps to last start", 999_999, 180_000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := snapToChapterStart(chapters, c.pos); got != c.want {
				t.Errorf("snapToChapterStart(%d) = %d, want %d", c.pos, got, c.want)
			}
		})
	}

	if got := snapToChapterStart(nil, 12_345); got != 12_345 {
		t.Errorf("snapToChapterStart(nil) = %d, want pass-through", got)
	}
	if got := snapToChapterStart([]ChapterJSON{}, 12_345); got != 12_345 {
		t.Errorf("snapToChapterStart(empty) = %d, want pass-through", got)
	}
}

// ── ApplyPoster ──────────────────────────────────────────────────────────────

type mockPosterPicker struct {
	listResult []metadata.PosterCandidate
	listErr    error
	setErr     error
}

func (m *mockPosterPicker) ListPosters(_ context.Context, _ string, _ int) ([]metadata.PosterCandidate, error) {
	return m.listResult, m.listErr
}
func (m *mockPosterPicker) SetItemPoster(_ context.Context, _ uuid.UUID, _ string) error {
	return m.setErr
}

// TestApplyPoster_DownloadHTTPError_Returns400 locks the contract that
// surfaced the user-confusion bug: when the pasted URL is a Wikipedia
// file page (403) or IMDB mediaviewer (202) instead of a direct image,
// the handler returns 400 with the actual upstream HTTP status in the
// message — not a generic 500 that leaves operators guessing whether
// the URL was wrong or the server itself broke.
func TestApplyPoster_DownloadHTTPError_Returns400(t *testing.T) {
	ms := &mockItemMedia{item: &media.Item{ID: uuid.New(), Type: "movie", Title: "Test"}}
	picker := &mockPosterPicker{
		setErr: &artwork.DownloadHTTPError{URL: "https://example.com/page.html", StatusCode: 403},
	}
	h := NewItemHandler(ms, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default())
	h = h.WithPosterPicker(picker)

	body := bytes.NewBufferString(`{"url":"https://example.com/page.html"}`)
	req := withChiParam(httptest.NewRequest(http.MethodPost, "/", body), "id", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	req = adminClaims(req)

	rec := httptest.NewRecorder()
	h.ApplyPoster(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (DownloadHTTPError must surface as 4xx, not generic 500)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "403") {
		t.Errorf("body: %s — should mention upstream status 403 so the operator knows the URL was the problem", rec.Body.String())
	}
}

// TestApplyPoster_GenericError_Returns500 confirms the typed-error
// special-case doesn't swallow other failure modes — a generic
// "couldn't write the file" still goes to 500 and is logged.
func TestApplyPoster_GenericError_Returns500(t *testing.T) {
	ms := &mockItemMedia{item: &media.Item{ID: uuid.New(), Type: "movie", Title: "Test"}}
	picker := &mockPosterPicker{setErr: errors.New("disk write failed")}
	h := NewItemHandler(ms, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil, streaming.NewTracker(), slog.Default())
	h = h.WithPosterPicker(picker)

	body := bytes.NewBufferString(`{"url":"https://upstream.example/poster.jpg"}`)
	req := withChiParam(httptest.NewRequest(http.MethodPost, "/", body), "id", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	req = adminClaims(req)

	rec := httptest.NewRecorder()
	h.ApplyPoster(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500 for non-HTTP errors", rec.Code)
	}
}

// TestServeSubtitle_CacheHit proves a pre-cached extracted VTT is served
// directly (no ffmpeg), which is the fast path that makes 4K-remux subtitles
// instant on every request after the first extraction.
func TestServeSubtitle_CacheHit(t *testing.T) {
	cacheDir := t.TempDir()
	fileID := uuid.New()
	itemID := uuid.New()
	libID := uuid.New()
	streamIdx := 3

	// A text subtitle stream at index 3 so the image-sub guard passes.
	subsJSON := []byte(`[{"index":3,"codec":"subrip","language":"eng","forced":false,"sdh":false}]`)
	ms := &mockItemMedia{
		file: &media.File{ID: fileID, Status: "active", FilePath: "/movies/300.mkv",
			MediaItemID: itemID, SubtitleStreams: subsJSON},
		item: &media.Item{ID: itemID, LibraryID: libID, Type: "movie"},
	}
	h := newItemHandler(ms).WithSubtitleCache(cacheDir)

	// Pre-populate the cache exactly where ServeSubtitle looks.
	cachePath := filepath.Join(cacheDir, "embedded", fileID.String(), "3.vtt")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		t.Fatal(err)
	}
	want := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nCached line\n"
	if err := os.WriteFile(cachePath, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("fileId", fileID.String())
	rctx.URLParams.Add("streamIndex", strconv.Itoa(streamIdx))
	req := httptest.NewRequest("GET", "/media/subtitles/"+fileID.String()+"/3", nil).
		WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.ServeSubtitle(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (body=%q)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != want {
		t.Fatalf("served body mismatch:\ngot  %q\nwant %q", rec.Body.String(), want)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/vtt; charset=utf-8" {
		t.Fatalf("content-type: got %q", ct)
	}
}

// TestServeSubtitle_ImageSubRejected proves the image-based codec guard
// returns 415 before any extraction.
func TestServeSubtitle_ImageSubRejected(t *testing.T) {
	fileID := uuid.New()
	itemID := uuid.New()
	subsJSON := []byte(`[{"index":2,"codec":"hdmv_pgs_subtitle","language":"eng"}]`)
	ms := &mockItemMedia{
		file: &media.File{ID: fileID, Status: "active", FilePath: "/movies/Avatar.mkv",
			MediaItemID: itemID, SubtitleStreams: subsJSON},
		item: &media.Item{ID: itemID, LibraryID: uuid.New(), Type: "movie"},
	}
	h := newItemHandler(ms).WithSubtitleCache(t.TempDir())

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("fileId", fileID.String())
	rctx.URLParams.Add("streamIndex", "2")
	req := httptest.NewRequest("GET", "/x", nil).
		WithContext(context.WithValue(context.Background(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.ServeSubtitle(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status: got %d, want 415", rec.Code)
	}
}

// ── watch-limit bypass regressions (audit round 2) ──────────────────────────

// StreamFile used to gate the parental limit only on isPlaybackStart(r) — no
// Range header, or one beginning at byte 0. `Range: bytes=1-` is trivially
// settable and never satisfied it, so the whole file streamed outside allowed
// hours. Direct play is the default path for compatible media.
func TestStreamFile_WatchLimitBlocksNonZeroRange(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "movie.mp4")
	if err := os.WriteFile(tmp, bytes.Repeat([]byte("x"), 1000), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ranges := []string{"", "bytes=0-", "bytes=1-", "bytes=500-999", "bytes=999-"}
	for _, rng := range ranges {
		name := rng
		if name == "" {
			name = "(no Range)"
		}
		t.Run(name, func(t *testing.T) {
			ms := &mockItemMedia{
				file: &media.File{ID: uuid.New(), Status: "active", FilePath: tmp, MediaItemID: uuid.New()},
				item: &media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "Test"},
			}
			wl := &mockItemWatchLimit{
				policy: watchlimit.Policy{DailyLimitMinutes: wlIntPtr(60)},
				used:   120 * 60, // double the cap — must block
			}
			h := NewItemHandler(ms, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil,
				streaming.NewTracker(), slog.Default()).WithWatchLimit(wl)

			rec := httptest.NewRecorder()
			req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
			if rng != "" {
				req.Header.Set("Range", rng)
			}
			req = req.WithContext(middleware.WithClaims(req.Context(),
				&auth.Claims{UserID: uuid.New(), Username: "kid"}))
			h.StreamFile(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("Range %q: got %d, want 403 — a range request that skips the "+
					"gate streams the whole file past the daily cap", rng, rec.Code)
			}
		})
	}
}

// An unrestricted user must still stream on every range shape — the fix must
// gate more requests, not break playback.
func TestStreamFile_UnrestrictedUserUnaffectedByGate(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "movie.mp4")
	if err := os.WriteFile(tmp, bytes.Repeat([]byte("y"), 1000), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ms := &mockItemMedia{
		file: &media.File{ID: uuid.New(), Status: "active", FilePath: tmp, MediaItemID: uuid.New()},
		item: &media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "Test"},
	}
	h := NewItemHandler(ms, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil,
		streaming.NewTracker(), slog.Default()).
		WithWatchLimit(&mockItemWatchLimit{}) // zero policy = unrestricted

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	req.Header.Set("Range", "bytes=100-199")
	req = req.WithContext(middleware.WithClaims(req.Context(),
		&auth.Claims{UserID: uuid.New(), Username: "adult"}))
	h.StreamFile(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Errorf("unrestricted user: got %d, want 206", rec.Code)
	}
}

// ── rating endpoints authz (audit round 2) ─────────────────────────────────

type stubRatings struct {
	setCalls   int
	clearCalls int
	getCalls   int
}

func (s *stubRatings) Get(_ context.Context, _, _ uuid.UUID) (float64, error) {
	s.getCalls++
	return 7, nil
}
func (s *stubRatings) Set(_ context.Context, _, _ uuid.UUID, _ float64) error {
	s.setCalls++
	return nil
}
func (s *stubRatings) Clear(_ context.Context, _, _ uuid.UUID) error {
	s.clearCalls++
	return nil
}
func (s *stubRatings) CommunityAverage(_ context.Context, _ uuid.UUID) (float64, int, error) {
	return 0, 0, nil
}

type denyAllAccess struct{}

func (denyAllAccess) CanAccessLibrary(_ context.Context, _, _ uuid.UUID, isAdmin bool) (bool, error) {
	return isAdmin, nil
}
func (denyAllAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, _ bool) (map[uuid.UUID]struct{}, error) {
	return map[uuid.UUID]struct{}{}, nil
}

// 72d06ac routed GET/PUT/DELETE /items/{id}/rating for the first time. The
// handlers never loaded the item, so any authenticated caller could rate an
// item in a library it cannot see — and the score feeds the community average
// other users read. Routing dormant handlers is what made this reachable.
func TestRating_RejectsItemInInvisibleLibrary(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*ItemHandler, http.ResponseWriter, *http.Request)
		body string
	}{
		{"get", (*ItemHandler).GetRating, ""},
		{"set", (*ItemHandler).SetRating, `{"score":9}`},
		{"delete", (*ItemHandler).DeleteRating, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ms := &mockItemMedia{
				item: &media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "Hidden"},
			}
			rs := &stubRatings{}
			h := newItemHandler(ms).WithRatings(rs).WithLibraryAccess(denyAllAccess{})

			req := withChiParam(
				httptest.NewRequest("PUT", "/", strings.NewReader(tc.body)), "id", uuid.New().String())
			req = req.WithContext(middleware.WithClaims(req.Context(),
				&auth.Claims{UserID: uuid.New(), Username: "outsider"}))
			rec := httptest.NewRecorder()
			tc.call(h, rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("got %d, want 404 (fail-closed, not an existence oracle)", rec.Code)
			}
			if rs.setCalls+rs.clearCalls+rs.getCalls != 0 {
				t.Errorf("ratings store was reached for an item in an invisible library")
			}
		})
	}
}

// The ceiling must gate ratings too: an R-rated item is not rateable by a
// PG-restricted profile.
func TestRating_RejectsItemAboveRatingCeiling(t *testing.T) {
	rating := "R"
	ms := &mockItemMedia{
		item: &media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie",
			Title: "Adult", ContentRating: &rating},
	}
	rs := &stubRatings{}
	h := newItemHandler(ms).WithRatings(rs)

	req := withChiParam(httptest.NewRequest("PUT", "/", strings.NewReader(`{"score":10}`)),
		"id", uuid.New().String())
	req = req.WithContext(middleware.WithClaims(req.Context(),
		&auth.Claims{UserID: uuid.New(), Username: "kid", MaxContentRating: "PG"}))
	rec := httptest.NewRecorder()
	h.SetRating(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
	if rs.setCalls != 0 {
		t.Error("an R-rated item was rated by a PG-restricted profile")
	}
}

// A permitted rating must still go through.
func TestRating_AllowsVisibleInCeilingItem(t *testing.T) {
	rating := "G"
	ms := &mockItemMedia{
		item: &media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie",
			Title: "Kids", ContentRating: &rating},
	}
	rs := &stubRatings{}
	h := newItemHandler(ms).WithRatings(rs)

	req := withChiParam(httptest.NewRequest("PUT", "/", strings.NewReader(`{"score":8}`)),
		"id", uuid.New().String())
	req = req.WithContext(middleware.WithClaims(req.Context(),
		&auth.Claims{UserID: uuid.New(), Username: "kid", MaxContentRating: "PG"}))
	rec := httptest.NewRecorder()
	h.SetRating(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rs.setCalls != 1 {
		t.Error("a permitted rating was not stored")
	}
}
