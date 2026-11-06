package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── mocks ────────────────────────────────────────────────────────────────────

type mockBulkAdminDB struct {
	rows             []gen.ListUnmatchedTopLevelItemsRow
	listErr          error
	missingArtRows   []gen.ListMediaItemsMissingArtRow
	missingArtErr    error
	missingArtLimits []int32 // captures every limit the handler asked for
}

func (m *mockBulkAdminDB) ListUnmatchedTopLevelItems(_ context.Context, _ gen.ListUnmatchedTopLevelItemsParams) ([]gen.ListUnmatchedTopLevelItemsRow, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.rows, nil
}

func (m *mockBulkAdminDB) ListMediaItemsMissingArt(_ context.Context, limit int32) ([]gen.ListMediaItemsMissingArtRow, error) {
	m.missingArtLimits = append(m.missingArtLimits, limit)
	return m.missingArtRows, m.missingArtErr
}

func adminClaims(r *http.Request) *http.Request {
	claims := &auth.Claims{
		UserID:    uuid.New(),
		Username:  "admin",
		IsAdmin:   true,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	return r.WithContext(middleware.WithClaims(r.Context(), claims))
}

func nonAdminClaims(r *http.Request) *http.Request {
	claims := &auth.Claims{
		UserID:    uuid.New(),
		Username:  "user",
		IsAdmin:   false,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	return r.WithContext(middleware.WithClaims(r.Context(), claims))
}

// ── ListMissingArt ───────────────────────────────────────────────────────────

// TestListMissingArt_HappyPath round-trips a row through the handler and
// asserts the response shape the Set Poster tray UI consumes (id +
// library_id + type + title + optional year + optional tmdb_id).
func TestListMissingArt_HappyPath(t *testing.T) {
	year := int32(1999)
	tmdbID := int32(17621)
	row := gen.ListMediaItemsMissingArtRow{
		ID:        uuid.New(),
		LibraryID: uuid.New(),
		Type:      "movie",
		Title:     "Magical Legend of the Leprechauns",
		Year:      &year,
		TmdbID:    &tmdbID,
	}
	db := &mockBulkAdminDB{missingArtRows: []gen.ListMediaItemsMissingArtRow{row}}
	h := NewItemBulkAdminHandler(db, slog.Default())

	rec := httptest.NewRecorder()
	req := adminClaims(httptest.NewRequest("GET", "/api/v1/admin/items/missing-art", nil))
	h.ListMissingArt(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var resp struct {
		Data missingArtListResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Total != 1 {
		t.Errorf("total: got %d, want 1", resp.Data.Total)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("items: got %d, want 1", len(resp.Data.Items))
	}
	got := resp.Data.Items[0]
	if got.ID != row.ID.String() || got.Title != row.Title || got.Type != "movie" {
		t.Errorf("row: got %+v", got)
	}
	if got.Year == nil || *got.Year != 1999 {
		t.Errorf("year: got %v, want 1999", got.Year)
	}
	if got.TMDBID == nil || *got.TMDBID != 17621 {
		t.Errorf("tmdb_id: got %v, want 17621", got.TMDBID)
	}
}

// TestListMissingArt_NonAdmin_Forbidden mirrors every other admin
// handler's auth check. The Set Poster tray is only meant for the
// operator running the server.
func TestListMissingArt_NonAdmin_Forbidden(t *testing.T) {
	h := NewItemBulkAdminHandler(&mockBulkAdminDB{}, slog.Default())

	rec := httptest.NewRecorder()
	req := nonAdminClaims(httptest.NewRequest("GET", "/api/v1/admin/items/missing-art", nil))
	h.ListMissingArt(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", rec.Code)
	}
}

// TestListMissingArt_EmptyResult returns an empty list with total=0,
// not null. The UI relies on iterating items.length === 0 for the
// "All items have posters" empty state — a null would crash it.
func TestListMissingArt_EmptyResult(t *testing.T) {
	db := &mockBulkAdminDB{missingArtRows: nil}
	h := NewItemBulkAdminHandler(db, slog.Default())

	rec := httptest.NewRecorder()
	req := adminClaims(httptest.NewRequest("GET", "/api/v1/admin/items/missing-art", nil))
	h.ListMissingArt(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("body should contain empty array (not null) for the UI iterator: %s", rec.Body.String())
	}
}
