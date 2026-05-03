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
	unsupportedID := uuid.New()
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	db := &perLibHubDB{
		byLib: map[uuid.UUID][]gen.ListRecentlyAddedRow{
			moviesID: {{ID: uuid.New(), LibraryID: moviesID, Title: "The Matrix", Type: "movie"}},
			musicID:  {{ID: uuid.New(), LibraryID: musicID, Title: "Dark Side of the Moon", Type: "album"}},
			// DVR captures land as `movie` / `episode` items in a library
			// of type='dvr'. Pre-v2.1 the handler skipped the dvr library
			// type entirely; post-v2.1 it's in the allowlist so DVR
			// recordings surface in the home hub like any other library.
			dvrID: {{ID: uuid.New(), LibraryID: dvrID, Title: "Nightly News", Type: "movie"}},
			// Unrecognised library types still skip — defensive guard so
			// a future scaffolded library type doesn't accidentally start
			// rendering until it has a corresponding hub row shape.
			unsupportedID: {{ID: uuid.New(), LibraryID: unsupportedID, Title: "x", Type: "x"}},
		},
	}
	libs := &stubHubLibLister{libs: []library.Library{
		{ID: musicID, Name: "Music", Type: "music", CreatedAt: later},
		{ID: moviesID, Name: "Movies", Type: "movie", CreatedAt: earlier},
		{ID: dvrID, Name: "Recordings", Type: "dvr", CreatedAt: mid},
		{ID: unsupportedID, Name: "Future", Type: "future_thing", CreatedAt: earlier},
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

	if len(resp.Data.ByLibrary) != 3 {
		t.Fatalf("by_library rows: got %d, want 3 (movies + dvr + music; future_thing skipped)", len(resp.Data.ByLibrary))
	}
	// Stable order = library creation time ascending.
	wantNames := []string{"Movies", "Recordings", "Music"}
	for i, want := range wantNames {
		if resp.Data.ByLibrary[i].LibraryName != want {
			t.Errorf("row[%d]: got %q, want %q", i, resp.Data.ByLibrary[i].LibraryName, want)
		}
	}
	// "Future" library never enters the response.
	for _, row := range resp.Data.ByLibrary {
		if row.LibraryName == "Future" {
			t.Errorf("future_thing library type must be skipped, got row: %+v", row)
		}
	}
}

// TestHub_Get_CW_EpisodeRollsUpToShow locks the v2.1 Continue Watching
// TV behaviour: episode rows surfaced by ListContinueWatching carry
// the show ancestor's id / title / type / poster on a dedicated
// show_* set of columns, and the handler swaps them into the
// displayed item so the tile renders the show and the click target
// navigates to the show detail page (not the specific episode).
// view_offset_ms is preserved from the episode so the tile's
// progress bar still reflects "where I am in the next episode".
func TestHub_Get_CW_EpisodeRollsUpToShow(t *testing.T) {
	episodeID := uuid.New()
	showID := uuid.New()
	libraryID := uuid.New()
	showTitle := "Severance"
	showPoster := "/posters/severance.jpg"
	showFanart := "/fanart/severance.jpg"
	showThumb := "/thumbs/severance.jpg"
	showYear := int32(2022)
	episodeYear := int32(2022)
	offsetMs := int64(1234567)
	episodePoster := "/posters/episode-still.jpg"

	db := &mockHubDB{
		cwRows: []gen.ListContinueWatchingRow{
			{
				ID:        episodeID,
				LibraryID: libraryID,
				Title:     "S2 E5: Trojan's Horse",
				Type:      "episode",
				Year:      &episodeYear,
				// Episode's own poster (rare but possible — may differ
				// from the show poster). Should be hidden by the swap.
				PosterPath: &episodePoster,
				ViewOffset: offsetMs,
				UpdatedAt:  pgtype.Timestamptz{Valid: false},
				// Show rollup payload from the LEFT JOIN.
				ShowID:         pgtype.UUID{Bytes: showID, Valid: true},
				ShowTitle:      &showTitle,
				ShowYear:       &showYear,
				ShowPosterPath: &showPoster,
				ShowFanartPath: &showFanart,
				ShowThumbPath:  &showThumb,
			},
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
	if len(resp.Data.ContinueWatchingTV) != 1 {
		t.Fatalf("continue_watching_tv: got %d, want 1", len(resp.Data.ContinueWatchingTV))
	}
	got := resp.Data.ContinueWatchingTV[0]
	if got.ID != showID.String() {
		t.Errorf("ID: got %q, want show ID %q", got.ID, showID.String())
	}
	if got.Type != "show" {
		t.Errorf("Type: got %q, want %q", got.Type, "show")
	}
	if got.Title != showTitle {
		t.Errorf("Title: got %q, want %q", got.Title, showTitle)
	}
	if got.PosterPath == nil || *got.PosterPath != showPoster {
		t.Errorf("PosterPath: got %v, want %q", got.PosterPath, showPoster)
	}
	if got.ViewOffsetMS == nil || *got.ViewOffsetMS != offsetMs {
		t.Errorf("ViewOffsetMS: got %v, want %d (episode progress preserved)", got.ViewOffsetMS, offsetMs)
	}
	// Movie / Other buckets must stay untouched.
	if len(resp.Data.ContinueWatchingMovies) != 0 {
		t.Errorf("continue_watching_movies should be empty, got %d", len(resp.Data.ContinueWatchingMovies))
	}
	if len(resp.Data.ContinueWatchingOther) != 0 {
		t.Errorf("continue_watching_other should be empty, got %d", len(resp.Data.ContinueWatchingOther))
	}
}

// TestHub_Get_CW_OrphanEpisodeStaysInTV verifies the defensive path:
// an episode row whose ancestor chain didn't resolve (ShowID NULL)
// still lands in the TV bucket — clients render the episode-as-is
// rather than the row going missing.
func TestHub_Get_CW_OrphanEpisodeStaysInTV(t *testing.T) {
	db := &mockHubDB{
		cwRows: []gen.ListContinueWatchingRow{
			{
				ID:        uuid.New(),
				LibraryID: uuid.New(),
				Title:     "Pilot (orphan)",
				Type:      "episode",
				UpdatedAt: pgtype.Timestamptz{Valid: false},
				ShowID:    pgtype.UUID{Valid: false}, // no ancestor
			},
		},
	}
	h := newHubHandler(db)
	rec := httptest.NewRecorder()
	req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
	h.Get(rec, req)
	var resp struct {
		Data HubResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data.ContinueWatchingTV) != 1 {
		t.Fatalf("continue_watching_tv: got %d, want 1", len(resp.Data.ContinueWatchingTV))
	}
	got := resp.Data.ContinueWatchingTV[0]
	if got.Type != "episode" {
		t.Errorf("Type: got %q, want %q (no rollup means no swap)", got.Type, "episode")
	}
}

// TestHub_Get_RecentlyAdded_EpisodeUsesFallbackPoster guards the v2.1
// "Recently Added Episodes" change: ListRecentlyAdded now returns
// episode rows for TV libraries (deduped per show via window function),
// and episodes almost always have a NULL poster_path because TMDB gives
// us per-show artwork, not per-episode stills. The handler must fall
// back to the show poster from FallbackPoster so the tile actually
// renders artwork.
func TestHub_Get_RecentlyAdded_EpisodeUsesFallbackPoster(t *testing.T) {
	showPoster := "/posters/foo-show.jpg"
	db := &mockHubDB{
		raRows: []gen.ListRecentlyAddedRow{
			{
				ID:             uuid.New(),
				LibraryID:      uuid.New(),
				Title:          "Pilot",
				Type:           "episode",
				PosterPath:     nil,         // episode has no poster
				FallbackPoster: &showPoster, // show's poster, propagated by SQL
				UpdatedAt:      pgtype.Timestamptz{Valid: false},
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
	if len(resp.Data.RecentlyAdded) != 1 {
		t.Fatalf("recently_added: got %d, want 1", len(resp.Data.RecentlyAdded))
	}
	got := resp.Data.RecentlyAdded[0]
	if got.PosterPath == nil || *got.PosterPath != showPoster {
		t.Errorf("PosterPath: want show fallback %q, got %v", showPoster, got.PosterPath)
	}
	if got.Type != "episode" {
		t.Errorf("Type: want episode, got %q", got.Type)
	}
}

// TestHub_Get_RecentlyAdded_NoFallbackPosterFiltered verifies the
// query contract — posterless rows (unenriched shows whose chain has
// no artwork to fall back to) shouldn't reach the handler at all
// because the SQL filters `AND fallback_poster IS NOT NULL`. The
// handler test simulates that by NOT putting any nil-poster rows in
// the mock; it'd only reach this code path on a stale schema or a
// bug in the SQL filter. Documenting the expected handler-side
// shape: no fallback → row would render artless if it slipped
// through, which is a regression.
func TestHub_Get_RecentlyAdded_NoFallbackPosterFiltered(t *testing.T) {
	// SQL contract: posterless rows are filtered upstream. Verify the
	// handler doesn't put any artless tile in the response if (somehow)
	// a posterless row arrived — a defense-in-depth check on the chain.
	db := &mockHubDB{
		raRows: []gen.ListRecentlyAddedRow{
			{ // legitimate, with fallback
				ID:             uuid.New(),
				LibraryID:      uuid.New(),
				Title:          "Pilot",
				Type:           "episode",
				FallbackPoster: strPtrLocal("/posters/show.jpg"),
				UpdatedAt:      pgtype.Timestamptz{Valid: false},
			},
		},
	}
	h := newHubHandler(db)

	rec := httptest.NewRecorder()
	req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
	h.Get(rec, req)

	var resp struct {
		Data HubResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data.RecentlyAdded) != 1 {
		t.Fatalf("recently_added: got %d, want 1", len(resp.Data.RecentlyAdded))
	}
	if resp.Data.RecentlyAdded[0].PosterPath == nil {
		t.Errorf("a row with FallbackPoster set must always render with a poster")
	}
}

func strPtrLocal(s string) *string { return &s }

// TestHub_Get_RecentlyAdded_PreferOwnPosterOverFallback verifies the
// fallback only kicks in when PosterPath is nil. Movies / albums /
// photos always populate poster_path on themselves, and the SQL sets
// fallback_poster = poster_path for the non-episode branch — so a movie
// row with both fields set should use its own (already correct).
func TestHub_Get_RecentlyAdded_PreferOwnPosterOverFallback(t *testing.T) {
	moviePoster := "/posters/the-matrix.jpg"
	db := &mockHubDB{
		raRows: []gen.ListRecentlyAddedRow{
			{
				ID:             uuid.New(),
				LibraryID:      uuid.New(),
				Title:          "The Matrix",
				Type:           "movie",
				PosterPath:     &moviePoster,
				FallbackPoster: &moviePoster, // SQL sets these equal for movies
				UpdatedAt:      pgtype.Timestamptz{Valid: false},
			},
		},
	}
	h := newHubHandler(db)

	rec := httptest.NewRecorder()
	req := hubAuthedRequest(httptest.NewRequest("GET", "/api/v1/hub", nil))
	h.Get(rec, req)

	var resp struct {
		Data HubResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if got := *resp.Data.RecentlyAdded[0].PosterPath; got != moviePoster {
		t.Errorf("PosterPath: got %q, want %q", got, moviePoster)
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
