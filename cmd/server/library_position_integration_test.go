//go:build integration

// GET /libraries/{id}/items/position against a real Postgres, through the
// production media adapter and media service: for every sort (both
// directions), with filters, a watch filter, a rating ceiling and a label
// restriction, each item's position is its row in GET /libraries/{id}/items
// with the same parameters — ties on the sort key included — and an item
// outside that listing is a 404.
//
// Run with: go test -tags 'dev integration' -p 1 ./cmd/server/ -run Integration_ItemPosition
package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/testdb"
)

// posLibraries is the library service the handlers read: library types,
// and a grant list for non-admins.
type posLibraries struct {
	types   map[uuid.UUID]string
	granted map[uuid.UUID]bool
}

func (l posLibraries) Get(_ context.Context, id uuid.UUID) (*library.Library, error) {
	t, ok := l.types[id]
	if !ok {
		return nil, library.ErrNotFound
	}
	return &library.Library{ID: id, Type: t}, nil
}
func (l posLibraries) CanAccessLibrary(_ context.Context, _, id uuid.UUID, isAdmin bool) (bool, error) {
	return isAdmin || l.granted[id], nil
}
func (posLibraries) List(context.Context) ([]library.Library, error) { return nil, nil }
func (posLibraries) ListForUser(context.Context, uuid.UUID, bool) ([]library.Library, error) {
	return nil, nil
}
func (posLibraries) Create(context.Context, library.CreateLibraryParams) (*library.Library, error) {
	return nil, library.ErrNotFound
}
func (posLibraries) Update(context.Context, library.UpdateLibraryParams) (*library.Library, error) {
	return nil, library.ErrNotFound
}
func (posLibraries) Delete(context.Context, uuid.UUID) error      { return nil }
func (posLibraries) EnqueueScan(context.Context, uuid.UUID) error { return nil }

