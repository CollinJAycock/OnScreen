package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
)

// ── mock hub DB ─────────────────────────────────────────────────────────────

type mockHubDB struct {
	cwRows []gen.ListContinueWatchingRow
	cwErr  error

	raRows []gen.ListRecentlyAddedRow
	raErr  error

	trRows []gen.ListTrendingRow
	trErr  error
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

func (m *mockHubDB) ListTrending(_ context.Context, _ gen.ListTrendingParams) ([]gen.ListTrendingRow, error) {
	if m.trErr != nil {
		return nil, m.trErr
	}
	return m.trRows, nil
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

// TestHub_Get_Trending covers the v2.1 trending row — every-user-same
// "what others are watching" surface aggregated from watch_events. The
// handler's job is to (1) cap output at 12, (2) filter by library
// access, (3) tolerate a ListTrending error without nuking the rest of
// the hub.
func TestHub_Get_Trending(t *testing.T) {
	t.Run("populates trending row from ListTrending", func(t *testing.T) {
		year := int32(1999)
		db := &mockHubDB{
			trRows: []gen.ListTrendingRow{
				{ID: uuid.New(), LibraryID: uuid.New(), Title: "The Matrix", Type: "movie", Year: &year, UpdatedAt: pgtype.Timestamptz{Valid: false}},
				{ID: uuid.New(), LibraryID: uuid.New(), Title: "Fight Club", Type: "movie", UpdatedAt: pgtype.Timestamptz{Valid: false}},
			},
		}
		h := newHubHandler(db)
		rec := httptest.NewRecorder()
		req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
		h.Get(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: got %d", rec.Code)
		}
		var resp struct {
			Data HubResponse `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Data.Trending) != 2 {
			t.Errorf("trending: got %d, want 2", len(resp.Data.Trending))
		}
	})

	t.Run("caps trending at 12 even when ListTrending returns more", func(t *testing.T) {
		var rows []gen.ListTrendingRow
		for i := 0; i < 30; i++ {
			rows = append(rows, gen.ListTrendingRow{
				ID:        uuid.New(),
				LibraryID: uuid.New(),
				Title:     "X",
				Type:      "movie",
				UpdatedAt: pgtype.Timestamptz{Valid: false},
			})
		}
		db := &mockHubDB{trRows: rows}
		h := newHubHandler(db)
		rec := httptest.NewRecorder()
		req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
		h.Get(rec, req)
		var resp struct {
			Data HubResponse `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Data.Trending) != 12 {
			t.Errorf("trending cap: got %d, want 12", len(resp.Data.Trending))
		}
	})

	t.Run("ListTrending error returns empty trending without nuking the hub", func(t *testing.T) {
		db := &mockHubDB{
			cwRows: []gen.ListContinueWatchingRow{{ID: uuid.New(), LibraryID: uuid.New(), Title: "Inception", Type: "movie", UpdatedAt: pgtype.Timestamptz{Valid: false}}},
			trErr:  errors.New("trending query failed"),
		}
		h := newHubHandler(db)
		rec := httptest.NewRecorder()
		req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
		h.Get(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status: got %d", rec.Code)
		}
		var resp struct {
			Data HubResponse `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Data.Trending) != 0 {
			t.Errorf("trending should be empty on error, got %d", len(resp.Data.Trending))
		}
		if len(resp.Data.ContinueWatching) != 1 {
			t.Errorf("continue_watching should still populate when trending errors, got %d", len(resp.Data.ContinueWatching))
		}
	})

	t.Run("library access filters out non-allowed trending items", func(t *testing.T) {
		allowedLib := uuid.New()
		blockedLib := uuid.New()
		db := &mockHubDB{
			trRows: []gen.ListTrendingRow{
				{ID: uuid.New(), LibraryID: allowedLib, Title: "Allowed", Type: "movie", UpdatedAt: pgtype.Timestamptz{Valid: false}},
				{ID: uuid.New(), LibraryID: blockedLib, Title: "Blocked", Type: "movie", UpdatedAt: pgtype.Timestamptz{Valid: false}},
				{ID: uuid.New(), LibraryID: allowedLib, Title: "Allowed 2", Type: "movie", UpdatedAt: pgtype.Timestamptz{Valid: false}},
			},
		}
		h := newHubHandler(db).WithLibraryAccess(&stubLibraryAccessChecker{
			allowed: map[uuid.UUID]struct{}{allowedLib: {}},
		})
		rec := httptest.NewRecorder()
		req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
		h.Get(rec, req)
		var resp struct {
			Data HubResponse `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Data.Trending) != 2 {
			t.Errorf("trending after lib filter: got %d, want 2 (Blocked dropped)", len(resp.Data.Trending))
		}
		for _, it := range resp.Data.Trending {
			if it.Title == "Blocked" {
				t.Errorf("Blocked item leaked through library access filter")
			}
		}
	})
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

// ── Per-library recently added ──────────────────────────────────────────────

type stubHubLibLister struct{ libs []library.Library }

func (s *stubHubLibLister) List(_ context.Context) ([]library.Library, error) {
	return s.libs, nil
}

// perLibHubDB returns rows keyed by the LibraryID param so we can
// assert the handler is querying per-library, without relying on
// narg behaviour in sqlc-generated code.
type perLibHubDB struct {
	mockHubDB
	byLib map[uuid.UUID][]gen.ListRecentlyAddedRow
}

func (d *perLibHubDB) ListRecentlyAdded(_ context.Context, arg gen.ListRecentlyAddedParams) ([]gen.ListRecentlyAddedRow, error) {
	if !arg.LibraryID.Valid {
		return nil, nil
	}
	return d.byLib[uuid.UUID(arg.LibraryID.Bytes)], nil
}

func TestHub_Get_PerLibraryRecentlyAdded(t *testing.T) {
	moviesID := uuid.New()
	musicID := uuid.New()
	dvrID := uuid.New()
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	db := &perLibHubDB{
		byLib: map[uuid.UUID][]gen.ListRecentlyAddedRow{
			moviesID: {{ID: uuid.New(), LibraryID: moviesID, Title: "The Matrix", Type: "movie"}},
			musicID:  {{ID: uuid.New(), LibraryID: musicID, Title: "Dark Side of the Moon", Type: "album"}},
			// DVR rows are returned by the DB but the handler must skip
			// the library entirely because type='dvr' has no visible
			// recently-added items.
			dvrID: {{ID: uuid.New(), LibraryID: dvrID, Title: "Nightly News", Type: "movie"}},
		},
	}
	libs := &stubHubLibLister{libs: []library.Library{
		{ID: musicID, Name: "Music", Type: "music", CreatedAt: later},
		{ID: moviesID, Name: "Movies", Type: "movie", CreatedAt: earlier},
		{ID: dvrID, Name: "Recordings", Type: "dvr", CreatedAt: earlier},
	}}
	h := NewHubHandler(db, slog.Default()).WithLibraries(libs)

	rec := httptest.NewRecorder()
	req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var resp struct {
		Data HubResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	if len(resp.Data.ByLibrary) != 2 {
		t.Fatalf("by_library rows: got %d, want 2 (dvr must be skipped)", len(resp.Data.ByLibrary))
	}
	// Stable order = library creation time ascending: Movies before Music.
	if resp.Data.ByLibrary[0].LibraryName != "Movies" {
		t.Errorf("row[0]: got %q, want Movies", resp.Data.ByLibrary[0].LibraryName)
	}
	if resp.Data.ByLibrary[1].LibraryName != "Music" {
		t.Errorf("row[1]: got %q, want Music", resp.Data.ByLibrary[1].LibraryName)
	}
	if resp.Data.ByLibrary[0].Items[0].Title != "The Matrix" {
		t.Errorf("movies item: got %q", resp.Data.ByLibrary[0].Items[0].Title)
	}
	if resp.Data.ByLibrary[1].Items[0].Title != "Dark Side of the Moon" {
		t.Errorf("music item: got %q", resp.Data.ByLibrary[1].Items[0].Title)
	}
}

// TestHub_Get_PerLibrary_HonoursLibraryACL verifies non-admin users
// only see rows for libraries they've been granted access to.
func TestHub_Get_PerLibrary_HonoursLibraryACL(t *testing.T) {
	allowedID := uuid.New()
	forbiddenID := uuid.New()
	db := &perLibHubDB{
		byLib: map[uuid.UUID][]gen.ListRecentlyAddedRow{
			allowedID:   {{ID: uuid.New(), LibraryID: allowedID, Title: "Public", Type: "movie"}},
			forbiddenID: {{ID: uuid.New(), LibraryID: forbiddenID, Title: "Private", Type: "movie"}},
		},
	}
	libs := &stubHubLibLister{libs: []library.Library{
		{ID: allowedID, Name: "Allowed", Type: "movie"},
		{ID: forbiddenID, Name: "Forbidden", Type: "movie"},
	}}
	access := &stubLibraryAccessChecker{allowed: map[uuid.UUID]struct{}{allowedID: {}}}
	h := NewHubHandler(db, slog.Default()).
		WithLibraries(libs).
		WithLibraryAccess(access)

	rec := httptest.NewRecorder()
	req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
	h.Get(rec, req)

	var resp struct {
		Data HubResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data.ByLibrary) != 1 {
		t.Fatalf("by_library: got %d, want 1", len(resp.Data.ByLibrary))
	}
	if resp.Data.ByLibrary[0].LibraryName != "Allowed" {
		t.Errorf("got %q, want Allowed", resp.Data.ByLibrary[0].LibraryName)
	}
}

type stubLibraryAccessChecker struct {
	allowed map[uuid.UUID]struct{}
}

func (s *stubLibraryAccessChecker) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, isAdmin bool) (map[uuid.UUID]struct{}, error) {
	if isAdmin {
		return nil, nil
	}
	return s.allowed, nil
}

func (s *stubLibraryAccessChecker) CanAccessLibrary(_ context.Context, _ uuid.UUID, libID uuid.UUID, isAdmin bool) (bool, error) {
	if isAdmin {
		return true, nil
	}
	_, ok := s.allowed[libID]
	return ok, nil
}
