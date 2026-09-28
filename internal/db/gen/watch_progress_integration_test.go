//go:build integration

// Round-trips the unified watch-state model (migration 00023): the
// trigger-maintained watch_progress rollup, manual played/unplayed marks,
// Continue Watching dismissals, Next Up, and the library watch rollups /
// filter. The rule under test everywhere: a mark overrides everything before
// it; events after it derive normally; marks never count as plays.
//
// Run with: go test -tags 'dev integration' -run WatchProgress ./internal/db/gen/
package gen_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// dbNow reads the database clock. Marks are stamped with the server's now(),
// so event times in these tests are taken relative to it rather than the
// host clock (a Docker VM clock can drift from the host's).
func dbNow(ctx context.Context, t *testing.T, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var now time.Time
	if err := pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatalf("clock_timestamp: %v", err)
	}
	return now
}

// play records one watch event at pos of dur milliseconds.
func play(ctx context.Context, t *testing.T, q *gen.Queries, user, media uuid.UUID, eventType string, pos, dur int64, at time.Time) {
	t.Helper()
	d := dur
	if _, err := q.InsertWatchEvent(ctx, gen.InsertWatchEventParams{
		UserID: user, MediaID: media, EventType: eventType,
		PositionMs: pos, DurationMs: &d,
		OccurredAt: pgtype.Timestamptz{Time: at, Valid: true},
	}); err != nil {
		t.Fatalf("InsertWatchEvent: %v", err)
	}
}

func mark(ctx context.Context, t *testing.T, q *gen.Queries, user, target uuid.UUID, targetType, state string, maxRank *int32) int64 {
	t.Helper()
	n, err := q.MarkWatchStateForTarget(ctx, gen.MarkWatchStateForTargetParams{
		UserID: user, State: state, TargetType: targetType, TargetID: target, MaxRatingRank: maxRank,
	})
	if err != nil {
		t.Fatalf("MarkWatchStateForTarget(%s %s): %v", targetType, state, err)
	}
	return n
}

func state(ctx context.Context, t *testing.T, q *gen.Queries, user, media uuid.UUID) gen.GetWatchStateRow {
	t.Helper()
	s, err := q.GetWatchState(ctx, gen.GetWatchStateParams{UserID: user, MediaID: media})
	if err != nil {
		t.Fatalf("GetWatchState: %v", err)
	}
	return s
}

// seedChild inserts a typed item under parent (uuid.Nil = top level).
func seedChild(ctx context.Context, t *testing.T, q *gen.Queries, lib, parent uuid.UUID, typ, title string, index int32, rating string) uuid.UUID {
	t.Helper()
	p := gen.CreateMediaItemParams{LibraryID: lib, Type: typ, Title: title, SortTitle: title}
	if parent != uuid.Nil {
		p.ParentID = pgtype.UUID{Bytes: parent, Valid: true}
	}
	if index >= 0 {
		i := index
		p.Index = &i
	}
	if rating != "" {
		r := rating
		p.ContentRating = &r
	}
	it, err := q.CreateMediaItem(ctx, p)
	if err != nil {
		t.Fatalf("CreateMediaItem %s %q: %v", typ, title, err)
	}
	return it.ID
}

func rank(n int32) *int32 { return &n }

