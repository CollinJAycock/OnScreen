package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// ── mock library service ─────────────────────────────────────────────────────

type mockLibraryService struct {
	libs       []library.Library
	lib        *library.Library
	listErr    error
	getErr     error
	createErr  error
	updateErr  error
	deleteErr  error
	scanErr    error
	created    *library.Library
}

func (m *mockLibraryService) List(_ context.Context) ([]library.Library, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.libs, nil
}
func (m *mockLibraryService) Get(_ context.Context, _ uuid.UUID) (*library.Library, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.lib, nil
}
func (m *mockLibraryService) Create(_ context.Context, p library.CreateLibraryParams) (*library.Library, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	if m.created != nil {
		return m.created, nil
	}
	lib := &library.Library{
		ID:        uuid.New(),
		Name:      p.Name,
		Type:      p.Type,
		Paths:     p.Paths,
		Agent:     p.Agent,
		Lang:      p.Lang,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return lib, nil
}
func (m *mockLibraryService) Update(_ context.Context, p library.UpdateLibraryParams) (*library.Library, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	lib := &library.Library{
		ID:        p.ID,
		Name:      p.Name,
		Paths:     p.Paths,
		Agent:     p.Agent,
		Lang:      p.Lang,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return lib, nil
}
func (m *mockLibraryService) Delete(_ context.Context, _ uuid.UUID) error {
	return m.deleteErr
}
func (m *mockLibraryService) EnqueueScan(_ context.Context, _ uuid.UUID) error {
	return m.scanErr
}

// ── mock media item lister ───────────────────────────────────────────────────

type mockMediaLister struct {
	items    []media.Item
	listErr  error
	count    int64
	countErr error
}

func (m *mockMediaLister) ListItems(_ context.Context, _ uuid.UUID, _ string, _, _ int32) ([]media.Item, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.items, nil
}
func (m *mockMediaLister) CountItems(_ context.Context, _ uuid.UUID, _ string) (int64, error) {
	if m.countErr != nil {
		return 0, m.countErr
	}
	return m.count, nil
}

func newLibHandler(svc *mockLibraryService) *LibraryHandler {
	return NewLibraryHandler(svc, slog.Default())
}

// ── List ─────────────────────────────────────────────────────────────────────

func TestLibrary_List_Success(t *testing.T) {
	svc := &mockLibraryService{
		libs: []library.Library{
			{ID: uuid.New(), Name: "Movies", Type: "movie", Paths: []string{"/movies"}, Agent: "tmdb", Lang: "en", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		},
	}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/libraries", nil)
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestLibrary_List_Error(t *testing.T) {
	svc := &mockLibraryService{listErr: errors.New("db down")}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// ── Get ──────────────────────────────────────────────────────────────────────

func TestLibrary_Get_Success(t *testing.T) {
	id := uuid.New()
	svc := &mockLibraryService{
		lib: &library.Library{ID: id, Name: "Movies", Type: "movie", Agent: "tmdb", Lang: "en", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", id.String())
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestLibrary_Get_NotFound(t *testing.T) {
	svc := &mockLibraryService{getErr: library.ErrNotFound}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	h.Get(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestLibrary_Get_InvalidID(t *testing.T) {
	h := newLibHandler(&mockLibraryService{})

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", "bad")
	h.Get(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// ── Create ───────────────────────────────────────────────────────────────────

func TestLibrary_Create_Success(t *testing.T) {
	svc := &mockLibraryService{}
	h := newLibHandler(svc)

	body := `{"name":"Movies","type":"movie","scan_paths":["/media/movies"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusCreated)
	}
}

func TestLibrary_Create_InvalidBody(t *testing.T) {
	h := newLibHandler(&mockLibraryService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader("not json"))
	h.Create(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestLibrary_Create_ValidationError(t *testing.T) {
	svc := &mockLibraryService{
		createErr: &library.ValidationError{Field: "name", Message: "required"},
	}
	h := newLibHandler(svc)

	body := `{"name":"","type":"movie","scan_paths":["/media"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	h.Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestLibrary_Create_Defaults(t *testing.T) {
	// When agent, language, and scan_interval are not specified, defaults should apply.
	svc := &mockLibraryService{}
	h := newLibHandler(svc)

	body := `{"name":"Shows","type":"show","scan_paths":["/shows"]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusCreated)
	}
}

// ── Update ───────────────────────────────────────────────────────────────────

func TestLibrary_Update_Success(t *testing.T) {
	id := uuid.New()
	svc := &mockLibraryService{}
	h := newLibHandler(svc)

	body := `{"name":"Updated Movies","scan_paths":["/new/path"]}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", id.String())
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestLibrary_Update_NotFound(t *testing.T) {
	svc := &mockLibraryService{updateErr: library.ErrNotFound}
	h := newLibHandler(svc)

	body := `{"name":"X"}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", uuid.New().String())
	h.Update(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestLibrary_Update_ScanIntervalMinutes(t *testing.T) {
	svc := &mockLibraryService{}
	h := newLibHandler(svc)

	body := `{"name":"Movies","scan_interval_minutes":60}`
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", uuid.New().String())
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

// ── Delete ───────────────────────────────────────────────────────────────────

func TestLibrary_Delete_Success(t *testing.T) {
	svc := &mockLibraryService{}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("DELETE", "/", nil), "id", uuid.New().String())
	h.Delete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestLibrary_Delete_NotFound(t *testing.T) {
	svc := &mockLibraryService{deleteErr: library.ErrNotFound}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("DELETE", "/", nil), "id", uuid.New().String())
	h.Delete(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// ── Refresh (scan) ───────────────────────────────────────────────────────────

func TestLibrary_Refresh_Success(t *testing.T) {
	svc := &mockLibraryService{}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", uuid.New().String())
	h.Refresh(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestLibrary_Refresh_NotFound(t *testing.T) {
	svc := &mockLibraryService{scanErr: library.ErrNotFound}
	h := newLibHandler(svc)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("POST", "/", nil), "id", uuid.New().String())
	h.Refresh(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// ── Items ────────────────────────────────────────────────────────────────────

func TestLibrary_Items_NoMediaService(t *testing.T) {
	svc := &mockLibraryService{}
	h := newLibHandler(svc) // no WithMedia

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	h.Items(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotImplemented)
	}
}

func TestLibrary_Items_Success(t *testing.T) {
	libID := uuid.New()
	svc := &mockLibraryService{
		lib: &library.Library{ID: libID, Name: "Movies", Type: "movie", Agent: "tmdb", Lang: "en", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	ml := &mockMediaLister{
		items: []media.Item{{ID: uuid.New(), Title: "Test", Type: "movie"}},
		count: 1,
	}
	h := newLibHandler(svc)
	h.WithMedia(ml)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/?limit=10&offset=0", nil), "id", libID.String())
	h.Items(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data []json.RawMessage `json:"data"`
		Meta map[string]any    `json:"meta"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data) != 1 {
		t.Errorf("data length: got %d, want 1", len(resp.Data))
	}
}

func TestLibrary_Items_LibraryNotFound(t *testing.T) {
	svc := &mockLibraryService{getErr: library.ErrNotFound}
	ml := &mockMediaLister{}
	h := newLibHandler(svc)
	h.WithMedia(ml)

	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", uuid.New().String())
	h.Items(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// ── toLibraryResponse ────────────────────────────────────────────────────────

func TestToLibraryResponse_NilPaths(t *testing.T) {
	lib := &library.Library{
		ID:        uuid.New(),
		Name:      "Test",
		Paths:     nil,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	resp := toLibraryResponse(lib)
	if resp.ScanPaths == nil {
		t.Error("ScanPaths should not be nil (should be empty slice)")
	}
}

func TestToLibraryResponse_WithScanInterval(t *testing.T) {
	dur := 2 * time.Hour
	lib := &library.Library{
		ID:           uuid.New(),
		Name:         "Test",
		ScanInterval: &dur,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	resp := toLibraryResponse(lib)
	if resp.ScanIntervalMinutes == nil {
		t.Fatal("ScanIntervalMinutes should not be nil")
	}
	if *resp.ScanIntervalMinutes != 120 {
		t.Errorf("ScanIntervalMinutes: got %d, want 120", *resp.ScanIntervalMinutes)
	}
}
