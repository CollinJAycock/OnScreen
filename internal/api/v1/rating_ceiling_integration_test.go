//go:build integration

// One cross-cutting check, against a real Postgres, that a rating-capped
// managed profile never sees an over-ceiling item, an unrated item, or an item
// from a library it has no grant on through any of the watch / discovery
// surfaces added in waves 1-2: the hub's Next Up and Plan to Watch rows,
// GET /items/{id}/up-next, GET /libraries/{id}/items?watch=,
// GET /libraries/{id}/random, franchise collection parts + items, and (for
// the same profile, a non-admin) GET /sessions showing only its own streams.
//
// Every surface also runs for the same user WITHOUT a ceiling as a positive
// control, so a fixture that silently seeded nothing can't pass.
//
// Run with: go test -tags=integration -run TestRatingCeiling ./internal/api/v1/
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/dbconv"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/testdb"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
)

// ── fakes around the real queries ────────────────────────────────────────────

// rcAccess grants non-admins a fixed set of libraries (the ACL itself is
// covered by library_access tests; here it only has to be applied).
type rcAccess struct{ granted map[uuid.UUID]struct{} }

func (a rcAccess) CanAccessLibrary(_ context.Context, _, libraryID uuid.UUID, isAdmin bool) (bool, error) {
	if isAdmin {
		return true, nil
	}
	_, ok := a.granted[libraryID]
	return ok, nil
}

func (a rcAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, isAdmin bool) (map[uuid.UUID]struct{}, error) {
	if isAdmin {
		return nil, nil
	}
	out := make(map[uuid.UUID]struct{}, len(a.granted))
	for id := range a.granted {
		out[id] = struct{}{}
	}
	return out, nil
}

// rcLibraries is the LibraryServiceIface the Items / Random handlers read.
type rcLibraries struct {
	rcAccess
	types map[uuid.UUID]string
}

func (l rcLibraries) Get(_ context.Context, id uuid.UUID) (*library.Library, error) {
	t, ok := l.types[id]
	if !ok {
		return nil, library.ErrNotFound
	}
	return &library.Library{ID: id, Type: t}, nil
}
func (rcLibraries) List(context.Context) ([]library.Library, error) { return nil, nil }
func (rcLibraries) ListForUser(context.Context, uuid.UUID, bool) ([]library.Library, error) {
	return nil, nil
}
func (rcLibraries) Create(context.Context, library.CreateLibraryParams) (*library.Library, error) {
	return nil, errors.New("unused")
}
func (rcLibraries) Update(context.Context, library.UpdateLibraryParams) (*library.Library, error) {
	return nil, errors.New("unused")
}
func (rcLibraries) Delete(context.Context, uuid.UUID) error      { return errors.New("unused") }
func (rcLibraries) EnqueueScan(context.Context, uuid.UUID) error { return errors.New("unused") }

// rcMedia lists a library through the same generated query cmd/server's
// mediaAdapter uses for the default sort (title ascending).
type rcMedia struct{ q *gen.Queries }

func (m rcMedia) ListItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32, f media.FilterParams) ([]media.Item, error) {
	if f.Sort != "title" || !f.SortAsc {
		return nil, fmt.Errorf("rcMedia: only the default sort is wired, got %s", f.Sort)
	}
	rows, err := m.q.ListMediaItemsByTitle(ctx, gen.ListMediaItemsByTitleParams{
		LibraryID:     libraryID,
		Type:          itemType,
		Limit:         limit,
		Offset:        offset,
		Genre:         f.Genre,
		YearMin:       dbconv.IntPtrToInt32Ptr(f.YearMin),
		YearMax:       dbconv.IntPtrToInt32Ptr(f.YearMax),
		RatingMin:     dbconv.Float64PtrToNumeric(f.RatingMin),
		MaxRatingRank: dbconv.IntPtrToInt32Ptr(f.MaxRatingRank),
		Watch:         nonEmptyStringPtr(f.Watch),
		WatchUserID:   f.WatchUserID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]media.Item, 0, len(rows))
	for _, r := range rows {
		out = append(out, media.Item{ID: r.ID, LibraryID: r.LibraryID, Type: r.Type, Title: r.Title, ContentRating: r.ContentRating})
	}
	return out, nil
}

