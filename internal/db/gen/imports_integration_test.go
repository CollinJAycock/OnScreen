//go:build integration

package gen_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestImportQueries_Integration runs the server-import queries (migration
// 00040) against Postgres: one running job at a time, the stale-job sweep,
// the match candidates, and the writes' "what's already here wins" and
// "a re-run adds only what's new" rules, read back through user_watch_state.
//
// Run with: go test -tags 'dev integration' -run ImportQueries ./internal/db/gen/
func TestImportQueries_Integration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := gen.New(pool)
	ts := func(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

	// ── jobs ──
	job, err := q.CreateImportJob(ctx, gen.CreateImportJobParams{
		SourceKind: "plex", SourceUrl: "http://10.0.0.5:32400", Options: []byte(`{"watched":true}`),
		Mapping: []byte(`[]`), Phase: "Starting",
	})
	if err != nil {
		t.Fatalf("CreateImportJob: %v", err)
	}
	if job.Status != "running" || string(job.Stats) != "{}" {
		t.Errorf("new job = %s %s", job.Status, job.Stats)
	}
	_, err = q.CreateImportJob(ctx, gen.CreateImportJobParams{SourceKind: "emby", SourceUrl: "http://x", Options: []byte(`{}`), Mapping: []byte(`[]`)})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Errorf("second running job = %v, want a unique violation", err)
	}
	if _, err := q.CreateImportJob(ctx, gen.CreateImportJobParams{SourceKind: "kodi", SourceUrl: "x", Options: []byte(`{}`), Mapping: []byte(`[]`)}); err == nil {
		t.Error("unknown source kind accepted")
	}
	if err := q.UpdateImportJobProgress(ctx, gen.UpdateImportJobProgressParams{ID: job.ID, Phase: "Writing", Stats: []byte(`{"matched":3}`)}); err != nil {
		t.Fatalf("UpdateImportJobProgress: %v", err)
	}
	if n, err := q.FailStaleImportJobs(ctx, ts(time.Now().Add(-time.Minute))); err != nil || n != 0 {
		t.Errorf("stale sweep failed a live job: %d %v", n, err)
	}
	if n, err := q.FailStaleImportJobs(ctx, ts(time.Now().Add(time.Minute))); err != nil || n != 1 {
		t.Errorf("stale sweep = %d %v, want 1", n, err)
	}
	stale, _ := q.GetImportJob(ctx, job.ID)
	if stale.Status != "failed" || stale.Error == nil || !stale.FinishedAt.Valid {
		t.Errorf("stale job = %s %v", stale.Status, stale.Error)
	}
	if _, err := q.FinishImportJob(ctx, gen.FinishImportJobParams{ID: job.ID, Status: "succeeded", Stats: []byte(`{}`), Unmatched: []byte(`[]`)}); err == nil {
		t.Error("finished a job that wasn't running")
	}
	job2, err := q.CreateImportJob(ctx, gen.CreateImportJobParams{SourceKind: "jellyfin", SourceUrl: "http://jf", Options: []byte(`{}`), Mapping: []byte(`[]`)})
	if err != nil {
		t.Fatalf("job after the stale one: %v", err)
	}
	done, err := q.FinishImportJob(ctx, gen.FinishImportJobParams{ID: job2.ID, Status: "succeeded", Phase: "Done",
		Stats: []byte(`{"matched":1}`), Unmatched: []byte(`["X"]`)})
	if err != nil || done.Status != "succeeded" || string(done.Unmatched) != `["X"]` {
		t.Errorf("FinishImportJob = %+v %v", done, err)
	}
	if jobs, err := q.ListImportJobs(ctx, 10); err != nil || len(jobs) != 2 || jobs[0].ID != job2.ID {
		t.Errorf("ListImportJobs = %d %v", len(jobs), err)
	}

	// ── candidates ──
	var user, lib uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (username) VALUES ('importer') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('Lib', 'movie', '{/m}') RETURNING id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	item := func(typ, title string, parent *uuid.UUID, index *int, tmdb *int) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO media_items (library_id, type, title, sort_title, parent_id, index, tmdb_id, duration_ms)
			 VALUES ($1, $2, $3, $3, $4, $5, $6, 6000000) RETURNING id`,
			lib, typ, title, parent, index, tmdb).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", typ, err)
		}
		return id
	}
	n := func(i int) *int { return &i }
	movie := item("movie", "Alien", nil, nil, n(348))
	other := item("movie", "Heat", nil, nil, nil)
	show := item("show", "Severance", nil, nil, n(95396))
	season := item("season", "Season 1", &show, n(1), nil)
	ep := item("episode", "Good News", &season, n(1), nil)
	item("episode", "Flat", &show, n(2), nil) // no season: path-only
	if _, err := pool.Exec(ctx, `INSERT INTO media_files (media_item_id, file_path, file_size, duration_ms) VALUES ($1, '/m/Alien (1979)/Alien.mkv', 1, 7000000)`, movie); err != nil {
		t.Fatal(err)
	}

	items, err := q.ListImportCandidateItems(ctx)
	if err != nil || len(items) != 2 { // Alien and Severance carry IDs; Heat doesn't
		t.Errorf("candidate items = %+v %v", items, err)
	}
	eps, err := q.ListImportCandidateEpisodes(ctx)
	if err != nil || len(eps) != 1 || eps[0].ID != ep || eps[0].ShowID != show || eps[0].SeasonNumber != 1 || eps[0].EpisodeNumber != 1 {
		t.Errorf("candidate episodes = %+v %v", eps, err)
	}
	files, err := q.ListImportCandidateFiles(ctx)
	if err != nil || len(files) != 1 || files[0].Type != "movie" || *files[0].FileDurationMs != 7000000 || *files[0].ItemDurationMs != 6000000 {
		t.Errorf("candidate files = %+v %v", files, err)
	}

	// ── watched marks ──
	state := func(media uuid.UUID) (status string, pos int64, resumable bool) {
		t.Helper()
		err := pool.QueryRow(ctx, `SELECT status, position_ms, resumable FROM user_watch_state WHERE user_id = $1 AND media_id = $2`,
			user, media).Scan(&status, &pos, &resumable)
		if err != nil {
			t.Fatalf("read watch state: %v", err)
		}
		return
	}
	t1 := time.Date(2025, 3, 1, 20, 0, 0, 0, time.UTC)
	mark := func(media uuid.UUID, at time.Time) int64 {
		t.Helper()
		n, err := q.ImportWatchedMark(ctx, gen.ImportWatchedMarkParams{UserID: user, MediaID: media, MarkedAt: ts(at)})
		if err != nil {
			t.Fatalf("ImportWatchedMark: %v", err)
		}
		return n
	}
	if mark(movie, t1) != 1 {
		t.Error("first mark not written")
	}
	if s, _, _ := state(movie); s != "watched" {
		t.Errorf("after mark: %s", s)
	}
	if mark(movie, t1) != 0 || mark(movie, t1.Add(-time.Hour)) != 0 {
		t.Error("an equal or older mark replaced a newer one")
	}
	// A newer "unwatched" mark made on OnScreen wins over an older import.
	if _, err := pool.Exec(ctx, `UPDATE watch_progress SET mark_state = 'unwatched', marked_at = $3 WHERE user_id = $1 AND media_id = $2`,
		user, movie, t1.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if mark(movie, t1) != 0 {
		t.Error("an import overrode a newer local unwatched mark")
	}

	// ── events ──
	event := func(media uuid.UUID, typ string, pos, dur int64, at time.Time) int64 {
		t.Helper()
		n, err := q.ImportWatchEvent(ctx, gen.ImportWatchEventParams{UserID: user, MediaID: media, EventType: typ,
			PositionMs: pos, DurationMs: &dur, ClientID: "import:plex", ClientName: "Plex (imported)", OccurredAt: ts(at)})
		if err != nil {
			t.Fatalf("ImportWatchEvent: %v", err)
		}
		return n
	}
	// A resume point makes Heat resumable at its position.
	if event(other, "pause", 600000, 6000000, t1) != 1 {
		t.Error("resume point not written")
	}
	if s, pos, res := state(other); s != "in_progress" || pos != 600000 || !res {
		t.Errorf("after resume point: %s %d %v", s, pos, res)
	}
	if event(other, "pause", 600000, 6000000, t1) != 0 {
		t.Error("the same resume point was written twice")
	}
	// An older imported play doesn't move the resume point back.
	if event(other, "scrobble", 6000000, 6000000, t1.Add(-24*time.Hour)) != 1 {
		t.Error("play not written")
	}
	if s, pos, _ := state(other); s != "watched" || pos != 600000 {
		t.Errorf("after an older play: %s %d (watched, resume point kept)", s, pos)
	}

	// ── ratings, favorites ──
	if n, err := q.ImportUserRating(ctx, gen.ImportUserRatingParams{UserID: user, MediaItemID: movie, Score: 9}); err != nil || n != 1 {
		t.Errorf("rating = %d %v", n, err)
	}
	if n, _ := q.ImportUserRating(ctx, gen.ImportUserRatingParams{UserID: user, MediaItemID: movie, Score: 2}); n != 0 {
		t.Error("an import replaced an existing rating")
	}
	if n, err := q.ImportFavorite(ctx, gen.ImportFavoriteParams{UserID: user, MediaID: show}); err != nil || n != 1 {
		t.Errorf("favorite = %d %v", n, err)
	}
	if n, _ := q.ImportFavorite(ctx, gen.ImportFavoriteParams{UserID: user, MediaID: show}); n != 0 {
		t.Error("favorite written twice")
	}

	// ── playlists ──
	src, key := "import:plex", "mach:1:40"
	pl, err := q.FindOrCreateImportedPlaylist(ctx, gen.FindOrCreateImportedPlaylistParams{
		UserID: pgtype.UUID{Bytes: user, Valid: true}, Name: "Mix", Source: &src, SourceKey: &key})
	if err != nil || !pl.Created {
		t.Fatalf("playlist = %+v %v", pl, err)
	}
	again, err := q.FindOrCreateImportedPlaylist(ctx, gen.FindOrCreateImportedPlaylistParams{
		UserID: pgtype.UUID{Bytes: user, Valid: true}, Name: "Mix", Source: &src, SourceKey: &key})
	if err != nil || again.Created || again.ID != pl.ID {
		t.Errorf("re-run playlist = %+v %v, want the same one", again, err)
	}
	for i, m := range []uuid.UUID{other, movie, other} {
		n, err := q.ImportPlaylistItem(ctx, gen.ImportPlaylistItemParams{CollectionID: pl.ID, MediaItemID: m})
		if err != nil || n != map[int]int64{0: 1, 1: 1, 2: 0}[i] {
			t.Errorf("playlist item %d = %d %v", i, n, err)
		}
	}
	var listed int
	var typ string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM collection_items WHERE collection_id = $1`, pl.ID).Scan(&listed); err != nil || listed != 2 {
		t.Errorf("playlist has %d items", listed)
	}
	if err := pool.QueryRow(ctx, `SELECT type FROM collections WHERE id = $1 AND user_id = $2`, pl.ID, user).Scan(&typ); err != nil || typ != "playlist" {
		t.Errorf("playlist row type = %q %v", typ, err)
	}
	var positions []int32
	rows, _ := pool.Query(ctx, `SELECT position FROM collection_items WHERE collection_id = $1 ORDER BY position`, pl.ID)
	for rows.Next() {
		var p int32
		_ = rows.Scan(&p)
		positions = append(positions, p)
	}
	rows.Close()
	if len(positions) != 2 || positions[0] != 0 || positions[1] != 1 {
		t.Errorf("positions = %v", positions)
	}
}
