//go:build integration

// A user's own stats against the real watch_plays matview: the aggregates
// are right, and every one of them applies the caller's library access,
// rating ceiling and label restriction. Also covers the digest's new-items
// query, which shares the gating rules and the migration.
//
// Run with: go test -tags=integration -run Integration ./internal/userstats/
package userstats_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/itemlabel"
	"github.com/onscreen/onscreen/internal/testdb"
	"github.com/onscreen/onscreen/internal/userstats"
)

type fixture struct {
	pool *pgxpool.Pool
	q    *gen.Queries
	user uuid.UUID
	// open is public; locked is private with no grant for user.
	open, locked uuid.UUID
	ids          map[string]uuid.UUID
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func (f *fixture) lib(t *testing.T, name string, private bool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO libraries (name, type, scan_paths, is_private) VALUES ($1, 'movie', ARRAY['/tmp/'||$1], $2) RETURNING id`,
		name, private).Scan(&id); err != nil {
		t.Fatalf("library: %v", err)
	}
	return id
}

// item inserts a media item; parent may be "" and rating "" (NULL).
func (f *fixture) item(t *testing.T, key, typ string, lib uuid.UUID, parent, rating string, labels []string, genres []string, durMS int64) uuid.UUID {
	t.Helper()
	var p *uuid.UUID
	if parent != "" {
		id := f.ids[parent]
		p = &id
	}
	var r *string
	if rating != "" {
		r = &rating
	}
	if labels == nil {
		labels = []string{}
	}
	var id uuid.UUID
	if err := f.pool.QueryRow(context.Background(),
		`INSERT INTO media_items (library_id, type, title, sort_title, parent_id, content_rating, labels, genres, duration_ms, year)
		 VALUES ($1, $2, $3, $3, $4, $5, $6, $7, $8, 2020) RETURNING id`,
		lib, typ, key, p, r, labels, genres, durMS).Scan(&id); err != nil {
		t.Fatalf("item %s: %v", key, err)
	}
	f.ids[key] = id
	return id
}

func (f *fixture) play(t *testing.T, key string, at time.Time, posMS int64, client string) {
	t.Helper()
	mustExec(t, f.pool,
		`INSERT INTO watch_events (user_id, media_id, event_type, position_ms, client_name, occurred_at)
		 VALUES ($1, $2, 'stop', $3, $4, $5)`, f.user, f.ids[key], posMS, client, at)
}

func setup(t *testing.T) *fixture {
	pool := testdb.New(t)
	f := &fixture{pool: pool, q: gen.New(pool), ids: map[string]uuid.UUID{}}
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (username, email) VALUES ('stats-'||substr(gen_random_uuid()::text,1,8), 'u@example.com') RETURNING id`).Scan(&f.user); err != nil {
		t.Fatalf("user: %v", err)
	}
	f.open = f.lib(t, "open-"+uuid.NewString()[:8], false)
	f.locked = f.lib(t, "locked-"+uuid.NewString()[:8], true)

	hour := int64(3_600_000)
	f.item(t, "Heat", "movie", f.open, "", "R", nil, []string{"Crime"}, 2*hour)
	f.item(t, "Paddington", "movie", f.open, "", "PG", nil, []string{"Family"}, hour)
	f.item(t, "Scream", "movie", f.open, "", "R", []string{"horror"}, []string{"Horror"}, hour)
	f.item(t, "Locked Film", "movie", f.locked, "", "PG", nil, []string{"Drama"}, hour)
	f.item(t, "Bluey", "show", f.open, "", "TV-Y", nil, []string{"Animation"}, 0)
	f.item(t, "Bluey S1", "season", f.open, "Bluey", "", nil, nil, 0)
	f.item(t, "Bluey E1", "episode", f.open, "Bluey S1", "", nil, nil, hour/4)
	f.item(t, "Bluey E2", "episode", f.open, "Bluey S1", "", nil, nil, hour/4)
	f.item(t, "Joni", "artist", f.open, "", "", nil, []string{"Folk"}, 0)
	f.item(t, "Blue", "album", f.open, "Joni", "", nil, nil, 0)
	f.item(t, "River", "track", f.open, "Blue", "", nil, nil, 240_000)
	f.item(t, "Carey", "track", f.open, "Blue", "", nil, nil, 180_000)
	f.item(t, "A Book", "book", f.open, "", "", nil, nil, 0)

	y := time.Now().Year()
	at := func(m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, time.UTC) }
	f.play(t, "Paddington", at(1, 2, 18), hour, "Fire TV")                            // finished; first play of the year
	f.play(t, "Heat", at(1, 3, 20), hour, "Fire TV")                                  // half way
	f.play(t, "Scream", at(1, 4, 21), hour, "Web")                                    // label-blocked below
	f.play(t, "Locked Film", at(1, 5, 21), hour, "Web")                               // no library access
	f.play(t, "Bluey E1", at(1, 6, 8), hour/4, "Phone")                               // finished
	f.play(t, "Bluey E2", at(1, 7, 8), hour/4, "Phone")                               // finished
	f.play(t, "River", at(1, 3, 9), 240_000, "Desktop")                               // finished
	f.play(t, "Carey", at(1, 4, 9), 60_000, "Desktop")                                // partial
	f.play(t, "Heat", time.Date(y-1, 6, 1, 20, 0, 0, 0, time.UTC), 2*hour, "Fire TV") // last year
	mustExec(t, pool, `INSERT INTO watch_progress (user_id, media_id, mark_state, marked_at) VALUES ($1, $2, 'watched', $3)`,
		f.user, f.ids["A Book"], at(2, 1, 12))
	mustExec(t, pool, `REFRESH MATERIALIZED VIEW public.watch_plays`)
	return f
}