// TestWatchProgress_Integration_MarksOverrideHistory covers the leaf-level
// rules and proves marks never become plays.
func TestWatchProgress_Integration_MarksOverrideHistory(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "wp-marks-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "wp-marks-lib-"+uuid.New().String()[:8])
	neverPlayed := seedMediaItem(ctx, t, q, lib, "Never Played")
	finished := seedMediaItem(ctx, t, q, lib, "Finished")
	now := dbNow(ctx, t, pool)

	play(ctx, t, q, user, finished, "stop", 570_000, 600_000, now.Add(-2*time.Hour))
	if _, err := pool.Exec(ctx, "REFRESH MATERIALIZED VIEW public.watch_plays"); err != nil {
		t.Fatalf("refresh watch_plays: %v", err)
	}
	playsBefore := topUserPlays(ctx, t, q, user)
	var eventsBefore int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM watch_events WHERE user_id = $1", user).Scan(&eventsBefore)

	// Mark watched with no history at all → watched, no resume point.
	if n := mark(ctx, t, q, user, neverPlayed, "movie", "watched", nil); n != 1 {
		t.Fatalf("mark rows = %d, want 1", n)
	}
	s := state(ctx, t, q, user, neverPlayed)
	if s.Status != "watched" || s.PositionMs != 0 || s.Resumable {
		t.Errorf("marked watched: %+v", s)
	}

	// A >90% play, then marked unwatched → unwatched (the sticky completion
	// predates the mark).
	if got := state(ctx, t, q, user, finished).Status; got != "watched" {
		t.Fatalf("before mark: %s, want watched", got)
	}
	mark(ctx, t, q, user, finished, "movie", "unwatched", nil)
	s = state(ctx, t, q, user, finished)
	if s.Status != "unwatched" || s.PositionMs != 0 || s.Resumable {
		t.Errorf("marked unwatched after a finished play: %+v", s)
	}

	// Marks write no events and no plays.
	if _, err := pool.Exec(ctx, "REFRESH MATERIALIZED VIEW public.watch_plays"); err != nil {
		t.Fatalf("refresh watch_plays: %v", err)
	}
	if got := topUserPlays(ctx, t, q, user); got != playsBefore {
		t.Errorf("plays after marks = %d, want %d (marks must not count as plays)", got, playsBefore)
	}
	var eventsAfter int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM watch_events WHERE user_id = $1", user).Scan(&eventsAfter)
	if eventsAfter != eventsBefore {
		t.Errorf("watch_events %d → %d: marks must not write events", eventsBefore, eventsAfter)
	}

	// Playing again after the unwatched mark derives normally… Event times
	// sit a few milliseconds apart just after the mark (read from the DB
	// clock), so the final mark below is strictly newer than all of them.
	after := dbNow(ctx, t, pool)
	play(ctx, t, q, user, finished, "play", 300_000, 600_000, after)
	s = state(ctx, t, q, user, finished)
	if s.Status != "in_progress" || s.PositionMs != 300_000 || !s.Resumable {
		t.Errorf("replay after unwatched mark: %+v", s)
	}
	// …including a new completion.
	play(ctx, t, q, user, finished, "stop", 590_000, 600_000, after.Add(2*time.Millisecond))
	if got := state(ctx, t, q, user, finished).Status; got != "watched" {
		t.Errorf("finished again: %s, want watched", got)
	}

	// Sticky: a rewatch from the start keeps "watched" but is resumable.
	play(ctx, t, q, user, finished, "play", 20_000, 600_000, after.Add(4*time.Millisecond))
	s = state(ctx, t, q, user, finished)
	if s.Status != "watched" || s.PositionMs != 20_000 || !s.Resumable {
		t.Errorf("rewatch: %+v", s)
	}

	// An out-of-order (older) event must not replace the latest position.
	play(ctx, t, q, user, finished, "play", 450_000, 600_000, after.Add(3*time.Millisecond))
	if got := state(ctx, t, q, user, finished).PositionMs; got != 20_000 {
		t.Errorf("late older event moved position to %d", got)
	}

	// Mark-as-watched clears the resume point of an in-progress item.
	time.Sleep(20 * time.Millisecond)
	mark(ctx, t, q, user, finished, "movie", "watched", nil)
	s = state(ctx, t, q, user, finished)
	if s.Status != "watched" || s.PositionMs != 0 || s.Resumable {
		t.Errorf("marked watched mid-rewatch: %+v", s)
	}
}

func topUserPlays(ctx context.Context, t *testing.T, q *gen.Queries, user uuid.UUID) int64 {
	t.Helper()
	rows, err := q.GetTopUsers(ctx, 30)
	if err != nil {
		t.Fatalf("GetTopUsers: %v", err)
	}
	for _, r := range rows {
		if r.UserID == user {
			return r.PlayCount
		}
	}
	return 0
}