func (m rcMedia) CountItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, f media.FilterParams) (int64, error) {
	return m.q.CountMediaItemsFiltered(ctx, gen.CountMediaItemsFilteredParams{
		LibraryID:     libraryID,
		Type:          itemType,
		Genre:         f.Genre,
		YearMin:       dbconv.IntPtrToInt32Ptr(f.YearMin),
		YearMax:       dbconv.IntPtrToInt32Ptr(f.YearMax),
		RatingMin:     dbconv.Float64PtrToNumeric(f.RatingMin),
		MaxRatingRank: dbconv.IntPtrToInt32Ptr(f.MaxRatingRank),
		Watch:         nonEmptyStringPtr(f.Watch),
		WatchUserID:   f.WatchUserID,
	})
}

func (m rcMedia) ListItems(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32) ([]media.Item, error) {
	return m.ListItemsFiltered(ctx, libraryID, itemType, limit, offset, media.FilterParams{Sort: "title", SortAsc: true})
}
func (m rcMedia) CountItems(ctx context.Context, libraryID uuid.UUID, itemType string) (int64, error) {
	return m.CountItemsFiltered(ctx, libraryID, itemType, media.FilterParams{Sort: "title", SortAsc: true})
}
func (rcMedia) ListDistinctGenres(context.Context, uuid.UUID) ([]string, error) { return nil, nil }
func (rcMedia) ListGenresWithCounts(context.Context, uuid.UUID, string, *int) ([]media.GenreCount, error) {
	return nil, nil
}
func (rcMedia) ListYearsWithCounts(context.Context, uuid.UUID, string, *int) ([]media.YearCount, error) {
	return nil, nil
}
func (rcMedia) ListEventCollectionsForLibrary(context.Context, uuid.UUID) ([]media.EventCollection, error) {
	return nil, nil
}

// ── fixture ──────────────────────────────────────────────────────────────────

type rcFixture struct {
	kid, other                         uuid.UUID
	libTV, libMovies, libHidden, libHT uuid.UUID

	kidsShow, kidsSeason, e1, e2, e3, e4 uuid.UUID // TV-Y show; e2 TV-MA, e3 unrated
	adultShow, a2                        uuid.UUID // TV-MA show, TV-Y episodes
	unratedShow, u2                      uuid.UUID // unrated show, TV-Y episodes
	hiddenShow, h2                       uuid.UUID // TV-Y show in a library without a grant

	okMovie, rMovie, unratedMovie, hiddenMovie uuid.UUID // plan to watch, unwatched
	okWatched, rWatched, unratedWatched        uuid.UUID // marked watched

	franchise, badFranchise uuid.UUID

	// forbidden: every item the capped profile must never be handed.
	forbidden map[uuid.UUID]string
}

func rcStr(s string) *string { return &s }

