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

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── mock search DB ──────────────────────────────────────────────────────────

type mockSearchDB struct {
	globalRows []gen.SearchMediaItemsGlobalRow
	globalErr  error
	// globalCalls records each global query's arguments.
	globalCalls []gen.SearchMediaItemsGlobalParams

	scopedRows []gen.SearchMediaItemsRow
	scopedErr  error
}

func (m *mockSearchDB) SearchMediaItems(_ context.Context, _ gen.SearchMediaItemsParams) ([]gen.SearchMediaItemsRow, error) {
	if m.scopedErr != nil {
		return nil, m.scopedErr
	}
	return m.scopedRows, nil
}

func (m *mockSearchDB) SearchMediaItemsGlobal(_ context.Context, arg gen.SearchMediaItemsGlobalParams) ([]gen.SearchMediaItemsGlobalRow, error) {
	m.globalCalls = append(m.globalCalls, arg)
	if m.globalErr != nil {
		return nil, m.globalErr
	}
	return m.globalRows, nil
}

func newSearchHandler(db *mockSearchDB) *SearchHandler {
	return NewSearchHandler(db, slog.Default())
}

// ── Search tests ────────────────────────────────────────────────────────────

func TestSearch_GlobalSuccess(t *testing.T) {
	id1 := uuid.New()
	libID := uuid.New()
	year := int32(2024)
	db := &mockSearchDB{
		globalRows: []gen.SearchMediaItemsGlobalRow{
			{ID: id1, LibraryID: libID, Title: "Inception", Type: "movie", Year: &year},
		},
	}
	h := newSearchHandler(db)

	rec := httptest.NewRecorder()
	req := withClaims(httptest.NewRequest("GET", "/api/v1/search?q=inception", nil))
	h.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data []SearchResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("results: got %d, want 1", len(resp.Data))
	}
	if resp.Data[0].Title != "Inception" {
		t.Errorf("title: got %q, want %q", resp.Data[0].Title, "Inception")
	}
	if resp.Data[0].ID != id1.String() {
		t.Errorf("id: got %q, want %q", resp.Data[0].ID, id1.String())
	}
}

func TestSearch_LibraryScoped(t *testing.T) {
	libID := uuid.New()
	db := &mockSearchDB{
		scopedRows: []gen.SearchMediaItemsRow{
			{ID: uuid.New(), LibraryID: libID, Title: "Friends", Type: "show"},
		},
	}
	h := newSearchHandler(db)

	rec := httptest.NewRecorder()
	req := withClaims(httptest.NewRequest("GET", "/api/v1/search?q=friends&library_id="+libID.String(), nil))
	h.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data []SearchResult `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data) != 1 {
		t.Fatalf("results: got %d, want 1", len(resp.Data))
	}
	if resp.Data[0].LibraryID != libID.String() {
		t.Errorf("library_id: got %q, want %q", resp.Data[0].LibraryID, libID.String())
	}
}

func TestSearch_EmptyQuery_Returns400(t *testing.T) {
	h := newSearchHandler(&mockSearchDB{})

	rec := httptest.NewRecorder()
	req := withClaims(httptest.NewRequest("GET", "/api/v1/search?q=", nil))
	h.Search(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSearch_NoResults_ReturnsEmptyArray(t *testing.T) {
	db := &mockSearchDB{
		globalRows: []gen.SearchMediaItemsGlobalRow{},
	}
	h := newSearchHandler(db)

	rec := httptest.NewRecorder()
	req := withClaims(httptest.NewRequest("GET", "/api/v1/search?q=nonexistent", nil))
	h.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data []SearchResult `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data) != 0 {
		t.Errorf("results: got %d, want 0", len(resp.Data))
	}
}

func TestSearch_InvalidLibraryID(t *testing.T) {
	h := newSearchHandler(&mockSearchDB{})

	rec := httptest.NewRecorder()
	req := withClaims(httptest.NewRequest("GET", "/api/v1/search?q=test&library_id=not-a-uuid", nil))
	h.Search(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSearch_DBError_Returns500(t *testing.T) {
	db := &mockSearchDB{
		globalErr: errors.New("connection refused"),
	}
	h := newSearchHandler(db)

	rec := httptest.NewRecorder()
	req := withClaims(httptest.NewRequest("GET", "/api/v1/search?q=test", nil))
	h.Search(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// A restricted user's grant set goes into the global query, so the LIMIT
// counts only rows they can see (filtered afterwards, the top rows across
// every library could all be hidden and a common word found nothing). An
// admin's query is unscoped; a user with no grants doesn't query at all.
func TestSearch_GlobalScopedInSQL(t *testing.T) {
	lib := uuid.New()
	grants := &stubLibraryAccessChecker{allowed: map[uuid.UUID]struct{}{lib: {}}}
	user := func(r *http.Request) *http.Request {
		return r.WithContext(middleware.WithClaims(r.Context(), &auth.Claims{UserID: uuid.New()}))
	}

	db := &mockSearchDB{globalRows: []gen.SearchMediaItemsGlobalRow{{ID: uuid.New(), LibraryID: lib, Title: "Night of the Living Dead", Type: "movie"}}}
	h := newSearchHandler(db).WithLibraryAccess(grants)
	rec := httptest.NewRecorder()
	h.Search(rec, user(httptest.NewRequest("GET", "/api/v1/search?q=night", nil)))
	if rec.Code != http.StatusOK || len(db.globalCalls) != 1 {
		t.Fatalf("status %d, calls %d", rec.Code, len(db.globalCalls))
	}
	if got := db.globalCalls[0].LibraryIds; len(got) != 1 || got[0] != lib {
		t.Errorf("library_ids = %v, want [%s]", got, lib)
	}
	if !strings.Contains(rec.Body.String(), "Night of the Living Dead") {
		t.Errorf("body = %s, want the granted match", rec.Body)
	}

	db = &mockSearchDB{}
	h = newSearchHandler(db).WithLibraryAccess(grants)
	h.Search(httptest.NewRecorder(), withClaims(httptest.NewRequest("GET", "/api/v1/search?q=night", nil)))
	if len(db.globalCalls) != 1 || db.globalCalls[0].LibraryIds != nil {
		t.Errorf("admin query = %+v, want library_ids NULL", db.globalCalls)
	}

	db = &mockSearchDB{}
	h = newSearchHandler(db).WithLibraryAccess(&stubLibraryAccessChecker{allowed: map[uuid.UUID]struct{}{}})
	rec = httptest.NewRecorder()
	h.Search(rec, user(httptest.NewRequest("GET", "/api/v1/search?q=night", nil)))
	if rec.Code != http.StatusOK || len(db.globalCalls) != 0 {
		t.Errorf("no grants: status %d, calls %d; want 200 without a query", rec.Code, len(db.globalCalls))
	}
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Errorf("no grants: body = %s, want an empty list", rec.Body)
	}
}

func TestSearch_CustomLimit(t *testing.T) {
	db := &mockSearchDB{
		globalRows: []gen.SearchMediaItemsGlobalRow{},
	}
	h := newSearchHandler(db)

	rec := httptest.NewRecorder()
	req := withClaims(httptest.NewRequest("GET", "/api/v1/search?q=test&limit=5", nil))
	h.Search(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}