// TestWatchProgress_Integration_ContinueWatchingDismiss covers the resumable
// rule on Continue Watching and dismissals (movie + show rollup).
func TestWatchProgress_Integration_ContinueWatchingDismiss(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "wp-cw-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "wp-cw-lib-"+uuid.New().String()[:8])
	movie := seedMediaItem(ctx, t, q, lib, "CW Movie")
	show := seedChild(ctx, t, q, lib, uuid.Nil, "show", "CW Show", -1, "")
	season := seedChild(ctx, t, q, lib, show, "season", "Season 1", 1, "")
	ep1 := seedChild(ctx, t, q, lib, season, "episode", "E1", 1, "")
	now := dbNow(ctx, t, pool)

	play(ctx, t, q, user, movie, "play", 100_000, 600_000, now.Add(-10*time.Minute))
	play(ctx, t, q, user, ep1, "play", 200_000, 600_000, now.Add(-5*time.Minute))

	cw := func() map[uuid.UUID]bool {
		t.Helper()
		rows, err := q.ListContinueWatching(ctx, gen.ListContinueWatchingParams{UserID: user, Limit: 20})
		if err != nil {
			t.Fatalf("ListContinueWatching: %v", err)
		}
		out := map[uuid.UUID]bool{}
		for _, r := range rows {
			out[r.ID] = true
			if r.ShowID.Valid {
				out[uuid.UUID(r.ShowID.Bytes)] = true
			}
		}
		return out
	}
	if got := cw(); !got[movie] || !got[show] {
		t.Fatalf("both in progress (play ticks alone) should be on CW: %v", got)
	}

	// Dismissing an episode hides its show's tile; the movie stays.
	if n, err := q.DismissContinueWatching(ctx, gen.DismissContinueWatchingParams{UserID: user, MediaID: ep1}); err != nil || n != 1 {
		t.Fatalf("dismiss episode: n=%d err=%v", n, err)
	}
	if got := cw(); got[show] || !got[movie] {
		t.Errorf("after episode dismiss: %v", got)
	}
	var key uuid.UUID
	_ = pool.QueryRow(ctx, "SELECT media_id FROM continue_watching_dismissals WHERE user_id = $1", user).Scan(&key)
	if key != show {
		t.Errorf("episode dismissal stored against %s, want the show", key)
	}

	// Dismiss the movie; new activity after the dismissal brings it back.
	if _, err := q.DismissContinueWatching(ctx, gen.DismissContinueWatchingParams{UserID: user, MediaID: movie}); err != nil {
		t.Fatal(err)
	}
	if cw()[movie] {
		t.Fatal("dismissed movie still on CW")
	}
	play(ctx, t, q, user, movie, "play", 150_000, 600_000, dbNow(ctx, t, pool))
	if !cw()[movie] {
		t.Error("movie should return after a progress event newer than the dismissal")
	}

	// Marked watched → not resumable → off CW.
	mark(ctx, t, q, user, movie, "movie", "watched", nil)
	if cw()[movie] {
		t.Error("a movie marked watched must leave Continue Watching")
	}

	// Recommendations read the same state: the marked movie is a seed.
	seeds, err := q.ListSeedItemsForUser(ctx, gen.ListSeedItemsForUserParams{UserID: user, Limit: 5})
	if err != nil {
		t.Fatalf("ListSeedItemsForUser: %v", err)
	}
	if len(seeds) != 1 || seeds[0].ID != movie {
		t.Errorf("seeds = %v, want the watched movie", seeds)
	}
}