func seedRatingCeiling(ctx context.Context, t *testing.T, q *gen.Queries) *rcFixture {
	t.Helper()
	f := &rcFixture{forbidden: map[uuid.UUID]string{}}

	user := func(name string) uuid.UUID {
		u, err := q.CreateUser(ctx, gen.CreateUserParams{Username: name + "-" + uuid.NewString()[:8]})
		if err != nil {
			t.Fatalf("create user %s: %v", name, err)
		}
		return u.ID
	}
	f.kid, f.other = user("kid"), user("other")

	lib := func(name, typ string) uuid.UUID {
		l, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
			Name: name + "-" + uuid.NewString()[:8], Type: typ, ScanPaths: []string{"/tmp/" + name},
			Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
		})
		if err != nil {
			t.Fatalf("create library %s: %v", name, err)
		}
		return l.ID
	}
	f.libTV, f.libMovies = lib("tv", "show"), lib("movies", "movie")
	f.libHidden, f.libHT = lib("hidden-movies", "movie"), lib("hidden-tv", "show")

	item := func(libID uuid.UUID, typ, title string, rating *string, parent uuid.UUID, index, year, tmdb int32) uuid.UUID {
		p := gen.CreateMediaItemParams{
			LibraryID: libID, Type: typ, Title: title, SortTitle: strings.ToLower(title),
			ContentRating: rating, Genres: []string{}, Tags: []string{},
		}
		if parent != uuid.Nil {
			p.ParentID = pgtype.UUID{Bytes: parent, Valid: true}
		}
		if index != 0 {
			p.Index = &index
		}
		if year != 0 {
			p.Year = &year
		}
		if tmdb != 0 {
			p.TmdbID = &tmdb
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatalf("create %s %q: %v", typ, title, err)
		}
		return row.ID
	}
	tvy, tvma, pg, r := rcStr("TV-Y"), rcStr("TV-MA"), rcStr("PG"), rcStr("R")

	// show → season 1 → episodes; returns (show, season, episode ids).
	show := func(libID uuid.UUID, title string, showRating *string, epRatings ...*string) (uuid.UUID, uuid.UUID, []uuid.UUID) {
		sh := item(libID, "show", title, showRating, uuid.Nil, 0, 2001, 0)
		se := item(libID, "season", title+" S1", showRating, sh, 1, 0, 0)
		eps := make([]uuid.UUID, 0, len(epRatings))
		for i, er := range epRatings {
			eps = append(eps, item(libID, "episode", fmt.Sprintf("%s E%d", title, i+1), er, se, int32(i+1), 0, 0))
		}
		return sh, se, eps
	}
	var eps []uuid.UUID
	f.kidsShow, f.kidsSeason, eps = show(f.libTV, "Kids Show", tvy, tvy, tvma, nil, tvy)
	f.e1, f.e2, f.e3, f.e4 = eps[0], eps[1], eps[2], eps[3]
	var adultSeason, unratedSeason, hiddenSeason uuid.UUID
	f.adultShow, adultSeason, eps = show(f.libTV, "Adult Show", tvma, tvy, tvy)
	adult1 := eps[0]
	f.a2 = eps[1]
	f.unratedShow, unratedSeason, eps = show(f.libTV, "Unrated Show", nil, tvy, tvy)
	unrated1 := eps[0]
	f.u2 = eps[1]
	f.hiddenShow, hiddenSeason, eps = show(f.libHT, "Hidden Show", tvy, tvy, tvy)
	hidden1 := eps[0]
	f.h2 = eps[1]

	f.okMovie = item(f.libMovies, "movie", "OK Movie", pg, uuid.Nil, 0, 1990, 101)
	f.rMovie = item(f.libMovies, "movie", "R Movie", r, uuid.Nil, 0, 1991, 102)
	f.unratedMovie = item(f.libMovies, "movie", "Unrated Movie", nil, uuid.Nil, 0, 1992, 103)
	f.hiddenMovie = item(f.libHidden, "movie", "Hidden Movie", pg, uuid.Nil, 0, 1993, 104)
	f.okWatched = item(f.libMovies, "movie", "OK Watched", pg, uuid.Nil, 0, 1980, 0)
	f.rWatched = item(f.libMovies, "movie", "R Watched", r, uuid.Nil, 0, 1979, 0)
	f.unratedWatched = item(f.libMovies, "movie", "Unrated Watched", nil, uuid.Nil, 0, 1981, 0)

	for id, why := range map[uuid.UUID]string{
		f.e2: "TV-MA episode", f.e3: "unrated episode",
		f.adultShow: "TV-MA show", adultSeason: "TV-MA season", adult1: "episode of a TV-MA show", f.a2: "episode of a TV-MA show",
		f.unratedShow: "unrated show", unratedSeason: "unrated season", unrated1: "episode of an unrated show", f.u2: "episode of an unrated show",
		f.hiddenShow: "show without a grant", hiddenSeason: "season without a grant", hidden1: "episode without a grant", f.h2: "episode without a grant",
		f.rMovie: "R movie", f.unratedMovie: "unrated movie", f.hiddenMovie: "movie without a grant",
		f.rWatched: "R movie", f.unratedWatched: "unrated movie",
	} {
		f.forbidden[id] = why
	}

	// Watch history recorded before the ceiling was set (marks are how the
	// fixture says "watched"; the uncapped mark covers every episode).
	mark := func(typ string, id uuid.UUID) {
		if _, err := q.MarkWatchStateForTarget(ctx, gen.MarkWatchStateForTargetParams{
			UserID: f.kid, State: "watched", TargetType: typ, TargetID: id,
		}); err != nil {
			t.Fatalf("mark %s: %v", id, err)
		}
	}
	for _, id := range []uuid.UUID{f.e1, adult1, unrated1, hidden1} {
		mark("episode", id)
	}
	for _, id := range []uuid.UUID{f.okWatched, f.rWatched, f.unratedWatched} {
		mark("movie", id)
	}
	for _, id := range []uuid.UUID{f.okMovie, f.rMovie, f.unratedMovie, f.hiddenMovie, f.kidsShow, f.adultShow, f.unratedShow} {
		if _, err := q.UpsertUserWatchStatus(ctx, gen.UpsertUserWatchStatusParams{UserID: f.kid, MediaItemID: id, Status: "plan_to_watch"}); err != nil {
			t.Fatalf("plan to watch %s: %v", id, err)
		}
	}

	// Franchise: TMDB parts 101-104 are the four plan-to-watch movies, 105 is
	// not on the server. A second franchise holds only over-ceiling films.
	franchise := func(tmdbCol int32, name string, parts []map[string]any, members ...uuid.UUID) uuid.UUID {
		raw, err := json.Marshal(parts)
		if err != nil {
			t.Fatal(err)
		}
		if err := q.UpsertTMDBCollectionSnapshot(ctx, gen.UpsertTMDBCollectionSnapshotParams{
			TmdbCollectionID: tmdbCol, Name: name, Parts: raw,
		}); err != nil {
			t.Fatalf("snapshot %s: %v", name, err)
		}
		id, err := q.UpsertFranchiseCollection(ctx, gen.UpsertFranchiseCollectionParams{Name: name, TmdbCollectionID: tmdbCol})
		if err != nil {
			t.Fatalf("franchise %s: %v", name, err)
		}
		for _, m := range members {
			if _, err := q.AddCollectionItem(ctx, gen.AddCollectionItemParams{CollectionID: id, MediaItemID: m}); err != nil {
				t.Fatalf("franchise member: %v", err)
			}
		}
		return id
	}
	part := func(tmdb int, title string) map[string]any { return map[string]any{"tmdb_id": tmdb, "title": title} }
	f.franchise = franchise(900, "Test Collection",
		[]map[string]any{part(101, "OK Movie"), part(102, "R Movie"), part(103, "Unrated Movie"), part(104, "Hidden Movie"), part(105, "Not Here Yet")},
		f.okMovie, f.rMovie, f.unratedMovie, f.hiddenMovie)
	f.badFranchise = franchise(901, "Grown-up Collection",
		[]map[string]any{part(102, "R Movie"), part(103, "Unrated Movie")},
		f.rMovie, f.unratedMovie)
	f.forbidden[f.badFranchise] = "franchise with no visible member"
	return f
}

