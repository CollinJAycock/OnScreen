//go:build integration

package gen_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestPodcastQueries_Integration runs the podcast subscription queries
// (migration 00039) against Postgres: one subscription per library and URL,
// episode upserts that leave download state alone, the download claim, the
// scanner's path lookup, retention order, and the cascade on unsubscribe.
//
// Run with: go test -tags 'dev integration' -run PodcastQueries ./internal/db/gen/
func TestPodcastQueries_Integration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := gen.New(pool)

	var lib uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO libraries (name, type, scan_paths) VALUES ('Podcasts', 'podcast', '{/media/podcasts}') RETURNING id`,
	).Scan(&lib); err != nil {
		t.Fatalf("insert library: %v", err)
	}

	feed, err := q.CreatePodcastFeed(ctx, gen.CreatePodcastFeedParams{
		LibraryID: lib, FeedUrl: "https://a.example/feed.xml", Title: "Show",
		Folder: "/media/podcasts/Show", AutoDownload: true, KeepLatest: 2,
	})
	if err != nil {
		t.Fatalf("CreatePodcastFeed: %v", err)
	}
	if !feed.LastCheckedAt.Valid {
		t.Error("last_checked_at not set at subscribe")
	}
	_, err = q.CreatePodcastFeed(ctx, gen.CreatePodcastFeedParams{
		LibraryID: lib, FeedUrl: "https://a.example/feed.xml", Title: "Again", Folder: "/x",
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("second subscription to the same URL = %v, want a unique violation", err)
	}
	if _, err := q.CreatePodcastFeed(ctx, gen.CreatePodcastFeedParams{
		LibraryID: lib, FeedUrl: "https://b.example/rss", Title: "B", Folder: "/media/podcasts/B", KeepLatest: -1,
	}); err == nil {
		t.Error("negative keep_latest accepted")
	}

	day := func(d int) pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC), Valid: true}
	}
	upsert := func(guid, title string, d int) gen.PodcastFeedEpisode {
		t.Helper()
		ep, err := q.UpsertPodcastEpisode(ctx, gen.UpsertPodcastEpisodeParams{
			FeedID: feed.ID, Guid: guid, Title: title, PublishedAt: day(d),
			EnclosureUrl: "https://a.example/" + guid + ".mp3",
		})
		if err != nil {
			t.Fatalf("UpsertPodcastEpisode(%s): %v", guid, err)
		}
		return ep
	}
	ep1 := upsert("g1", "One", 1)
	ep2 := upsert("g2", "Two", 2)
	ep3 := upsert("g3", "Three", 3)
	if ep1.Status != "available" {
		t.Errorf("new episode status = %q", ep1.Status)
	}

	// Claim: once, then refused until it's failed or removed.
	path1 := "/media/podcasts/Show/2026-10-01 One.mp3"
	claimed, err := q.ClaimPodcastEpisodeDownload(ctx, gen.ClaimPodcastEpisodeDownloadParams{ID: ep1.ID, FilePath: &path1})
	if err != nil || claimed.Status != "downloading" {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if _, err := q.ClaimPodcastEpisodeDownload(ctx, gen.ClaimPodcastEpisodeDownloadParams{ID: ep1.ID, FilePath: &path1}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("second claim = %v, want no rows", err)
	}

	// A refresh's upsert updates details but not download state.
	again := upsert("g1", "One (renamed)", 1)
	if again.ID != ep1.ID || again.Title != "One (renamed)" || again.Status != "downloading" || again.FilePath == nil {
		t.Errorf("re-upsert = %+v", again)
	}
	guids, err := q.ListPodcastEpisodeGUIDs(ctx, feed.ID)
	if err != nil || len(guids) != 3 {
		t.Errorf("guids = %v, %v", guids, err)
	}

	// The scanner finds the episode and its feed by path, and the feed by
	// folder.
	byPath, err := q.GetPodcastEpisodeByFilePath(ctx, &path1)
	if err != nil || byPath.ID != ep1.ID || byPath.FeedTitle != "Show" || byPath.FeedFolder != "/media/podcasts/Show" {
		t.Errorf("by path = %+v, %v", byPath, err)
	}
	byFolder, err := q.GetPodcastFeedByFolder(ctx, "/media/podcasts/Show")
	if err != nil || byFolder.ID != feed.ID {
		t.Errorf("by folder = %+v, %v", byFolder, err)
	}

	if err := q.FinishPodcastEpisodeDownload(ctx, ep1.ID); err != nil {
		t.Fatal(err)
	}
	for _, ep := range []gen.PodcastFeedEpisode{ep2, ep3} {
		p := "/media/podcasts/Show/" + ep.Guid + ".mp3"
		if _, err := q.ClaimPodcastEpisodeDownload(ctx, gen.ClaimPodcastEpisodeDownloadParams{ID: ep.ID, FilePath: &p}); err != nil {
			t.Fatal(err)
		}
		if err := q.FinishPodcastEpisodeDownload(ctx, ep.ID); err != nil {
			t.Fatal(err)
		}
	}

	// Retention reads downloads newest-published first.
	downloaded, err := q.ListDownloadedPodcastEpisodes(ctx, feed.ID)
	if err != nil || len(downloaded) != 3 || downloaded[0].Guid != "g3" || downloaded[2].Guid != "g1" {
		t.Fatalf("downloaded order = %v, %v", guidsOf(downloaded), err)
	}

	feeds, err := q.ListPodcastFeeds(ctx, pgtype.UUID{Bytes: lib, Valid: true})
	if err != nil || len(feeds) != 1 || feeds[0].EpisodeCount != 3 || feeds[0].DownloadedCount != 3 {
		t.Errorf("feed list = %+v, %v", feeds, err)
	}
	if all, _ := q.ListPodcastFeeds(ctx, pgtype.UUID{}); len(all) != 1 {
		t.Errorf("all feeds = %d", len(all))
	}

	// Removed: path cleared, and a manual download can claim it again.
	if err := q.MarkPodcastEpisodeRemoved(ctx, ep1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetPodcastEpisodeByFilePath(ctx, &path1); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("removed episode still found by path: %v", err)
	}
	if _, err := q.ClaimPodcastEpisodeDownload(ctx, gen.ClaimPodcastEpisodeDownloadParams{ID: ep1.ID, FilePath: &path1}); err != nil {
		t.Errorf("claim of a removed episode = %v", err)
	}

	// A claim older than the cutoff is handed back as failed.
	n, err := q.ResetStalePodcastDownloads(ctx, pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true})
	if err != nil || n != 1 {
		t.Errorf("reset stale = %d, %v; want 1", n, err)
	}
	if ep, _ := q.GetPodcastEpisode(ctx, ep1.ID); ep.Status != "failed" || ep.FilePath != nil {
		t.Errorf("after reset = %q %v", ep.Status, ep.FilePath)
	}

	// Paging, newest first.
	page, err := q.ListPodcastEpisodes(ctx, gen.ListPodcastEpisodesParams{FeedID: feed.ID, Limit: 2, Offset: 1})
	if err != nil || len(page) != 2 || page[0].Guid != "g2" {
		t.Errorf("page = %v, %v", guidsOf(page), err)
	}

	// Unsubscribing deletes the episodes with the feed.
	if err := q.DeletePodcastFeed(ctx, feed.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := q.CountPodcastEpisodes(ctx, feed.ID); n != 0 {
		t.Errorf("episodes left after unsubscribe: %d", n)
	}
}

func guidsOf(eps []gen.PodcastFeedEpisode) []string {
	out := make([]string, len(eps))
	for i, e := range eps {
		out[i] = e.Guid
	}
	return out
}

// TestCleanupEmptyPodcastItems_Integration: the scan-end cleanup hides a
// podcast episode left with no file and then its show once no episode is
// left, as it does for TV shows and albums. Before podcast types were in
// these queries, deleting a feed's last download (or unsubscribing with
// "also delete") left an empty show in the library.
func TestCleanupEmptyPodcastItems_Integration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := gen.New(pool)

	var lib uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO libraries (name, type, scan_paths) VALUES ('Podcasts', 'podcast', '{/media/podcasts}') RETURNING id`,
	).Scan(&lib); err != nil {
		t.Fatalf("insert library: %v", err)
	}
	item := func(typ, title string, parent *uuid.UUID) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO media_items (library_id, type, title, sort_title, parent_id) VALUES ($1, $2, $3, $3, $4) RETURNING id`,
			lib, typ, title, parent,
		).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", typ, err)
		}
		return id
	}
	emptied := item("podcast", "Emptied", nil)
	gone := item("podcast_episode", "Gone", &emptied)
	kept := item("podcast", "Kept", nil)
	live := item("podcast_episode", "Live", &kept)
	if _, err := pool.Exec(ctx,
		`INSERT INTO media_files (media_item_id, file_path, file_size) VALUES ($1, '/media/podcasts/Kept/ep.mp3', 1)`, live,
	); err != nil {
		t.Fatalf("insert file: %v", err)
	}

	if err := q.SoftDeleteItemsWithNoActiveFiles(ctx, lib); err != nil {
		t.Fatalf("SoftDeleteItemsWithNoActiveFiles: %v", err)
	}
	if err := q.SoftDeleteEmptyContainerItems(ctx, lib); err != nil {
		t.Fatalf("SoftDeleteEmptyContainerItems: %v", err)
	}

	for id, wantDeleted := range map[uuid.UUID]bool{gone: true, emptied: true, live: false, kept: false} {
		var deleted bool
		if err := pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM media_items WHERE id = $1`, id).Scan(&deleted); err != nil {
			t.Fatalf("read item: %v", err)
		}
		if deleted != wantDeleted {
			t.Errorf("item %s deleted = %v, want %v", id, deleted, wantDeleted)
		}
	}
}