// TestWatchProgress_Integration_NextUpAndShowMarks covers ListNextUp, the
// Up Next episode listing and show/season marks.
func TestWatchProgress_Integration_NextUpAndShowMarks(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "wp-nu-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "wp-nu-lib-"+uuid.New().String()[:8])
	show := seedChild(ctx, t, q, lib, uuid.Nil, "show", "NU Show", -1, "TV-14")
	specials := seedChild(ctx, t, q, lib, show, "season", "Specials", 0, "")
	s1 := seedChild(ctx, t, q, lib, show, "season", "Season 1", 1, "")
	s2 := seedChild(ctx, t, q, lib, show, "season", "Season 2", 2, "")
	sp1 := seedChild(ctx, t, q, lib, specials, "episode", "SP1", 1, "TV-14")
	s1e1 := seedChild(ctx, t, q, lib, s1, "episode", "S1E1", 1, "TV-14")
	s1e2 := seedChild(ctx, t, q, lib, s1, "episode", "S1E2", 2, "TV-14")
	s1e3 := seedChild(ctx, t, q, lib, s1, "episode", "S1E3", 3, "TV-MA")
	s2e1 := seedChild(ctx, t, q, lib, s2, "episode", "S2E1", 1, "TV-14")
	now := dbNow(ctx, t, pool)

	nextUp := func(maxRank *int32) []gen.ListNextUpRow {
		t.Helper()
		rows, err := q.ListNextUp(ctx, gen.ListNextUpParams{UserID: user, MaxRatingRank: maxRank, ResultLimit: 20})
		if err != nil {
			t.Fatalf("ListNextUp: %v", err)
		}
		return rows
	}

	if got := nextUp(nil); len(got) != 0 {
		t.Fatalf("nothing watched → no Next Up, got %d", len(got))
	}

	// A watched special alone anchors nothing useful: next is the first
	// regular episode, specials are skipped.
	play(ctx, t, q, user, sp1, "stop", 590_000, 600_000, now.Add(-4*time.Hour))
	if got := nextUp(nil); len(got) != 1 || got[0].ID != s1e1 {
		t.Fatalf("after a special: %v, want S1E1", got)
	}

	play(ctx, t, q, user, s1e1, "stop", 590_000, 600_000, now.Add(-3*time.Hour))
	play(ctx, t, q, user, s1e2, "stop", 590_000, 600_000, now.Add(-2*time.Hour))
	got := nextUp(nil)
	if len(got) != 1 || got[0].ID != s1e3 || got[0].ShowID != show ||
		got[0].SeasonNumber == nil || *got[0].SeasonNumber != 1 || *got[0].EpisodeNumber != 3 {
		t.Fatalf("next up = %+v, want S1E3", got)
	}
	// S1E3 is TV-MA: over a TV-14 ceiling the next visible episode is S2E1.
	if got := nextUp(rank(2)); len(got) != 1 || got[0].ID != s2e1 {
		t.Errorf("TV-14 ceiling: %v, want S2E1", got)
	}
	// Show above the ceiling → the whole row is gone.
	if got := nextUp(rank(1)); len(got) != 0 {
		t.Errorf("PG ceiling: %d tiles, want 0 (show is TV-14)", len(got))
	}

	// In progress → it's on Continue Watching TV instead.
	play(ctx, t, q, user, s1e3, "play", 100_000, 600_000, now.Add(-time.Hour))
	if got := nextUp(nil); len(got) != 0 {
		t.Errorf("show with a resumable episode must not be in Next Up, got %d", len(got))
	}

	// Mark the season watched (ceiling-scoped: TV-MA S1E3 is skipped under
	// TV-14, so S1E3 stays resumable) — then without a ceiling.
	if n := mark(ctx, t, q, user, s1, "season", "watched", rank(2)); n != 2 {
		t.Errorf("season mark under TV-14 touched %d episodes, want 2", n)
	}
	// S1E1/S1E2 already carry the watched mark, so only S1E3 is written.
	if n := mark(ctx, t, q, user, s1, "season", "watched", nil); n != 1 {
		t.Errorf("season mark touched %d episodes, want 1 (only the TV-MA S1E3 changes)", n)
	}
	if got := nextUp(nil); len(got) != 1 || got[0].ID != s2e1 {
		t.Fatalf("after marking S1 watched: %v, want S2E1", got)
	}

	// Up Next listing for the show: play order, statuses, ceiling.
	eps, err := q.ListUpNextEpisodes(ctx, gen.ListUpNextEpisodesParams{UserID: user, ContainerID: show})
	if err != nil {
		t.Fatalf("ListUpNextEpisodes: %v", err)
	}
	wantOrder := []uuid.UUID{sp1, s1e1, s1e2, s1e3, s2e1}
	if len(eps) != len(wantOrder) {
		t.Fatalf("episodes = %d, want %d", len(eps), len(wantOrder))
	}
	for i, id := range wantOrder {
		if eps[i].ID != id {
			t.Errorf("episode %d = %s, want %s", i, eps[i].Title, id)
		}
	}
	if eps[3].Status != "watched" || eps[3].Resumable || eps[4].Status != "unwatched" || eps[1].SeasonID != s1 {
		t.Errorf("states: %+v / %+v", eps[3], eps[4])
	}
	capped, _ := q.ListUpNextEpisodes(ctx, gen.ListUpNextEpisodesParams{UserID: user, ContainerID: show, MaxRatingRank: rank(2)})
	if len(capped) != 4 {
		t.Errorf("TV-14 ceiling: %d episodes, want 4 (TV-MA S1E3 hidden)", len(capped))
	}
	seasonOnly, _ := q.ListUpNextEpisodes(ctx, gen.ListUpNextEpisodesParams{UserID: user, ContainerID: s2})
	if len(seasonOnly) != 1 || seasonOnly[0].ID != s2e1 {
		t.Errorf("season scope = %v", seasonOnly)
	}

	// Whole show watched → caught up; a new episode later brings it back.
	mark(ctx, t, q, user, show, "show", "watched", nil)
	if got := nextUp(nil); len(got) != 0 {
		t.Errorf("caught up: %d tiles, want 0", len(got))
	}
	s2e2 := seedChild(ctx, t, q, lib, s2, "episode", "S2E2", 2, "TV-14")
	if got := nextUp(nil); len(got) != 1 || got[0].ID != s2e2 {
		t.Errorf("new episode after catching up: %v, want S2E2", got)
	}

	// Dismissing the tile (by episode) hides the show.
	if _, err := q.DismissContinueWatching(ctx, gen.DismissContinueWatchingParams{UserID: user, MediaID: s2e2}); err != nil {
		t.Fatal(err)
	}
	if got := nextUp(nil); len(got) != 0 {
		t.Errorf("dismissed show still in Next Up")
	}

	// Mark the show unwatched → every episode unwatched, nothing to offer.
	mark(ctx, t, q, user, show, "show", "unwatched", nil)
	for _, id := range []uuid.UUID{sp1, s1e1, s1e3, s2e1} {
		if s := state(ctx, t, q, user, id); s.Status != "unwatched" || s.Resumable {
			t.Errorf("after show unmark %s: %+v", id, s)
		}
	}
}

