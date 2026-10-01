//go:build integration

// SQL-side library scoping for the queries that used to be filtered in the
// handler after their LIMIT (global search, the hub's cross-library Recently
// Added) and for the people directory. Real Postgres via testcontainers.
//
// Run with: go test -tags=integration -run Integration ./internal/api/v1/...
package v1

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestLibraryScope_Integration(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	granted := mustCreateLibrary(ctx, t, q)
	hidden := mustCreateLibrary(ctx, t, q)
	poster := "poster.jpg"
	pg := "PG"

	newItem := func(lib uuid.UUID, title string, rating *string) uuid.UUID {
		t.Helper()
		row, err := q.CreateMediaItem(ctx, gen.CreateMediaItemParams{
			LibraryID: lib, Type: "movie", Title: title, SortTitle: title,
			PosterPath: &poster, ContentRating: rating,
		})
		if err != nil {
			t.Fatalf("seed %q: %v", title, err)
		}
		return row.ID
	}

	// The demo title first, then a crowd of newer, better-ranked matches in a
	// library the restricted user can't see.
	demo := newItem(granted, "Night of the Living Dead", &pg)
	for i := range 5 {
		newItem(hidden, fmt.Sprintf("Night %d", i), &pg)
	}

	t.Run("global search scopes before the limit", func(t *testing.T) {
		search := func(libs []uuid.UUID) []gen.SearchMediaItemsGlobalRow {
			t.Helper()
			rows, err := q.SearchMediaItemsGlobal(ctx, gen.SearchMediaItemsGlobalParams{
				WebsearchToTsquery: "night", Limit: 3, LibraryIds: libs,
			})
			if err != nil {
				t.Fatalf("SearchMediaItemsGlobal: %v", err)
			}
			return rows
		}
		// The admin's top 3 are all hidden-library rows: filtering those
		// afterwards (the old handler) left the user with nothing.
		for _, r := range search(nil) {
			if r.ID == demo {
				t.Fatal("fixture: the demo title should be crowded out of the unscoped top 3")
			}
		}
		if rows := search([]uuid.UUID{granted}); len(rows) != 1 || rows[0].ID != demo {
			t.Errorf("scoped search = %+v, want the demo title", rows)
		}
		if rows := search([]uuid.UUID{}); len(rows) != 0 {
			t.Errorf("no grants: %d rows, want none", len(rows))
		}
	})

	t.Run("recently added scopes before the limit", func(t *testing.T) {
		recent := func(libs []uuid.UUID) []gen.ListRecentlyAddedRow {
			t.Helper()
			rows, err := q.ListRecentlyAdded(ctx, gen.ListRecentlyAddedParams{Limit: 3, LibraryIds: libs})
			if err != nil {
				t.Fatalf("ListRecentlyAdded: %v", err)
			}
			return rows
		}
		for _, r := range recent(nil) {
			if r.ID == demo {
				t.Fatal("fixture: the demo title should be older than the unscoped top 3")
			}
		}
		if rows := recent([]uuid.UUID{granted}); len(rows) != 1 || rows[0].ID != demo {
			t.Errorf("scoped recently added = %+v, want the demo title", rows)
		}
	})

	t.Run("people search only finds people credited on visible items", func(t *testing.T) {
		person := func(name string, tmdb int32, items ...uuid.UUID) uuid.UUID {
			t.Helper()
			p, err := q.UpsertPersonByTMDB(ctx, gen.UpsertPersonByTMDBParams{TmdbID: &tmdb, Name: name})
			if err != nil {
				t.Fatalf("person %q: %v", name, err)
			}
			for _, it := range items {
				if err := q.InsertCredit(ctx, gen.InsertCreditParams{MediaItemID: it, PersonID: p.ID, Role: "cast"}); err != nil {
					t.Fatalf("credit %q: %v", name, err)
				}
			}
			return p.ID
		}
		rRated := "R"
		mature := newItem(granted, "Mature Film", &rRated)
		hiddenFilm := newItem(hidden, "Private Film", &pg)

		visible := person("Rocky Demo", 1, demo)
		private := person("Rocky Private", 2, hiddenFilm)
		overCeiling := person("Rocky Mature", 3, mature)
		uncredited := person("Rocky Nobody", 4)

		names := func(libs []uuid.UUID, maxRank *int32) map[uuid.UUID]bool {
			t.Helper()
			rows, err := q.SearchPeople(ctx, gen.SearchPeopleParams{
				Prefix: "rocky", LibraryIds: libs, MaxRatingRank: maxRank, LimitN: 20,
			})
			if err != nil {
				t.Fatalf("SearchPeople: %v", err)
			}
			out := make(map[uuid.UUID]bool, len(rows))
			for _, r := range rows {
				out[r.ID] = true
			}
			return out
		}

		all := names(nil, nil)
		for _, id := range []uuid.UUID{visible, private, overCeiling, uncredited} {
			if !all[id] {
				t.Errorf("admin search misses %s", id)
			}
		}

		user := names([]uuid.UUID{granted}, nil)
		if !user[visible] || !user[overCeiling] || user[private] || user[uncredited] || len(user) != 2 {
			t.Errorf("restricted search = %v, want only the people credited in the granted library", user)
		}

		pgRank := int32(1) // PG
		kid := names([]uuid.UUID{granted}, &pgRank)
		if !kid[visible] || len(kid) != 1 {
			t.Errorf("PG profile search = %v, want only the PG title's cast", kid)
		}

		if none := names([]uuid.UUID{}, nil); len(none) != 0 {
			t.Errorf("no grants: %v, want nobody", none)
		}
	})
}