func (f *fixture) viewer(t *testing.T, rating string, labels itemlabel.Filter) userstats.Viewer {
	t.Helper()
	ids, err := f.q.ListAllowedLibraryIDsForUser(context.Background(), f.user)
	if err != nil {
		t.Fatal(err)
	}
	return userstats.Viewer{UserID: f.user, LibraryIDs: ids, MaxContentRating: rating, Labels: labels}
}

func titles(ts []userstats.Title) map[string]int64 {
	out := map[string]int64{}
	for _, t := range ts {
		out[t.Title] = t.Plays
	}
	return out
}

func TestUserStats_Integration(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	svc := userstats.New(f.q)
	now := time.Now()
	r, err := userstats.ParseRange("year:"+itoa(now.Year()), now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}

	// Unrestricted except for library access: the locked library's film is
	// out, everything else is in.
	st, err := svc.Compute(ctx, f.viewer(t, "", itemlabel.Filter{}), r, "UTC", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if st.Totals.Plays != 7 {
		t.Errorf("plays = %d, want 7 (locked library excluded)", st.Totals.Plays)
	}
	// Video: Paddington 1h + Heat 1h + Scream 1h + 2 × 15 min = 3.5h.
	if st.Totals.HoursWatched != 3.5 {
		t.Errorf("hours watched = %v, want 3.5", st.Totals.HoursWatched)
	}
	// Audio: 4 min + 1 min.
	if st.Totals.AudioMS != 300_000 {
		t.Errorf("listen ms = %d, want 300000", st.Totals.AudioMS)
	}
	// Finished: Paddington, Scream (full position), both episodes, River.
	if st.Totals.ItemsFinished != 5 {
		t.Errorf("items finished = %d, want 5", st.Totals.ItemsFinished)
	}
	if st.Totals.BooksRead != 1 {
		t.Errorf("books read = %d, want 1", st.Totals.BooksRead)
	}
	if got := titles(st.TopTitles.Shows); got["Bluey"] != 2 {
		t.Errorf("top shows = %v, want Bluey with 2 plays", got)
	}
	if got := titles(st.TopTitles.Artists); got["Joni"] != 2 {
		t.Errorf("top artists = %v", got)
	}
	if got := titles(st.TopTitles.Albums); got["Blue"] != 2 || st.TopTitles.Albums[0].ParentTitle != "Joni" {
		t.Errorf("top albums = %+v", st.TopTitles.Albums)
	}
	if got := titles(st.TopTitles.Movies); got["Locked Film"] != 0 || got["Heat"] != 1 {
		t.Errorf("top movies = %v", got)
	}
	genres := map[string]bool{}
	for _, g := range st.TopGenres {
		genres[g.Genre] = true
	}
	// Episodes take the show's genre, tracks the artist's (two levels up).
	if !genres["Animation"] || !genres["Folk"] || genres["Drama"] {
		t.Errorf("genres = %v", genres)
	}
	// Jan 2-4, then a gap on the 5th (its only play is in the locked library).
	if st.LongestStreak.Days != 3 || st.LongestStreak.End[5:] != "01-04" {
		t.Errorf("streak = %+v, want 3 days ending Jan 4", st.LongestStreak)
	}
	if len(st.Devices) == 0 || st.Devices[0].Device != "Fire TV" && st.Devices[0].Device != "Desktop" && st.Devices[0].Device != "Phone" {
		t.Errorf("devices = %+v", st.Devices)
	}

	// A PG-13 ceiling plus a blocked label: Heat (R), Scream (R + horror) and
	// the unrated tracks drop out; Bluey (TV-Y via the show) stays.
	capped, err := svc.Compute(ctx, f.viewer(t, "PG-13", itemlabel.Filter{Blocked: []string{"horror"}}), r, "UTC", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if capped.Totals.Plays != 3 {
		t.Errorf("capped plays = %d, want 3 (Paddington + 2 Bluey)", capped.Totals.Plays)
	}
	if got := titles(capped.TopTitles.Movies); got["Heat"] != 0 || got["Scream"] != 0 || got["Paddington"] != 1 {
		t.Errorf("capped movies = %v", got)
	}
	if len(capped.TopTitles.Artists) != 0 || len(capped.Devices) != 2 {
		t.Errorf("capped artists %v devices %v", capped.TopTitles.Artists, capped.Devices)
	}

	// An allowed-labels list nobody carries shows nothing.
	none, err := svc.Compute(ctx, f.viewer(t, "", itemlabel.Filter{Allowed: []string{"kids"}}), r, "UTC", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if none.Totals.Plays != 0 || len(none.TopTitles.Movies) != 0 {
		t.Errorf("allowed-labels viewer saw %d plays", none.Totals.Plays)
	}

	// Year in review: first play, new artist, last year's comparison.
	yr, err := svc.YearInReview(ctx, f.viewer(t, "", itemlabel.Filter{}), now.Year(), now, "UTC", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if yr.FirstPlay == nil || yr.FirstPlay.Title != "Paddington" {
		t.Errorf("first play = %+v", yr.FirstPlay)
	}
	if yr.NewArtists.Count != 1 || len(yr.NewArtists.Top) != 1 || yr.NewArtists.Top[0].Title != "Joni" {
		t.Errorf("new artists = %+v", yr.NewArtists)
	}
	if yr.PreviousYear == nil || yr.PreviousYear.Totals.Plays != 1 || yr.PreviousYear.Totals.HoursWatched != 2 {
		t.Errorf("previous year = %+v", yr.PreviousYear)
	}
}

func TestDigestNewItems_Integration(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	ids, err := f.q.ListAllowedLibraryIDsForUser(ctx, f.user)
	if err != nil {
		t.Fatal(err)
	}
	since := pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	until := pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
	kinds := func(rank *int32, blocked []string) map[string]map[string]int64 {
		rows, err := f.q.ListDigestNewItems(ctx, gen.ListDigestNewItemsParams{
			PerKind: 10, Since: since, Until: until, LibraryIds: ids, MaxRatingRank: rank, BlockedLabels: blocked,
		})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]map[string]int64{}
		for _, r := range rows {
			if out[r.Kind] == nil {
				out[r.Kind] = map[string]int64{}
			}
			out[r.Kind][r.Title] = r.ItemCount
		}
		return out
	}
	got := kinds(nil, nil)
	if got["show"]["Bluey"] != 2 {
		t.Errorf("show entry = %v, want Bluey with 2 new episodes", got["show"])
	}
	if _, ok := got["movie"]["Locked Film"]; ok {
		t.Error("digest listed a film from a library the user can't see")
	}
	if got["movie"]["Heat"] != 1 || got["album"]["Blue"] != 1 || got["book"]["A Book"] != 1 {
		t.Errorf("entries = %v", got)
	}
	rank := int32(2)
	got = kinds(&rank, []string{"horror"})
	if _, ok := got["movie"]["Heat"]; ok {
		t.Error("an R film reached a PG-13 recipient")
	}
	if _, ok := got["movie"]["Scream"]; ok {
		t.Error("a label-blocked film reached the recipient")
	}
	if got["movie"]["Paddington"] != 1 || got["show"]["Bluey"] != 2 {
		t.Errorf("capped entries = %v", got)
	}

	// Claiming a period is once-only; releasing hands it back.
	if _, err := f.q.SetDigestFrequency(ctx, gen.SetDigestFrequencyParams{UserID: f.user, Frequency: "weekly"}); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int64{1, 0} {
		n, err := f.q.ClaimDigestPeriod(ctx, gen.ClaimDigestPeriodParams{Period: "2026-W42", UserID: f.user})
		if err != nil || n != want {
			t.Fatalf("claim %d = %d, %v; want %d", i, n, err, want)
		}
	}
	if err := f.q.ReleaseDigestPeriod(ctx, gen.ReleaseDigestPeriodParams{UserID: f.user, Period: "2026-W42"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.q.ClaimDigestPeriod(ctx, gen.ClaimDigestPeriodParams{Period: "2026-W42", UserID: f.user}); n != 1 {
		t.Error("released period could not be claimed again")
	}
	if n, _ := f.q.UnsubscribeDigest(ctx, f.user); n != 1 {
		t.Error("unsubscribe changed nothing")
	}
	rec, err := f.q.ListDigestRecipients(ctx)
	if err != nil || len(rec) != 0 {
		t.Errorf("recipients after unsubscribe = %v, %v", rec, err)
	}
}

func itoa(n int) string {
	return time.Date(n, 1, 1, 0, 0, 0, 0, time.UTC).Format("2006")
}
