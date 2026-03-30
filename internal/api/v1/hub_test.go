package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── mock hub DB ─────────────────────────────────────────────────────────────

type mockHubDB struct {
	cwRows []gen.ListContinueWatchingRow
	cwErr  error

	raRows []gen.ListRecentlyAddedRow
	raErr  error
}

func (m *mockHubDB) ListContinueWatching(_ context.Context, _ gen.ListContinueWatchingParams) ([]gen.ListContinueWatchingRow, error) {
	if m.cwErr != nil {
		return nil, m.cwErr
	}
	return m.cwRows, nil
}

func (m *mockHubDB) ListRecentlyAdded(_ context.Context, _ gen.ListRecentlyAddedParams) ([]gen.ListRecentlyAddedRow, error) {
	if m.raErr != nil {
		return nil, m.raErr
	}
	return m.raRows, nil
}

func newHubHandler(db *mockHubDB) *HubHandler {
	return NewHubHandler(db, slog.Default())
}

func hubAuthedRequest(r *http.Request) *http.Request {
	ctx := middleware.WithClaims(r.Context(), &auth.Claims{
		UserID:   uuid.New(),
		Username: "testuser",
	})
	return r.WithContext(ctx)
}

// ── Get tests ───────────────────────────────────────────────────────────────

func TestHub_Get_Success(t *testing.T) {
	year := int32(2024)
	dur := int64(7200000)
	db := &mockHubDB{
		cwRows: []gen.ListContinueWatchingRow{
			{
				ID:        uuid.New(),
				LibraryID: uuid.New(),
				Title:     "Inception",
				Type:      "movie",
				Year:      &year,
				UpdatedAt: pgtype.Timestamptz{Valid: false},
			},
		},
		raRows: []gen.ListRecentlyAddedRow{
			{
				ID:         uuid.New(),
				LibraryID:  uuid.New(),
				Title:      "The Matrix",
				Type:       "movie",
				Year:       &year,
				DurationMs: &dur,
				UpdatedAt:  pgtype.Timestamptz{Valid: false},
			},
		},
	}
	h := newHubHandler(db)

	rec := httptest.NewRecorder()
	req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data HubResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data.ContinueWatching) != 1 {
		t.Errorf("continue_watching: got %d, want 1", len(resp.Data.ContinueWatching))
	}
	if len(resp.Data.RecentlyAdded) != 1 {
		t.Errorf("recently_added: got %d, want 1", len(resp.Data.RecentlyAdded))
	}
	if resp.Data.ContinueWatching[0].Title != "Inception" {
		t.Errorf("cw title: got %q, want %q", resp.Data.ContinueWatching[0].Title, "Inception")
	}
	if resp.Data.RecentlyAdded[0].Title != "The Matrix" {
		t.Errorf("ra title: got %q, want %q", resp.Data.RecentlyAdded[0].Title, "The Matrix")
	}
}

func TestHub_Get_EmptySections(t *testing.T) {
	db := &mockHubDB{
		cwRows: []gen.ListContinueWatchingRow{},
		raRows: []gen.ListRecentlyAddedRow{},
	}
	h := newHubHandler(db)

	rec := httptest.NewRecorder()
	req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data HubResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data.ContinueWatching) != 0 {
		t.Errorf("continue_watching: got %d, want 0", len(resp.Data.ContinueWatching))
	}
	if len(resp.Data.RecentlyAdded) != 0 {
		t.Errorf("recently_added: got %d, want 0", len(resp.Data.RecentlyAdded))
	}
}

func TestHub_Get_NoAuth(t *testing.T) {
	h := newHubHandler(&mockHubDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/hub", nil)
	h.Get(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHub_Get_CWError_StillReturnsRA(t *testing.T) {
	year := int32(2024)
	db := &mockHubDB{
		cwErr: errors.New("continue watching query failed"),
		raRows: []gen.ListRecentlyAddedRow{
			{
				ID:        uuid.New(),
				LibraryID: uuid.New(),
				Title:     "The Matrix",
				Type:      "movie",
				Year:      &year,
				UpdatedAt: pgtype.Timestamptz{Valid: false},
			},
		},
	}
	h := newHubHandler(db)

	rec := httptest.NewRecorder()
	req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
	h.Get(rec, req)

	// Hub handler logs errors but does not fail — still returns 200.
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data HubResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data.ContinueWatching) != 0 {
		t.Errorf("continue_watching: got %d, want 0 (error branch)", len(resp.Data.ContinueWatching))
	}
	if len(resp.Data.RecentlyAdded) != 1 {
		t.Errorf("recently_added: got %d, want 1", len(resp.Data.RecentlyAdded))
	}
}

func TestHub_Get_RAError_StillReturnsCW(t *testing.T) {
	db := &mockHubDB{
		cwRows: []gen.ListContinueWatchingRow{
			{
				ID:        uuid.New(),
				LibraryID: uuid.New(),
				Title:     "Inception",
				Type:      "movie",
				UpdatedAt: pgtype.Timestamptz{Valid: false},
			},
		},
		raErr: errors.New("recently added query failed"),
	}
	h := newHubHandler(db)

	rec := httptest.NewRecorder()
	req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data HubResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data.ContinueWatching) != 1 {
		t.Errorf("continue_watching: got %d, want 1", len(resp.Data.ContinueWatching))
	}
	if len(resp.Data.RecentlyAdded) != 0 {
		t.Errorf("recently_added: got %d, want 0 (error branch)", len(resp.Data.RecentlyAdded))
	}
}

func TestHub_Get_BothErrors_ReturnsEmptyHub(t *testing.T) {
	db := &mockHubDB{
		cwErr: errors.New("cw error"),
		raErr: errors.New("ra error"),
	}
	h := newHubHandler(db)

	rec := httptest.NewRecorder()
	req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data HubResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data.ContinueWatching) != 0 {
		t.Errorf("continue_watching: got %d, want 0", len(resp.Data.ContinueWatching))
	}
	if len(resp.Data.RecentlyAdded) != 0 {
		t.Errorf("recently_added: got %d, want 0", len(resp.Data.RecentlyAdded))
	}
}