// TestWatchProgress_Integration_LibraryRollups covers the watch filter on the
// library listing, the batched per-page summaries, the random pick and the
// Plan to Watch row.
func TestWatchProgress_Integration_LibraryRollups(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "wp-lib-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "wp-lib-lib-"+uuid.New().String()[:8])
	now := dbNow(ctx, t, pool)

	type showIDs struct{ show, season, e1, e2 uuid.UUID }
	mk := func(title string) showIDs {
		sh := seedChild(ctx, t, q, lib, uuid.Nil, "show", title, -1, "TV-14")
		se := seedChild(ctx, t, q, lib, sh, "season", title+" S1", 1, "")
		return showIDs{sh, se,
			seedChild(ctx, t, q, lib, se, "episode", title+" E1", 1, "TV-14"),
			seedChild(ctx, t, q, lib, se, "episode", title+" E2", 2, "TV-MA")}
	}
	done, partial, fresh := mk("Alpha Done"), mk("Bravo Partial"), mk("Charlie Fresh")
	mark(ctx, t, q, user, done.show, "show", "watched", nil)
	play(ctx, t, q, user, partial.e1, "play", 50_000, 600_000, now.Add(-time.Minute))

	list := func(watch string) []uuid.UUID {
		t.Helper()
		w := watch
		rows, err := q.ListMediaItemsByTitle(ctx, gen.ListMediaItemsByTitleParams{
			LibraryID: lib, Type: "show", Limit: 50, Watch: &w, WatchUserID: user,
		})
		if err != nil {
			t.Fatalf("ListMediaItemsByTitle(%s): %v", watch, err)
		}
		n, err := q.CountMediaItemsFiltered(ctx, gen.CountMediaItemsFilteredParams{
			LibraryID: lib, Type: "show", Watch: &w, WatchUserID: user,
		})
		if err != nil {
			t.Fatalf("CountMediaItemsFiltered(%s): %v", watch, err)
		}
		if n != int64(len(rows)) {
			t.Errorf("%s: count %d != page %d", watch, n, len(rows))
		}
		out := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			out[i] = r.ID
		}
		return out
	}
	for watch, want := range map[string]uuid.UUID{"watched": done.show, "in_progress": partial.show, "unwatched": fresh.show} {
		if got := list(watch); len(got) != 1 || got[0] != want {
			t.Errorf("watch=%s → %v, want [%s]", watch, got, want)
		}
	}
	// No filter → all three.
	all, _ := q.CountMediaItemsFiltered(ctx, gen.CountMediaItemsFilteredParams{LibraryID: lib, Type: "show", WatchUserID: user})
	if all != 3 {
		t.Errorf("unfiltered count = %d, want 3", all)
	}
	// The rollup respects the ceiling: under TV-14 only E1 counts, so
	// Bravo (E1 in progress) stays in_progress and Alpha stays watched.
	w := "watched"
	capped, _ := q.CountMediaItemsFiltered(ctx, gen.CountMediaItemsFilteredParams{
		LibraryID: lib, Type: "show", Watch: &w, WatchUserID: user, MaxRatingRank: rank(2),
	})
	if capped != 1 {
		t.Errorf("watched under TV-14 = %d, want 1", capped)
	}

	// Batched page summaries.
	sums, err := q.ListItemWatchSummaries(ctx, gen.ListItemWatchSummariesParams{
		UserID:   user,
		MediaIds: []uuid.UUID{done.show, partial.show, partial.season, partial.e1, fresh.e2},
	})
	if err != nil {
		t.Fatalf("ListItemWatchSummaries: %v", err)
	}
	by := map[uuid.UUID]gen.ListItemWatchSummariesRow{}
	for _, s := range sums {
		by[s.ID] = s
	}
	if s := by[done.show]; s.LeafCount != 2 || s.WatchedCount != 2 {
		t.Errorf("done show: %+v", s)
	}
	if s := by[partial.show]; s.LeafCount != 2 || s.WatchedCount != 0 {
		t.Errorf("partial show: %+v", s)
	}
	if s := by[partial.season]; s.LeafCount != 2 {
		t.Errorf("partial season: %+v", s)
	}
	if s := by[partial.e1]; s.Status != "in_progress" || s.PositionMs != 50_000 || !s.Resumable || s.LeafCount != 0 {
		t.Errorf("partial e1: %+v", s)
	}
	if s := by[fresh.e2]; s.Status != "unwatched" {
		t.Errorf("fresh e2: %+v", s)
	}
	cappedSums, _ := q.ListItemWatchSummaries(ctx, gen.ListItemWatchSummariesParams{
		UserID: user, MaxRatingRank: rank(2), MediaIds: []uuid.UUID{done.show},
	})
	if len(cappedSums) != 1 || cappedSums[0].LeafCount != 1 {
		t.Errorf("TV-14 leaf count = %+v, want 1 (TV-MA E2 hidden)", cappedSums)
	}

	// Random pick honours the watch filter; nothing matching → ErrNoRows.
	un := "unwatched"
	pick, err := q.PickRandomLibraryItem(ctx, gen.PickRandomLibraryItemParams{
		LibraryID: lib, ItemType: "show", Watch: &un, WatchUserID: user,
	})
	if err != nil || pick.ID != fresh.show || pick.Type != "show" {
		t.Errorf("random unwatched = %+v err=%v, want Charlie", pick, err)
	}
	_, err = q.PickRandomLibraryItem(ctx, gen.PickRandomLibraryItemParams{LibraryID: lib, ItemType: "movie", WatchUserID: user})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("no movies: err = %v, want ErrNoRows", err)
	}

	// Plan to Watch leaves out what's already fully watched.
	for _, id := range []uuid.UUID{done.show, fresh.show} {
		if _, err := q.UpsertUserWatchStatus(ctx, gen.UpsertUserWatchStatusParams{UserID: user, MediaItemID: id, Status: "plan_to_watch"}); err != nil {
			t.Fatalf("UpsertUserWatchStatus: %v", err)
		}
	}
	ptw, err := q.ListPlanToWatchHub(ctx, gen.ListPlanToWatchHubParams{UserID: user, ResultLimit: 20})
	if err != nil {
		t.Fatalf("ListPlanToWatchHub: %v", err)
	}
	if len(ptw) != 1 || ptw[0].ID != fresh.show {
		t.Errorf("plan to watch = %v, want only Charlie", ptw)
	}
	// Over the ceiling → gone too.
	ptw, _ = q.ListPlanToWatchHub(ctx, gen.ListPlanToWatchHubParams{UserID: user, ResultLimit: 20, MaxRatingRank: rank(1)})
	if len(ptw) != 0 {
		t.Errorf("PG ceiling plan to watch = %d, want 0", len(ptw))
	}
}
