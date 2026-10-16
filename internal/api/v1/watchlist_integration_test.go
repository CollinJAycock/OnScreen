//go:build integration

// GET /api/v1/watchlist and /watchlist/counts against a real Postgres: a
// restricted profile's list never carries an item from a library it has no
// grant on, an item above its rating ceiling, an item its label restriction
// hides, a deleted item, or another user's rows — in the rows, the total, or
// the tab counts. The same user unrestricted is the positive control, so a
// fixture that silently seeded nothing can't pass.
//
// Run with: go test -tags=integration -run TestWatchlist ./internal/api/v1/
package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestWatchlist_Integration(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	user := func(name string) uuid.UUID {
		u, err := q.CreateUser(ctx, gen.CreateUserParams{Username: name + "-" + uuid.NewString()[:8]})
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	kid, other := user("wl-kid"), user("wl-other")

	lib := func(name, typ string) uuid.UUID {
		l, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
			Name: name + "-" + uuid.NewString()[:8], Type: typ, ScanPaths: []string{"/tmp/" + name},
			Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
		})
		if err != nil {
			t.Fatalf("create library: %v", err)
		}
		return l.ID
	}
	libMovies, libTV, libHidden := lib("wl-movies", "movie"), lib("wl-tv", "show"), lib("wl-hidden", "movie")

	item := func(libID uuid.UUID, typ, title, rating string, parent uuid.UUID, index int32, poster string) uuid.UUID {
		p := gen.CreateMediaItemParams{
			LibraryID: libID, Type: typ, Title: title, SortTitle: title,
			ContentRating: rcStr(rating), Genres: []string{}, Tags: []string{},
		}
		if poster != "" {
			p.PosterPath = &poster
		}
		if parent != uuid.Nil {
			p.ParentID = pgtype.UUID{Bytes: parent, Valid: true}
		}
		if index != 0 {
			p.Index = &index
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatalf("create %s %q: %v", typ, title, err)
		}
		return row.ID
	}
	visibleMovie := item(libMovies, "movie", "Alpha Visible Movie", "G", uuid.Nil, 0, "/p/alpha.jpg")
	ratedR := item(libMovies, "movie", "Bravo Rated R", "R", uuid.Nil, 0, "")
	hiddenLib := item(libHidden, "movie", "Charlie Hidden Library", "G", uuid.Nil, 0, "")
	labelled := item(libMovies, "movie", "Delta Horror Label", "G", uuid.Nil, 0, "")
	deleted := item(libMovies, "movie", "Echo Deleted", "G", uuid.Nil, 0, "")
	show := item(libTV, "show", "Foxtrot Show", "G", uuid.Nil, 0, "/p/foxtrot.jpg")
	season := item(libTV, "season", "Season 2", "G", show, 2, "")
	episode := item(libTV, "episode", "Golf Episode", "G", season, 5, "")
	othersMovie := item(libMovies, "movie", "Hotel Other User", "G", uuid.Nil, 0, "")

	if _, err := q.SetMediaItemLabels(ctx, gen.SetMediaItemLabelsParams{ID: labelled, Labels: []string{"horror"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = now() WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}

	set := func(u, id uuid.UUID, status string) {
		t.Helper()
		if _, err := q.UpsertUserWatchStatus(ctx, gen.UpsertUserWatchStatusParams{UserID: u, MediaItemID: id, Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{visibleMovie, ratedR, hiddenLib, labelled, deleted, episode} {
		set(kid, id, "plan_to_watch")
	}
	set(kid, show, "watching")
	set(other, othersMovie, "plan_to_watch")
	set(other, visibleMovie, "dropped")

	h := NewWatchStatusHandler(nil, logger).
		WithItemGate(q, rcAccess{granted: map[uuid.UUID]struct{}{libMovies: {}, libTV: {}}}).
		WithWatchlist(q)
	r := chi.NewRouter()
	r.Get("/watchlist", h.List)
	r.Get("/watchlist/counts", h.Counts)

	restricted := &auth.Claims{UserID: kid, Username: "kid", MaxContentRating: "PG-13", BlockedLabels: []string{"horror"}}
	admin := &auth.Claims{UserID: kid, Username: "kid", IsAdmin: true}

	get := func(t *testing.T, c *auth.Claims, path string, into any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), c))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	type listResp struct {
		Data []WatchlistItem `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	titles := func(items []WatchlistItem) []string {
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.Title)
		}
		sort.Strings(out)
		return out
	}
	eq := func(t *testing.T, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	}

	t.Run("restricted profile sees only permitted items", func(t *testing.T) {
		var resp listResp
		get(t, restricted, "/watchlist", &resp)
		eq(t, titles(resp.Data), []string{"Alpha Visible Movie", "Golf Episode"})
		if resp.Meta.Total != 2 {
			t.Errorf("total = %d, want 2 (hidden rows must not be counted)", resp.Meta.Total)
		}
	})

	t.Run("unrestricted control sees every live row of its own", func(t *testing.T) {
		var resp listResp
		get(t, admin, "/watchlist?sort=title", &resp)
		got := make([]string, 0, len(resp.Data))
		for _, it := range resp.Data {
			got = append(got, it.Title)
		}
		// sort=title is server-side ordering; deleted + other user's rows absent.
		eq(t, got, []string{"Alpha Visible Movie", "Bravo Rated R", "Charlie Hidden Library", "Delta Horror Label", "Golf Episode"})
		if resp.Meta.Total != 5 {
			t.Errorf("total = %d, want 5", resp.Meta.Total)
		}
	})

	t.Run("episode card carries show ancestry and poster fallback", func(t *testing.T) {
		var resp listResp
		get(t, restricted, "/watchlist?type=episode", &resp)
		if len(resp.Data) != 1 {
			t.Fatalf("got %d rows, want 1", len(resp.Data))
		}
		ep := resp.Data[0]
		if ep.GrandparentTitle == nil || *ep.GrandparentTitle != "Foxtrot Show" {
			t.Errorf("grandparent_title = %v, want Foxtrot Show", ep.GrandparentTitle)
		}
		if ep.ParentIndex == nil || *ep.ParentIndex != 2 || ep.Index == nil || *ep.Index != 5 {
			t.Errorf("season/episode = %v/%v, want 2/5", ep.ParentIndex, ep.Index)
		}
		if ep.PosterPath == nil || *ep.PosterPath != "/p/foxtrot.jpg" {
			t.Errorf("poster_path = %v, want the show's", ep.PosterPath)
		}
		if ep.Status != "plan_to_watch" || ep.WatchState != "unwatched" || ep.StatusUpdatedAt.IsZero() {
			t.Errorf("status/watch_state/updated = %q/%q/%v", ep.Status, ep.WatchState, ep.StatusUpdatedAt)
		}
	})

	t.Run("status filter and all", func(t *testing.T) {
		var resp listResp
		get(t, restricted, "/watchlist?status=watching", &resp)
		eq(t, titles(resp.Data), []string{"Foxtrot Show"})
		get(t, restricted, "/watchlist?status=all", &resp)
		eq(t, titles(resp.Data), []string{"Alpha Visible Movie", "Foxtrot Show", "Golf Episode"})
		// The other user's "dropped" row on a shared item stays theirs.
		get(t, restricted, "/watchlist?status=dropped", &resp)
		if len(resp.Data) != 0 {
			t.Errorf("another user's row leaked: %v", titles(resp.Data))
		}
	})

	t.Run("paging is stable", func(t *testing.T) {
		var p1, p2 listResp
		get(t, admin, "/watchlist?sort=title&limit=2&offset=0", &p1)
		get(t, admin, "/watchlist?sort=title&limit=2&offset=2", &p2)
		if len(p1.Data) != 2 || len(p2.Data) != 2 || p1.Meta.Total != 5 {
			t.Fatalf("pages %d/%d total %d", len(p1.Data), len(p2.Data), p1.Meta.Total)
		}
		if p1.Data[1].Title != "Bravo Rated R" || p2.Data[0].Title != "Charlie Hidden Library" {
			t.Errorf("page boundary wrong: %q / %q", p1.Data[1].Title, p2.Data[0].Title)
		}
	})

	t.Run("counts apply the same filters", func(t *testing.T) {
		var resp struct{ Data WatchlistCounts }
		get(t, restricted, "/watchlist/counts", &resp)
		ptw := resp.Data.Statuses["plan_to_watch"]
		if ptw.Total != 2 || ptw.Types["movie"] != 1 || ptw.Types["episode"] != 1 {
			t.Errorf("plan_to_watch counts = %+v, want 2 (1 movie, 1 episode)", ptw)
		}
		if resp.Data.Statuses["watching"].Total != 1 || resp.Data.Total != 3 {
			t.Errorf("watching = %d total = %d, want 1 / 3", resp.Data.Statuses["watching"].Total, resp.Data.Total)
		}
		if _, ok := resp.Data.Statuses["dropped"]; !ok {
			t.Error("every status key must be present")
		}
		get(t, admin, "/watchlist/counts", &resp)
		if resp.Data.Statuses["plan_to_watch"].Total != 5 {
			t.Errorf("control plan_to_watch = %d, want 5", resp.Data.Statuses["plan_to_watch"].Total)
		}
	})
}
