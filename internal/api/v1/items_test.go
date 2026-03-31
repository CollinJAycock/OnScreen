package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/watchevent"
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

// ── mock watch service ───────────────────────────────────────────────────────

type mockItemWatch struct {
	state    watchevent.WatchState
	stateErr error
	recorded bool
	recordErr error
}

func (m *mockItemWatch) GetState(_ context.Context, _, _ uuid.UUID) (watchevent.WatchState, error) {
	return m.state, m.stateErr
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
	return NewItemHandler(ms, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, nil, streaming.NewTracker(), slog.Default())
}

// ── Get item ─────────────────────────────────────────────────────────────────

func TestItemGet_Success(t *testing.T) {
	id := uuid.New()
	fileID := uuid.New()
	ms := &mockItemMedia{
		item: &media.Item{ID: id, Title: "Test Movie", Type: "movie", Genres: []string{"Action"}},
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
	h := NewItemHandler(ms, ws, &mockSessionCleaner{}, nil, nil, nil, nil, slog.Default())

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

// ── Progress ─────────────────────────────────────────────────────────────────

func TestProgress_Success(t *testing.T) {
	id := uuid.New()
	ws := &mockItemWatch{}
	h := NewItemHandler(&mockItemMedia{}, ws, &mockSessionCleaner{}, nil, nil, nil, streaming.NewTracker(), slog.Default())

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
	h := NewItemHandler(&mockItemMedia{}, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, wh, streaming.NewTracker(), slog.Default())

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
	h := NewItemHandler(&mockItemMedia{}, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, wh, streaming.NewTracker(), slog.Default())

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
	h := NewItemHandler(&mockItemMedia{}, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, wh, streaming.NewTracker(), slog.Default())

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

// ── Enrich ───────────────────────────────────────────────────────────────────

func TestEnrich_NoEnricher(t *testing.T) {
	h := NewItemHandler(&mockItemMedia{}, &mockItemWatch{}, &mockSessionCleaner{}, nil, nil, nil, nil, slog.Default())

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", uuid.New().String())
	h.Enrich(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestEnrich_Success(t *testing.T) {
	enricher := &mockEnricher{}
	h := NewItemHandler(&mockItemMedia{}, &mockItemWatch{}, &mockSessionCleaner{}, enricher, nil, nil, nil, slog.Default())

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", uuid.New().String())
	h.Enrich(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
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
