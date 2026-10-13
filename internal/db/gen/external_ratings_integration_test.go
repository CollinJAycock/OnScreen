//go:build integration

package gen_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestExternalRatingQueries_Integration runs the external-ratings queries
// (migration 00041) against Postgres: which IMDb IDs are due for a lookup
// (missing first, recent titles after a week, older ones after 90 days, IDs
// OMDb didn't know after 30 days; movies and shows only, each ID once) and
// the upsert.
//
// Run with: go test -tags 'dev integration' -run ExternalRating ./internal/db/gen/
func TestExternalRatingQueries_Integration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := gen.New(pool)
	now := time.Now()

	var lib string
	if err := pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('M', 'movie', '{/m}') RETURNING id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	n := 0
	item := func(typ, imdb string, year int, deleted bool) {
		t.Helper()
		var del any
		if deleted {
			del = now
		}
		n++
		title := fmt.Sprintf("%s #%d", imdb, n)
		if _, err := pool.Exec(ctx,
			`INSERT INTO media_items (library_id, type, title, sort_title, imdb_id, year, deleted_at) VALUES ($1, $2, $3, $3, $4, $5, $6)`,
			lib, typ, title, imdb, year, del); err != nil {
			t.Fatalf("insert %s: %v", imdb, err)
		}
	}
	item("movie", "tt-new", 2010, false)     // never fetched
	item("movie", "tt-new", 2010, false)     // a second copy: listed once
	item("show", "tt-show", 2008, false)     // never fetched
	item("episode", "tt-ep", 2008, false)    // episodes aren't looked up
	item("movie", "tt-gone", 2010, true)     // deleted
	item("movie", "tt-recent", now.Year(), false)
	item("movie", "tt-old", 2001, false)
	item("movie", "tt-oldfresh", 2001, false)
	item("movie", "tt-missing", 2001, false)

	seed := func(id string, found bool, age time.Duration) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO external_ratings (imdb_id, found, fetched_at) VALUES ($1, $2, $3)`,
			id, found, now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	day := 24 * time.Hour
	seed("tt-recent", true, 8*day)    // recent title, over a week old: due
	seed("tt-old", true, 100*day)     // old title, over 90 days: due
	seed("tt-oldfresh", true, 10*day) // old title, 10 days: not due
	seed("tt-missing", false, 40*day) // unknown to OMDb 40 days ago: due

	ts := func(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
	params := gen.ListExternalRatingCandidatesParams{
		MissingBefore: ts(now.Add(-30 * day)),
		RecentYear:    int32(now.Year() - 1),
		RecentBefore:  ts(now.Add(-7 * day)),
		OldBefore:     ts(now.Add(-90 * day)),
		MaxIds:        100,
	}
	ids, err := q.ListExternalRatingCandidates(ctx, params)
	if err != nil {
		t.Fatalf("ListExternalRatingCandidates: %v", err)
	}
	// Never-fetched first, then the stalest.
	want := []string{"tt-new", "tt-show", "tt-old", "tt-missing", "tt-recent"}
	if !slices.Equal(ids, want) {
		t.Errorf("candidates = %v, want %v", ids, want)
	}
	params.MaxIds = 2
	if ids, _ := q.ListExternalRatingCandidates(ctx, params); len(ids) != 2 {
		t.Errorf("limit 2 returned %v", ids)
	}

	score, votes, rt := 7.9, int32(12345), int16(88)
	if err := q.UpsertExternalRatings(ctx, gen.UpsertExternalRatingsParams{
		ImdbID: "tt-new", ImdbRating: &score, ImdbVotes: &votes, RottenTomatoes: &rt, Found: true,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := q.UpsertExternalRatings(ctx, gen.UpsertExternalRatingsParams{ImdbID: "tt-old", Found: true}); err != nil {
		t.Fatalf("upsert refresh: %v", err)
	}
	got, err := q.GetExternalRatings(ctx, "tt-new")
	if err != nil || *got.ImdbRating != 7.9 || *got.ImdbVotes != 12345 || *got.RottenTomatoes != 88 || got.Metacritic != nil || !got.Found {
		t.Errorf("stored = %+v %v", got, err)
	}
	if old, _ := q.GetExternalRatings(ctx, "tt-old"); old.FetchedAt.Time.Before(now.Add(-time.Minute)) {
		t.Error("a refresh didn't move fetched_at")
	}
	ids, _ = q.ListExternalRatingCandidates(ctx, gen.ListExternalRatingCandidatesParams{
		MissingBefore: params.MissingBefore, RecentYear: params.RecentYear,
		RecentBefore: params.RecentBefore, OldBefore: params.OldBefore, MaxIds: 100,
	})
	if slices.Contains(ids, "tt-new") || slices.Contains(ids, "tt-old") {
		t.Errorf("freshly fetched IDs still due: %v", ids)
	}
	bad := 11.0
	if err := q.UpsertExternalRatings(ctx, gen.UpsertExternalRatingsParams{ImdbID: "tt-x", ImdbRating: &bad, Found: true}); err == nil {
		t.Error("an IMDb rating over 10 was stored")
	}

	c, err := q.CountExternalRatingCandidates(ctx)
	if err != nil || c.WithImdbID != 6 || c.Rated != 4 {
		t.Errorf("counts = %+v %v (6 movie/show IDs; tt-new, tt-old, tt-recent, tt-oldfresh rated)", c, err)
	}
}
