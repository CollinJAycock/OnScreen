//go:build integration

// Round-trips migration 00044 (multi-episode files and episode order):
// SetEpisodeIndexEnd, and every consumer that counts episodes treating an
// E01–E02 item's index..index_end as present — Next Up (E03 after it), Up
// Next, the library rollups (leaf / watched counts), and the owned-episode
// counts season requests read — plus the show / library episode order
// columns and their queries.
//
// Run with: go test -tags 'dev integration' -run EpisodeNumbering ./internal/db/gen/
package gen_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func setIndexEnd(ctx context.Context, t *testing.T, q *gen.Queries, id uuid.UUID, end *int32) int64 {
	t.Helper()
	n, err := q.SetEpisodeIndexEnd(ctx, gen.SetEpisodeIndexEndParams{ID: id, IndexEnd: end})
	if err != nil {
		t.Fatalf("SetEpisodeIndexEnd: %v", err)
	}
	return n
}

func enI32(n int32) *int32 { return &n }

func TestEpisodeNumbering_Integration_RangeConsumers(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "en-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "en-lib-"+uuid.New().String()[:8])
	show := seedChild(ctx, t, q, lib, uuid.Nil, "show", "Range Show", -1, "")
	if _, err := pool.Exec(ctx, `UPDATE media_items SET tmdb_id = 4242 WHERE id = $1`, show); err != nil {
		t.Fatal(err)
	}
	s1 := seedChild(ctx, t, q, lib, show, "season", "Season 1", 1, "")
	e12 := seedChild(ctx, t, q, lib, s1, "episode", "Pilot / Part 2", 1, "")
	e3 := seedChild(ctx, t, q, lib, s1, "episode", "Three", 3, "")
	e4 := seedChild(ctx, t, q, lib, s1, "episode", "Four", 4, "")
	for _, ep := range []uuid.UUID{e12, e3, e4} {
		seedMediaFile(ctx, t, q, ep, "/tv/"+uuid.New().String()+".mkv")
	}

	// index_end: set, idempotent, episodes only.
	if n := setIndexEnd(ctx, t, q, e12, enI32(2)); n != 1 {
		t.Fatalf("set index_end = %d rows, want 1", n)
	}
	if n := setIndexEnd(ctx, t, q, e12, enI32(2)); n != 0 {
		t.Errorf("unchanged index_end rewrote %d rows, want 0", n)
	}
	if n := setIndexEnd(ctx, t, q, s1, enI32(2)); n != 0 {
		t.Errorf("index_end on a season wrote %d rows, want 0", n)
	}
	got, err := q.GetMediaItem(ctx, e12)
	if err != nil || got.IndexEnd == nil || *got.IndexEnd != 2 {
		t.Fatalf("GetMediaItem index_end = %v (err %v), want 2", got.IndexEnd, err)
	}
	kids, err := q.ListMediaItemChildren(ctx, pgUUID(s1))
	if err != nil || len(kids) != 3 || kids[0].IndexEnd == nil || *kids[0].IndexEnd != 2 || kids[1].IndexEnd != nil {
		t.Fatalf("children index_end = %+v (err %v)", kids, err)
	}

	// Owned episode counts: the range holds E1 and E2 → 4 episodes.
	counts, err := q.ListOwnedEpisodeCountsByShowTMDB(ctx, gen.ListOwnedEpisodeCountsByShowTMDBParams{TmdbIds: []int32{4242}})
	if err != nil {
		t.Fatalf("ListOwnedEpisodeCountsByShowTMDB: %v", err)
	}
	if len(counts) != 1 || counts[0].EpisodeCount != 4 {
		t.Errorf("owned counts = %+v, want season 1: 4", counts)
	}
	// A separate E02 version next to the range counts once.
	e2 := seedChild(ctx, t, q, lib, s1, "episode", "Two", 2, "")
	seedMediaFile(ctx, t, q, e2, "/tv/"+uuid.New().String()+".mkv")
	if counts, _ = q.ListOwnedEpisodeCountsByShowTMDB(ctx, gen.ListOwnedEpisodeCountsByShowTMDBParams{TmdbIds: []int32{4242}}); len(counts) != 1 || counts[0].EpisodeCount != 4 {
		t.Errorf("owned counts with a duplicate E02 = %+v, want 4", counts)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = now() WHERE id = $1`, e2); err != nil {
		t.Fatal(err)
	}

	// Library rollups count episodes, not files.
	summaries := func() map[uuid.UUID]gen.ListItemWatchSummariesRow {
		t.Helper()
		rows, err := q.ListItemWatchSummaries(ctx, gen.ListItemWatchSummariesParams{UserID: user, MediaIds: []uuid.UUID{show, s1, e12}})
		if err != nil {
			t.Fatalf("ListItemWatchSummaries: %v", err)
		}
		out := map[uuid.UUID]gen.ListItemWatchSummariesRow{}
		for _, r := range rows {
			out[r.ID] = r
		}
		return out
	}
	sum := summaries()
	if sum[show].LeafCount != 4 || sum[s1].LeafCount != 4 || sum[e12].LeafCount != 0 {
		t.Errorf("leaf counts show=%d season=%d leaf=%d, want 4 4 0", sum[show].LeafCount, sum[s1].LeafCount, sum[e12].LeafCount)
	}

	// Up Next carries the range end.
	up, err := q.ListUpNextEpisodes(ctx, gen.ListUpNextEpisodesParams{UserID: user, ContainerID: show})
	if err != nil || len(up) != 3 || up[0].EpisodeNumberEnd == nil || *up[0].EpisodeNumberEnd != 2 || up[1].EpisodeNumberEnd != nil {
		t.Fatalf("up next rows = %+v (err %v)", up, err)
	}

	// Watch the E01–E02 file: Next Up is E03, and two episodes are watched.
	now := dbNow(ctx, t, pool)
	play(ctx, t, q, user, e12, "stop", 2_590_000, 2_600_000, now.Add(-time.Hour))
	next, err := q.ListNextUp(ctx, gen.ListNextUpParams{UserID: user, ResultLimit: 10})
	if err != nil {
		t.Fatalf("ListNextUp: %v", err)
	}
	if len(next) != 1 || next[0].ID != e3 || next[0].EpisodeNumberEnd != nil {
		t.Fatalf("next up = %+v, want E03", next)
	}
	sum = summaries()
	if sum[s1].WatchedCount != 2 || sum[show].WatchedCount != 2 {
		t.Errorf("watched counts season=%d show=%d, want 2 2", sum[s1].WatchedCount, sum[show].WatchedCount)
	}

	// A range ending past the next file's start still anchors at its end:
	// with E03–E04 as one file, watching it leaves nothing next.
	if n := setIndexEnd(ctx, t, q, e3, enI32(4)); n != 1 {
		t.Fatal("set E03–E04")
	}
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = now() WHERE id = $1`, e4); err != nil {
		t.Fatal(err)
	}
	play(ctx, t, q, user, e3, "stop", 2_590_000, 2_600_000, now.Add(-30*time.Minute))
	if next, _ = q.ListNextUp(ctx, gen.ListNextUpParams{UserID: user, ResultLimit: 10}); len(next) != 0 {
		t.Errorf("next up after the last range = %+v, want none", next)
	}

	// Clearing the range.
	if n := setIndexEnd(ctx, t, q, e3, nil); n != 1 {
		t.Errorf("clear index_end = %d rows, want 1", n)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_items SET index_end = 0 WHERE id = $1`, e3); err == nil {
		t.Errorf("index_end 0 accepted, want the check constraint to refuse it")
	}
}

func TestEpisodeNumbering_Integration_EpisodeOrder(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	lib := seedLibrary(ctx, t, q, "eo-lib-"+uuid.New().String()[:8])
	if order, err := q.GetLibraryEpisodeOrder(ctx, lib); err != nil || order != "aired" {
		t.Fatalf("default library order = %q (err %v), want aired", order, err)
	}
	upd, err := q.UpdateLibrary(ctx, gen.UpdateLibraryParams{
		ID: lib, Name: "eo", ScanPaths: []string{"/tmp/eo"}, Agent: "tmdb", Language: "en",
		ScanInterval: time.Hour, MetadataRefreshInterval: time.Hour, EpisodeOrder: enStr("dvd"),
	})
	if err != nil || upd.EpisodeOrder != "dvd" {
		t.Fatalf("UpdateLibrary episode_order = %q (err %v), want dvd", upd.EpisodeOrder, err)
	}
	// NULL keeps it.
	upd, err = q.UpdateLibrary(ctx, gen.UpdateLibraryParams{
		ID: lib, Name: "eo2", ScanPaths: []string{"/tmp/eo"}, Agent: "tmdb", Language: "en",
		ScanInterval: time.Hour, MetadataRefreshInterval: time.Hour,
	})
	if err != nil || upd.EpisodeOrder != "dvd" {
		t.Fatalf("UpdateLibrary without episode_order = %q (err %v), want dvd kept", upd.EpisodeOrder, err)
	}
	if _, err := q.UpdateLibrary(ctx, gen.UpdateLibraryParams{
		ID: lib, Name: "eo3", ScanPaths: []string{"/tmp/eo"}, Agent: "tmdb", Language: "en",
		ScanInterval: time.Hour, MetadataRefreshInterval: time.Hour, EpisodeOrder: enStr("story"),
	}); err == nil {
		t.Errorf("invalid library episode_order accepted")
	}

	a := seedChild(ctx, t, q, lib, uuid.Nil, "show", "A", -1, "")
	b := seedChild(ctx, t, q, lib, uuid.Nil, "show", "B", -1, "")
	season := seedChild(ctx, t, q, lib, a, "season", "Season 1", 1, "")

	if n, err := q.SetShowEpisodeOrder(ctx, gen.SetShowEpisodeOrderParams{ID: a, EpisodeOrder: enStr("absolute")}); err != nil || n != 1 {
		t.Fatalf("SetShowEpisodeOrder = %d (err %v)", n, err)
	}
	if n, _ := q.SetShowEpisodeOrder(ctx, gen.SetShowEpisodeOrderParams{ID: season, EpisodeOrder: enStr("dvd")}); n != 0 {
		t.Errorf("episode order on a season wrote %d rows, want 0", n)
	}
	if _, err := q.SetShowEpisodeOrder(ctx, gen.SetShowEpisodeOrderParams{ID: b, EpisodeOrder: enStr("story")}); err == nil {
		t.Errorf("invalid show episode_order accepted")
	}
	got, err := q.GetMediaItem(ctx, a)
	if err != nil || got.EpisodeOrder == nil || *got.EpisodeOrder != "absolute" {
		t.Fatalf("GetMediaItem episode_order = %v (err %v)", got.EpisodeOrder, err)
	}
	following, err := q.ListShowsFollowingLibraryEpisodeOrder(ctx, lib)
	if err != nil || !reflect.DeepEqual(following, []uuid.UUID{b}) {
		t.Errorf("following = %v (err %v), want [B]", following, err)
	}
	// Clearing puts the show back on the library default.
	if _, err := q.SetShowEpisodeOrder(ctx, gen.SetShowEpisodeOrderParams{ID: a, EpisodeOrder: nil}); err != nil {
		t.Fatal(err)
	}
	if following, _ = q.ListShowsFollowingLibraryEpisodeOrder(ctx, lib); len(following) != 2 {
		t.Errorf("following after clear = %v, want both shows", following)
	}
}

func enStr(s string) *string { return &s }