func TestIntegration_ItemPosition_MatchesListing(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := &mediaAdapter{q: q}
	svc := media.NewService(adapter, adapter, logger)

	newUser := func(name string) uuid.UUID {
		u, err := q.CreateUser(ctx, gen.CreateUserParams{Username: name + "-" + uuid.NewString()[:8]})
		if err != nil {
			t.Fatalf("create user %s: %v", name, err)
		}
		return u.ID
	}
	newLib := func(name, typ string) uuid.UUID {
		l, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
			Name: name + "-" + uuid.NewString()[:8], Type: typ, ScanPaths: []string{"/tmp/" + name},
			Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
		})
		if err != nil {
			t.Fatalf("create library %s: %v", name, err)
		}
		return l.ID
	}
	admin, kid := newUser("admin"), newUser("kid")
	movies, music, photos, hidden := newLib("movies", "movie"), newLib("music", "music"), newLib("photos", "photo"), newLib("hidden", "movie")

	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	type spec struct {
		typ, title    string
		sortTitle     string // "": the title
		parent        uuid.UUID
		year          int32 // 0: NULL
		rating        string
		contentRating string // "": unrated
		genres        []string
		labels        []string
		created       time.Duration // after t0
		taken         string        // "": NULL
	}
	item := func(lib uuid.UUID, s spec) uuid.UUID {
		t.Helper()
		p := gen.CreateMediaItemParams{
			LibraryID: lib, Type: s.typ, Title: s.title, SortTitle: s.title,
			Genres: append([]string{}, s.genres...), Tags: []string{},
		}
		if s.sortTitle != "" {
			p.SortTitle = s.sortTitle
		}
		if s.parent != uuid.Nil {
			p.ParentID = pgtype.UUID{Bytes: s.parent, Valid: true}
		}
		if s.year != 0 {
			p.Year = &s.year
		}
		if s.contentRating != "" {
			cr := s.contentRating
			p.ContentRating = &cr
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatalf("create %q: %v", s.title, err)
		}
		var rating, taken any
		if s.rating != "" {
			rating = s.rating
		}
		if s.taken != "" {
			taken = s.taken
		}
		if _, err := pool.Exec(ctx, `UPDATE media_items
			SET created_at = $2, rating = $3::numeric, originally_available_at = $4::date, labels = $5::text[]
			WHERE id = $1`, row.ID, t0.Add(s.created), rating, taken, append([]string{}, s.labels...)); err != nil {
			t.Fatalf("shape %q: %v", s.title, err)
		}
		return row.ID
	}

	// Ties on every sort key: two "alien"s, three 8.5 ratings, two 1979s and
	// two 1989s, NULL years / ratings / dates, three items added at t0.
	alienPG := item(movies, spec{typ: "movie", title: "alien", year: 1979, rating: "8.5", contentRating: "PG", genres: []string{"Horror", "Sci-Fi"}, labels: []string{"horror"}, taken: "2020-01-01"})
	alienR := item(movies, spec{typ: "movie", title: "alien (director's cut)", sortTitle: "alien", year: 1979, rating: "8.5", contentRating: "R", genres: []string{"Sci-Fi"}, taken: "2020-01-01"})
	batman := item(movies, spec{typ: "movie", title: "batman", year: 1989, contentRating: "PG-13", genres: []string{"Action"}, created: time.Hour})
	brazil := item(movies, spec{typ: "movie", title: "brazil", rating: "7.9", contentRating: "PG", genres: []string{"Sci-Fi"}, created: time.Hour, taken: "2021-05-05"})
	casablanca := item(movies, spec{typ: "movie", title: "casablanca", year: 1942, rating: "8.5", contentRating: "PG", genres: []string{"Drama"}, created: 2 * time.Hour, taken: "2020-01-01"})
	dune := item(movies, spec{typ: "movie", title: "dune", year: 1989, rating: "7.9", contentRating: "PG-13", genres: []string{"Sci-Fi"}})
	eraserhead := item(movies, spec{typ: "movie", title: "eraserhead", created: 2 * time.Hour, taken: "2021-05-05"})
	fargo := item(movies, spec{typ: "movie", title: "fargo", year: 1996, rating: "8.1", contentRating: "R", genres: []string{"Drama"}, created: time.Hour, taken: "2019-03-03"})
	gattaca := item(movies, spec{typ: "movie", title: "gattaca", year: 1997, contentRating: "PG-13"})
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = NOW() WHERE id = $1`, gattaca); err != nil {
		t.Fatal(err)
	}
	andor := item(movies, spec{typ: "show", title: "andor", contentRating: "PG"})                // not a movie
	zodiac := item(hidden, spec{typ: "movie", title: "zodiac", year: 2007, contentRating: "PG"}) // another library
	movieIDs := []uuid.UUID{alienPG, alienR, batman, brazil, casablanca, dune, eraserhead, fargo, gattaca, andor, zodiac}

	// Albums under artists, two artists with the same name, same-year albums.
	abba1 := item(music, spec{typ: "artist", title: "abba", contentRating: "PG"})
	abba2 := item(music, spec{typ: "artist", title: "abba (2)", sortTitle: "abba", contentRating: "PG"})
	beatles := item(music, spec{typ: "artist", title: "beatles", contentRating: "PG"})
	var albumIDs []uuid.UUID
	for _, a := range []spec{
		{title: "arrival", parent: abba1, year: 1976}, {title: "abba", parent: abba1, year: 1976},
		{title: "voulez-vous", parent: abba2, year: 1979}, {title: "ring ring", parent: abba2},
		{title: "help", parent: beatles, year: 1965}, {title: "rubber soul", parent: beatles, year: 1965},
	} {
		a.typ, a.contentRating = "album", "PG"
		albumIDs = append(albumIDs, item(music, a))
	}

	// Photos: the TV and web grids' default sort (taken_at desc), with
	// same-moment and undated photos.
	var photoIDs []uuid.UUID
	for i, taken := range []string{"2023-07-04", "2023-07-04", "", "2022-12-25", "", "2023-07-04", "2021-01-01"} {
		photoIDs = append(photoIDs, item(photos, spec{typ: "photo", title: "img_" + string(rune('a'+i)), contentRating: "PG", created: time.Duration(i%2) * time.Hour, taken: taken}))
	}

	// The kid has watched casablanca and dune.
	for _, id := range []uuid.UUID{casablanca, dune} {
		if _, err := q.MarkWatchStateForTarget(ctx, gen.MarkWatchStateForTargetParams{
			UserID: kid, State: "watched", TargetType: "movie", TargetID: id,
		}); err != nil {
			t.Fatalf("mark watched: %v", err)
		}
	}

	libs := posLibraries{
		types:   map[uuid.UUID]string{movies: "movie", music: "music", photos: "photo", hidden: "movie"},
		granted: map[uuid.UUID]bool{movies: true, music: true, photos: true},
	}
	h := v1.NewLibraryHandler(libs, logger).WithMedia(svc)
	r := chi.NewRouter()
	r.Get("/libraries/{id}/items", h.Items)
	r.Get("/libraries/{id}/items/position", h.ItemPosition)

	get := func(t *testing.T, c *auth.Claims, path string, out any) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(middleware.WithClaims(req.Context(), c))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK && out != nil {
			if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
				t.Fatalf("%s: %v (%s)", path, err, rec.Body.String())
			}
		}
		return rec.Code
	}

	adminC := &auth.Claims{UserID: admin, Username: "admin", IsAdmin: true}
	kidC := &auth.Claims{UserID: kid, Username: "kid"}
	cappedC := &auth.Claims{UserID: kid, Username: "kid", MaxContentRating: "PG-13"}
	noHorrorC := &auth.Claims{UserID: kid, Username: "kid", BlockedLabels: []string{"horror"}}

	type listCase struct {
		name   string
		claims *auth.Claims
		lib    uuid.UUID
		query  string
		ids    []uuid.UUID // every item the library holds, listed or not
		min    int         // the listing must hold at least this many items
	}
	var cases []listCase
	for _, s := range []string{"", "sort=title", "sort=bogus", "sort=year", "sort=rating", "sort=created_at", "sort=taken_at"} {
		for _, dir := range []string{"", "asc", "desc"} {
			qs := s
			if dir != "" {
				qs += "&sort_dir=" + dir
			}
			cases = append(cases, listCase{name: "movies " + qs, claims: adminC, lib: movies, query: qs, ids: movieIDs, min: 8})
		}
	}
	cases = append(cases,
		listCase{"sci-fi by year", adminC, movies, "sort=year&genre=Sci-Fi", movieIDs, 4},
		listCase{"since 1980 by rating", adminC, movies, "sort=rating&year_min=1980", movieIDs, 3},
		listCase{"rated 8+ by title desc", adminC, movies, "sort=title&sort_dir=desc&rating_min=8", movieIDs, 4},
		listCase{"unwatched by date added", kidC, movies, "sort=created_at&watch=unwatched", movieIDs, 6},
		listCase{"watched", kidC, movies, "watch=watched", movieIDs, 2},
		listCase{"rating ceiling", cappedC, movies, "sort=year&sort_dir=desc", movieIDs, 5},
		listCase{"rating ceiling, title", cappedC, movies, "", movieIDs, 5},
		listCase{"blocked label", noHorrorC, movies, "sort=rating", movieIDs, 7},
		listCase{"albums by artist", adminC, music, "type=album&sort=artist", albumIDs, 6},
		listCase{"albums by artist desc", adminC, music, "type=album&sort=artist&sort_dir=desc", albumIDs, 6},
		listCase{"albums by year", adminC, music, "type=album&sort=year", albumIDs, 6},
		listCase{"artists", adminC, music, "", []uuid.UUID{abba1, abba2, beatles}, 3},
		listCase{"photos taken desc", kidC, photos, "sort=taken_at&sort_dir=desc", photoIDs, 7},
		listCase{"photos taken asc", kidC, photos, "sort=taken_at&sort_dir=asc", photoIDs, 7},
	)

	type listing struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	type position struct {
		Data struct {
			Offset *int64 `json:"offset"`
			Total  *int64 `json:"total"`
		} `json:"data"`
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := "/libraries/" + c.lib.String() + "/items"
			var l listing
			if code := get(t, c.claims, base+"?limit=200&"+c.query, &l); code != http.StatusOK {
				t.Fatalf("/items: %d", code)
			}
			if len(l.Data) < c.min || int64(len(l.Data)) != l.Meta.Total {
				t.Fatalf("/items listed %d of total %d, want at least %d", len(l.Data), l.Meta.Total, c.min)
			}
			listed := map[uuid.UUID]bool{}
			for row, it := range l.Data {
				id := uuid.MustParse(it.ID)
				listed[id] = true
				var p position
				path := base + "/position?item_id=" + id.String() + "&" + c.query
				if code := get(t, c.claims, path, &p); code != http.StatusOK {
					t.Errorf("%s (row %d): %d", it.ID, row, code)
					continue
				}
				if p.Data.Offset == nil || *p.Data.Offset != int64(row) || p.Data.Total == nil || *p.Data.Total != l.Meta.Total {
					t.Errorf("%s: position %v / %v, want row %d of %d", it.ID, p.Data.Offset, p.Data.Total, row, l.Meta.Total)
				}
			}
			for _, id := range c.ids {
				if listed[id] {
					continue
				}
				if code := get(t, c.claims, base+"/position?item_id="+id.String()+"&"+c.query, nil); code != http.StatusNotFound {
					t.Errorf("%s is not in the listing: position %d, want 404", id, code)
				}
			}
		})
	}

	// The cases above must have refused the items each gate is for.
	t.Run("gated items", func(t *testing.T) {
		pos := func(c *auth.Claims, lib, id uuid.UUID, query string) int {
			return get(t, c, "/libraries/"+lib.String()+"/items/position?item_id="+id.String()+"&"+query, nil)
		}
		for name, code := range map[string]int{
			"deleted":                  pos(adminC, movies, gattaca, ""),
			"another type":             pos(adminC, movies, andor, ""),
			"another library":          pos(adminC, movies, zodiac, ""),
			"over the ceiling":         pos(cappedC, movies, fargo, ""),
			"unrated under a ceiling":  pos(cappedC, movies, eraserhead, ""),
			"blocked label":            pos(noHorrorC, movies, alienPG, ""),
			"filtered out by genre":    pos(adminC, movies, batman, "genre=Sci-Fi"),
			"filtered out by watch":    pos(kidC, movies, casablanca, "watch=unwatched"),
			"library without a grant":  pos(kidC, hidden, zodiac, ""),
			"an artist, albums listed": pos(adminC, music, abba1, "type=album"),
			"an album, artists listed": pos(adminC, music, albumIDs[0], ""),
		} {
			if code != http.StatusNotFound {
				t.Errorf("%s: %d, want 404", name, code)
			}
		}
		if code := pos(adminC, hidden, zodiac, ""); code != http.StatusOK {
			t.Errorf("admin, library without a grant: %d, want 200", code)
		}
		if code := pos(adminC, movies, alienPG, "watch=sometimes"); code != http.StatusBadRequest {
			t.Errorf("bad watch filter: %d, want 400", code)
		}
		if code := get(t, adminC, "/libraries/"+movies.String()+"/items/position?item_id=x", nil); code != http.StatusBadRequest {
			t.Errorf("bad item_id: %d, want 400", code)
		}
	})
}