// ── the test ─────────────────────────────────────────────────────────────────

func TestRatingCeiling_Integration_WatchSurfaces(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	f := seedRatingCeiling(ctx, t, q)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	access := rcAccess{granted: map[uuid.UUID]struct{}{f.libTV: {}, f.libMovies: {}}}
	libs := rcLibraries{rcAccess: access, types: map[uuid.UUID]string{
		f.libTV: "show", f.libMovies: "movie", f.libHidden: "movie", f.libHT: "show",
	}}

	// Sessions: the kid's own transcode, another user's R-rated transcode and
	// direct play, and an unattributed legacy byte-traffic entry.
	store := transcode.NewSessionStore(testvalkey.New(t))
	tracker := streaming.NewTracker()
	now := time.Now().UTC()
	kidSession := transcode.Session{
		ID: transcode.NewSessionID(), UserID: f.kid, MediaItemID: f.okMovie, FileID: uuid.New(),
		Decision: "transcode", FilePath: "/m/ok.mkv", CreatedAt: now, LastActivityAt: now,
	}
	for _, s := range []transcode.Session{kidSession, {
		ID: transcode.NewSessionID(), UserID: f.other, MediaItemID: f.rMovie, FileID: uuid.New(),
		Decision: "transcode", FilePath: "/m/r.mkv", CreatedAt: now, LastActivityAt: now,
	}} {
		if err := store.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	tracker.HeartbeatUser(f.other, "10.0.0.20", f.unratedMovie, "Other TV", "directplay")
	tracker.Heartbeat("10.0.0.21", f.rWatched, "Legacy")

	colH := NewCollectionHandler(q, logger).WithLibraryAccess(access).WithFranchise(q, nil)
	hubH := NewHubHandler(q, logger).WithLibraryAccess(access).WithWatchRows(q)
	wsH := NewWatchStateHandler(q, access, logger)
	libH := NewLibraryHandler(libs, logger).WithMedia(rcMedia{q: q}).WithWatchState(q)
	sessH := NewNativeSessionsHandler(store, tracker, q, logger)

	r := chi.NewRouter()
	r.Get("/hub", hubH.Get)
	r.Get("/items/{id}/up-next", wsH.UpNext)
	r.Get("/libraries/{id}/items", libH.Items)
	r.Get("/libraries/{id}/random", libH.Random)
	r.Get("/collections", colH.List)
	r.Get("/collections/{id}", colH.Get)
	r.Get("/collections/{id}/items", colH.Items)
	r.Get("/sessions", sessH.List)

	capped := &auth.Claims{UserID: f.kid, Username: "kid", MaxContentRating: "PG-13"}
	uncapped := &auth.Claims{UserID: f.kid, Username: "kid"}

	get := func(t *testing.T, c *auth.Claims, path string) (int, []byte) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), c))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	getOK := func(t *testing.T, c *auth.Claims, path string, into any) []byte {
		t.Helper()
		code, body := get(t, c, path)
		if code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, code, body)
		}
		if into != nil {
			if err := json.Unmarshal(body, into); err != nil {
				t.Fatalf("GET %s: decode: %v (%s)", path, err, body)
			}
		}
		return body
	}
	// noForbidden fails for any forbidden item id anywhere in a body.
	noForbidden := func(t *testing.T, what string, body []byte) {
		t.Helper()
		for id, why := range f.forbidden {
			if strings.Contains(string(body), id.String()) {
				t.Errorf("%s leaked %s (%s): %s", what, id, why, body)
			}
		}
	}
	ids := func(items []struct {
		ID string `json:"id"`
	}) []string {
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.ID)
		}
		sort.Strings(out)
		return out
	}
	sorted := func(in ...uuid.UUID) []string {
		out := make([]string, 0, len(in))
		for _, id := range in {
			out = append(out, id.String())
		}
		sort.Strings(out)
		return out
	}
	same := func(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

	type idList = []struct {
		ID string `json:"id"`
	}

	t.Run("hub next_up and plan_to_watch", func(t *testing.T) {
		var env struct {
			Data struct {
				NextUp      idList `json:"next_up"`
				PlanToWatch idList `json:"plan_to_watch"`
			} `json:"data"`
		}
		body := getOK(t, capped, "/hub", &env)
		if got, want := ids(env.Data.NextUp), sorted(f.e4); !same(got, want) {
			t.Errorf("capped next_up = %v, want only the next in-ceiling episode %v", got, want)
		}
		if got, want := ids(env.Data.PlanToWatch), sorted(f.okMovie, f.kidsShow); !same(got, want) {
			t.Errorf("capped plan_to_watch = %v, want %v", got, want)
		}
		// Nothing forbidden in ANY hub row (continue watching, recently
		// added and trending included), not just the two asserted above.
		noForbidden(t, "hub", body)

		// Positive control: no ceiling → the over-ceiling next episodes and
		// plan-to-watch titles are there (the grant still hides the others).
		getOK(t, uncapped, "/hub", &env)
		if got, want := ids(env.Data.NextUp), sorted(f.e2, f.a2, f.u2); !same(got, want) {
			t.Errorf("uncapped next_up = %v, want %v", got, want)
		}
		if got, want := ids(env.Data.PlanToWatch), sorted(f.okMovie, f.rMovie, f.unratedMovie, f.kidsShow, f.adultShow, f.unratedShow); !same(got, want) {
			t.Errorf("uncapped plan_to_watch = %v, want %v", got, want)
		}
	})

	t.Run("up-next", func(t *testing.T) {
		var env struct {
			Data UpNextResponse `json:"data"`
		}
		for _, container := range []uuid.UUID{f.kidsShow, f.kidsSeason} {
			body := getOK(t, capped, "/items/"+container.String()+"/up-next", &env)
			if env.Data.Mode != upNextNext || env.Data.Episode == nil || env.Data.Episode.ID != f.e4.String() {
				t.Errorf("capped up-next on %s = %s, want next → e4", container, body)
			}
			noForbidden(t, "up-next", body)
		}
		getOK(t, uncapped, "/items/"+f.kidsShow.String()+"/up-next", &env)
		if env.Data.Episode == nil || env.Data.Episode.ID != f.e2.String() {
			t.Errorf("uncapped up-next = %+v, want e2 (control)", env.Data)
		}
		for _, sh := range []uuid.UUID{f.adultShow, f.unratedShow, f.hiddenShow} {
			if code, body := get(t, capped, "/items/"+sh.String()+"/up-next"); code != http.StatusNotFound {
				t.Errorf("capped up-next on %s (%s) = %d %s, want 404", sh, f.forbidden[sh], code, body)
			}
		}
	})

	t.Run("library items ?watch=", func(t *testing.T) {
		list := func(c *auth.Claims, libID uuid.UUID, watch string) ([]string, []byte) {
			var env struct {
				Data idList `json:"data"`
			}
			path := "/libraries/" + libID.String() + "/items"
			if watch != "" {
				path += "?watch=" + watch
			}
			body := getOK(t, c, path, &env)
			return ids(env.Data), body
		}
		cases := []struct {
			lib          uuid.UUID
			watch        string
			capped, full []string
		}{
			{f.libMovies, "", sorted(f.okMovie, f.okWatched), sorted(f.okMovie, f.rMovie, f.unratedMovie, f.okWatched, f.rWatched, f.unratedWatched)},
			{f.libMovies, "watched", sorted(f.okWatched), sorted(f.okWatched, f.rWatched, f.unratedWatched)},
			{f.libMovies, "unwatched", sorted(f.okMovie), sorted(f.okMovie, f.rMovie, f.unratedMovie)},
			{f.libMovies, "in_progress", sorted(), sorted()},
			{f.libTV, "in_progress", sorted(f.kidsShow), sorted(f.kidsShow, f.adultShow, f.unratedShow)},
			{f.libTV, "watched", sorted(), sorted()},
		}
		for _, tc := range cases {
			got, body := list(capped, tc.lib, tc.watch)
			if !same(got, tc.capped) {
				t.Errorf("capped %s ?watch=%s = %v, want %v", tc.lib, tc.watch, got, tc.capped)
			}
			noForbidden(t, "library items ?watch="+tc.watch, body)
			if got, _ := list(uncapped, tc.lib, tc.watch); !same(got, tc.full) {
				t.Errorf("uncapped %s ?watch=%s = %v, want %v (control)", tc.lib, tc.watch, got, tc.full)
			}
		}
		for _, lib := range []uuid.UUID{f.libHidden, f.libHT} {
			if code, _ := get(t, capped, "/libraries/"+lib.String()+"/items?watch=unwatched"); code != http.StatusNotFound {
				t.Errorf("library without a grant: %d, want 404", code)
			}
		}
	})

	t.Run("library random", func(t *testing.T) {
		pick := func(c *auth.Claims, query string) (int, string) {
			var env struct {
				Data RandomItemResponse `json:"data"`
			}
			code, body := get(t, c, "/libraries/"+f.libMovies.String()+"/random"+query)
			if code == http.StatusOK {
				if err := json.Unmarshal(body, &env); err != nil {
					t.Fatal(err)
				}
			}
			return code, env.Data.ID
		}
		allowed := map[string]map[string]bool{
			"":                 {f.okMovie.String(): true, f.okWatched.String(): true},
			"?watch=unwatched": {f.okMovie.String(): true},
			"?watch=watched":   {f.okWatched.String(): true},
		}
		for query, ok := range allowed {
			for i := 0; i < 25; i++ {
				code, id := pick(capped, query)
				if code != http.StatusOK || !ok[id] {
					why := ""
					if u, err := uuid.Parse(id); err == nil {
						why = f.forbidden[u]
					}
					t.Fatalf("capped random%s = %d %q %s", query, code, id, why)
				}
			}
		}
		// Deterministic control: only the R film was released in 1979.
		if code, id := pick(uncapped, "?watch=watched&year_min=1979&year_max=1979"); code != http.StatusOK || id != f.rWatched.String() {
			t.Errorf("uncapped 1979 pick = %d %s, want the R film (control)", code, id)
		}
		if code, id := pick(capped, "?watch=watched&year_min=1979&year_max=1979"); code != http.StatusNotFound {
			t.Errorf("capped 1979 pick = %d %s, want 404", code, id)
		}
		if code, _ := get(t, capped, "/libraries/"+f.libHidden.String()+"/random"); code != http.StatusNotFound {
			t.Errorf("random in a library without a grant = %d, want 404", code)
		}
	})

	t.Run("franchise collection parts and items", func(t *testing.T) {
		var env struct {
			Data collectionResponse `json:"data"`
		}
		body := getOK(t, capped, "/collections/"+f.franchise.String(), &env)
		if len(env.Data.Parts) != 1 || env.Data.Parts[0].TMDBID != 101 || env.Data.Parts[0].ItemID == nil || *env.Data.Parts[0].ItemID != f.okMovie.String() {
			t.Errorf("capped parts = %+v, want only the owned in-ceiling film (missing parts hidden)", env.Data.Parts)
		}
		noForbidden(t, "collection parts", body)
		for _, title := range []string{"R Movie", "Unrated Movie", "Hidden Movie", "Not Here Yet"} {
			if strings.Contains(string(body), title) {
				t.Errorf("capped parts name %q: %s", title, body)
			}
		}
		getOK(t, uncapped, "/collections/"+f.franchise.String(), &env)
		if len(env.Data.Parts) != 5 {
			t.Errorf("uncapped parts = %d, want all 5 (control)", len(env.Data.Parts))
		}

		var items struct {
			Data idList `json:"data"`
			Meta struct {
				Total int64 `json:"total"`
			} `json:"meta"`
		}
		body = getOK(t, capped, "/collections/"+f.franchise.String()+"/items", &items)
		if got := ids(items.Data); !same(got, sorted(f.okMovie)) || items.Meta.Total != 1 {
			t.Errorf("capped collection items = %v (total %d), want only the PG film", got, items.Meta.Total)
		}
		noForbidden(t, "collection items", body)

		if code, _ := get(t, capped, "/collections/"+f.badFranchise.String()); code != http.StatusNotFound {
			t.Errorf("franchise with no visible member = %d, want 404", code)
		}
		var listing struct {
			Data idList `json:"data"`
		}
		body = getOK(t, capped, "/collections?include=franchise", &listing)
		if got := ids(listing.Data); !same(got, sorted(f.franchise)) {
			t.Errorf("capped franchise listing = %v, want only the visible franchise", got)
		}
		noForbidden(t, "collections listing", body)
		getOK(t, uncapped, "/collections?include=franchise", &listing)
		if len(listing.Data) != 2 {
			t.Errorf("uncapped franchise listing = %d rows, want 2 (control)", len(listing.Data))
		}
	})

	t.Run("sessions: non-admin sees only its own", func(t *testing.T) {
		var env struct {
			Data idList `json:"data"`
		}
		body := getOK(t, capped, "/sessions", &env)
		if len(env.Data) != 1 || env.Data[0].ID != kidSession.ID {
			t.Errorf("capped /sessions = %s, want only the kid's own session", body)
		}
		if strings.Contains(string(body), "R Movie") || strings.Contains(string(body), "Unrated") || strings.Contains(string(body), "R Watched") {
			t.Errorf("/sessions leaked another viewer's title: %s", body)
		}
		admin := &auth.Claims{UserID: uuid.New(), IsAdmin: true}
		getOK(t, admin, "/sessions", &env)
		if len(env.Data) != 4 {
			t.Errorf("admin /sessions = %d cards, want 4 (control: 2 sessions, 2 direct plays)", len(env.Data))
		}
	})
}
