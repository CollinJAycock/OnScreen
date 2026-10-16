package v1

import (
	"context"
	"encoding/json"
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
)

type wlFakeDB struct {
	listArg   gen.ListWatchlistParams
	countArg  gen.CountWatchlistParams
	countsArg gen.CountWatchlistByStatusTypeParams
	rows      []gen.ListWatchlistRow
	total     int64
	counts    []gen.CountWatchlistByStatusTypeRow
	listCalls int
}

func (f *wlFakeDB) ListWatchlist(_ context.Context, arg gen.ListWatchlistParams) ([]gen.ListWatchlistRow, error) {
	f.listCalls++
	f.listArg = arg
	return f.rows, nil
}
func (f *wlFakeDB) CountWatchlist(_ context.Context, arg gen.CountWatchlistParams) (int64, error) {
	f.countArg = arg
	return f.total, nil
}
func (f *wlFakeDB) CountWatchlistByStatusType(_ context.Context, arg gen.CountWatchlistByStatusTypeParams) ([]gen.CountWatchlistByStatusTypeRow, error) {
	f.countsArg = arg
	return f.counts, nil
}

func wlGet(t *testing.T, h *WatchStatusHandler, fn func(*WatchStatusHandler) http.HandlerFunc, path string, c *auth.Claims) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if c != nil {
		req = req.WithContext(middleware.WithClaims(req.Context(), c))
	}
	rec := httptest.NewRecorder()
	fn(h)(rec, req)
	return rec
}

func wlList(h *WatchStatusHandler) http.HandlerFunc   { return h.List }
func wlCounts(h *WatchStatusHandler) http.HandlerFunc { return h.Counts }

