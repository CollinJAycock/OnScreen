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

func (m *mockItemMedia) GetPhotoMetadata(_ context.Context, _ uuid.UUID) (*media.PhotoMetadata, error) {
	return nil, media.ErrNotFound
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