// TestWatchlist_ScopeReachesSQL pins that the caller's own id, library grant
// set, rating ceiling and label restriction all reach both the rows query and
// the total — the SQL is what keeps hidden items out (covered against a real
// database by TestWatchlist_Integration).
func TestWatchlist_ScopeReachesSQL(t *testing.T) {
	granted := uuid.New()
	db := &wlFakeDB{total: 7}
	h := NewWatchStatusHandler(nil, slog.Default()).
		WithItemGate(wsFakeItems{}, wsFakeAccess{allowed: granted}).
		WithWatchlist(db)
	profile := &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG", BlockedLabels: []string{"horror"}}

	rec := wlGet(t, h, wlList, "/api/v1/watchlist?type=movie&sort=title&limit=10&offset=20", profile)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	a := db.listArg
	if a.UserID != profile.UserID {
		t.Error("list must be scoped to the caller's own id")
	}
	if len(a.LibraryIds) != 1 || a.LibraryIds[0] != granted {
		t.Errorf("library_ids = %v, want the grant set", a.LibraryIds)
	}
	if a.MaxRatingRank == nil {
		t.Error("rating ceiling must reach the query")
	}
	if len(a.BlockedLabels) != 1 || a.BlockedLabels[0] != "horror" {
		t.Errorf("blocked labels = %v", a.BlockedLabels)
	}
	if a.Status == nil || *a.Status != "plan_to_watch" {
		t.Errorf("default status = %v, want plan_to_watch", a.Status)
	}
	if a.ItemType == nil || *a.ItemType != "movie" || a.Sort != "title" || a.Lim != 10 || a.Off != 20 {
		t.Errorf("type/sort/paging = %v/%q/%d/%d", a.ItemType, a.Sort, a.Lim, a.Off)
	}
	c := db.countArg
	if c.UserID != a.UserID || c.MaxRatingRank == nil || len(c.LibraryIds) != 1 || len(c.BlockedLabels) != 1 || *c.Status != *a.Status || *c.ItemType != *a.ItemType {
		t.Errorf("count filters must mirror the list's: %+v", c)
	}
	var body struct {
		Data []WatchlistItem `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data == nil || body.Meta.Total != 7 {
		t.Errorf("want an empty array and total 7, got %v / %d", body.Data, body.Meta.Total)
	}

	// Counts share the same scope.
	if rec := wlGet(t, h, wlCounts, "/api/v1/watchlist/counts", profile); rec.Code != http.StatusOK {
		t.Fatalf("counts status %d", rec.Code)
	}
	if db.countsArg.UserID != profile.UserID || len(db.countsArg.LibraryIds) != 1 || db.countsArg.MaxRatingRank == nil || len(db.countsArg.BlockedLabels) != 1 {
		t.Errorf("counts scope = %+v", db.countsArg)
	}
}

func TestWatchlist_StatusAllAndDefaults(t *testing.T) {
	db := &wlFakeDB{}
	h := NewWatchStatusHandler(nil, slog.Default()).WithWatchlist(db)
	admin := &auth.Claims{UserID: uuid.New(), IsAdmin: true}
	if rec := wlGet(t, h, wlList, "/api/v1/watchlist?status=all", admin); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if db.listArg.Status != nil || db.listArg.ItemType != nil || db.listArg.Sort != "recent" {
		t.Errorf("all/no type/default sort: %+v", db.listArg)
	}
	if db.listArg.LibraryIds != nil || db.listArg.MaxRatingRank != nil {
		t.Error("an unrestricted admin gets no library or rating filter")
	}
}

func TestWatchlist_RejectsBadParams(t *testing.T) {
	db := &wlFakeDB{}
	h := NewWatchStatusHandler(nil, slog.Default()).WithWatchlist(db)
	c := &auth.Claims{UserID: uuid.New()}
	for _, q := range []string{"status=finished", "type=channel", "sort=rating"} {
		if rec := wlGet(t, h, wlList, "/api/v1/watchlist?"+q, c); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", q, rec.Code)
		}
	}
	if db.listCalls != 0 {
		t.Error("a rejected request must not reach the database")
	}
	if rec := wlGet(t, h, wlList, "/api/v1/watchlist", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims: got %d, want 401", rec.Code)
	}
	unwired := NewWatchStatusHandler(nil, slog.Default())
	if rec := wlGet(t, unwired, wlList, "/api/v1/watchlist", c); rec.Code != http.StatusNotFound {
		t.Errorf("listing not wired: got %d, want 404", rec.Code)
	}
}

func TestWatchlist_RowMapping(t *testing.T) {
	showPoster := "/p/show.jpg"
	show, season := uuid.New(), uuid.New()
	updated := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	it := watchlistItemFromRow(gen.ListWatchlistRow{
		ID: uuid.New(), Type: "episode", Title: "Ep",
		ParentID:              pgtype.UUID{Bytes: season, Valid: true},
		GrandparentID:         pgtype.UUID{Bytes: show, Valid: true},
		GrandparentPosterPath: &showPoster,
		Status:                "watching",
		WatchState:            "in_progress",
		StatusUpdatedAt:       pgtype.Timestamptz{Time: updated, Valid: true},
	})
	if it.PosterPath == nil || *it.PosterPath != showPoster {
		t.Errorf("poster fallback = %v", it.PosterPath)
	}
	if it.GrandparentID == nil || *it.GrandparentID != show.String() || *it.ParentID != season.String() {
		t.Error("ancestor ids not mapped")
	}
	if !it.StatusUpdatedAt.Equal(updated) || !it.StatusCreatedAt.IsZero() {
		t.Error("timestamps not mapped")
	}
}

func TestWatchlist_CountsShape(t *testing.T) {
	db := &wlFakeDB{counts: []gen.CountWatchlistByStatusTypeRow{
		{Status: "plan_to_watch", Type: "movie", Count: 2},
		{Status: "plan_to_watch", Type: "show", Count: 1},
		{Status: "completed", Type: "movie", Count: 4},
	}}
	h := NewWatchStatusHandler(nil, slog.Default()).WithWatchlist(db)
	rec := wlGet(t, h, wlCounts, "/api/v1/watchlist/counts", &auth.Claims{UserID: uuid.New(), IsAdmin: true})
	var body struct{ Data WatchlistCounts }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Total != 7 || len(body.Data.Statuses) != 5 {
		t.Fatalf("total %d statuses %d", body.Data.Total, len(body.Data.Statuses))
	}
	ptw := body.Data.Statuses["plan_to_watch"]
	if ptw.Total != 3 || ptw.Types["show"] != 1 || body.Data.Statuses["dropped"].Types == nil {
		t.Errorf("plan_to_watch %+v / dropped %+v", ptw, body.Data.Statuses["dropped"])
	}
}
