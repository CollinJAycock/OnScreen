//go:build integration

// Round-trips against a real Postgres testcontainer for migration 00028
// (season-level TV requests): the relaxed unique index (several active season
// requests per show, still one per movie and one all-seasons request per
// show), seasons_available's once-per-season append, the per-season owned
// episode counts with their ACL / rating scoping, the season-aware
// superseded check in RecoverMediaRequestDownload, and the migration's
// Down/Up.
//
// Run with: go test -tags 'dev integration' ./internal/db/gen/ -run RequestSeasons
package gen_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func createSeasonRequest(ctx context.Context, q *gen.Queries, user uuid.UUID, typ string, tmdb int32, seasons string) (gen.MediaRequest, error) {
	var raw []byte
	if seasons != "" {
		raw = []byte(seasons)
	}
	return q.CreateMediaRequest(ctx, gen.CreateMediaRequestParams{
		UserID: user, Type: typ, TmdbID: tmdb, Title: "T", Status: "pending", Seasons: raw,
	})
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func TestRequestSeasons_Integration_UniqueIndex(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	user := seedUser(ctx, t, q, "rs-uniq-"+uuid.New().String()[:8])

	// Several active season requests for one show.
	if _, err := createSeasonRequest(ctx, q, user, "show", 1399, "[1,2]"); err != nil {
		t.Fatalf("seasons 1-2: %v", err)
	}
	if _, err := createSeasonRequest(ctx, q, user, "show", 1399, "[3]"); err != nil {
		t.Fatalf("season 3 alongside: %v", err)
	}
	// One all-seasons request per show…
	if _, err := createSeasonRequest(ctx, q, user, "show", 1399, ""); err != nil {
		t.Fatalf("first all-seasons: %v", err)
	}
	if _, err := createSeasonRequest(ctx, q, user, "show", 1399, ""); !isUniqueViolation(err) {
		t.Errorf("second all-seasons request: err = %v, want unique violation", err)
	}
	// …and one active request per movie, as before.
	if _, err := createSeasonRequest(ctx, q, user, "movie", 949, ""); err != nil {
		t.Fatalf("movie: %v", err)
	}
	if _, err := createSeasonRequest(ctx, q, user, "movie", 949, ""); !isUniqueViolation(err) {
		t.Errorf("duplicate movie: err = %v, want unique violation", err)
	}
	// An identical season set is a duplicate the DB itself rejects (a double
	// submit that raced past the service's read-then-insert check), however
	// the JSON is formatted.
	if _, err := createSeasonRequest(ctx, q, user, "show", 1399, "[1,2]"); !isUniqueViolation(err) {
		t.Errorf("identical season request: err = %v, want unique violation", err)
	}
	if _, err := createSeasonRequest(ctx, q, user, "show", 1399, "[1, 2]"); !isUniqueViolation(err) {
		t.Errorf("identical season request, other formatting: err = %v, want unique violation", err)
	}
	// Another user, and a settled request, don't count.
	other := seedUser(ctx, t, q, "rs-uniq2-"+uuid.New().String()[:8])
	if _, err := createSeasonRequest(ctx, q, other, "show", 1399, "[1,2]"); err != nil {
		t.Errorf("another user's identical request: %v", err)
	}
	three, err := createSeasonRequest(ctx, q, user, "show", 88, "[3]")
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []string{"failed", "available", "declined"} {
		if _, err := pool.Exec(ctx, `UPDATE media_requests SET status = $2 WHERE id = $1`, three.ID, st); err != nil {
			t.Fatal(err)
		}
		again, err := createSeasonRequest(ctx, q, user, "show", 88, "[3]")
		if err != nil {
			t.Fatalf("re-request after the first went %s: %v", st, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM media_requests WHERE id = $1`, again.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Overlapping but different sets are the service's to refuse, not the
	// index's (the documented residual).
	if _, err := createSeasonRequest(ctx, q, user, "show", 1399, "[2,4]"); err != nil {
		t.Errorf("overlapping, different set: %v", err)
	}

	// A new column defaults to an empty set.
	r, err := createSeasonRequest(ctx, q, user, "show", 7, "[1]")
	if err != nil {
		t.Fatal(err)
	}
	if r.SeasonsAvailable == nil || len(r.SeasonsAvailable) != 0 {
		t.Errorf("seasons_available = %#v, want empty", r.SeasonsAvailable)
	}
}

func TestRequestSeasons_Integration_SeasonAvailableOnce(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	user := seedUser(ctx, t, q, "rs-once-"+uuid.New().String()[:8])
	svc := seedDownloadArrService(ctx, t, q, "sonarr")
	r := seedDownloadRequest(ctx, t, pool, q, user, "show", 1399, "downloading", &svc)

	mark := func(season int32) int64 {
		t.Helper()
		n, err := q.MarkMediaRequestSeasonAvailable(ctx, gen.MarkMediaRequestSeasonAvailableParams{ID: r.ID, Season: season})
		if err != nil {
			t.Fatalf("MarkMediaRequestSeasonAvailable(%d): %v", season, err)
		}
		return n
	}
	if n := mark(3); n != 1 {
		t.Errorf("first mark of season 3 = %d, want 1", n)
	}
	if n := mark(3); n != 0 {
		t.Errorf("second mark of season 3 = %d, want 0 (once per season)", n)
	}
	if n := mark(1); n != 1 {
		t.Errorf("mark of season 1 = %d, want 1", n)
	}
	got, _ := q.GetMediaRequest(ctx, r.ID)
	if !reflect.DeepEqual(got.SeasonsAvailable, []int32{1, 3}) {
		t.Errorf("seasons_available = %v, want [1 3] (sorted)", got.SeasonsAvailable)
	}
	if !got.UpdatedAt.Time.Equal(r.UpdatedAt.Time) {
		t.Error("recording a season moved updated_at (the last status change)")
	}

	// Only in-flight requests take a season.
	if err := q.MarkMediaRequestAvailable(ctx, gen.MarkMediaRequestAvailableParams{ID: r.ID}); err != nil {
		t.Fatal(err)
	}
	if n := mark(2); n != 0 {
		t.Errorf("mark on an available request = %d, want 0", n)
	}
	pending, err := createSeasonRequest(ctx, q, user, "show", 55, "[1]")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := q.MarkMediaRequestSeasonAvailable(ctx, gen.MarkMediaRequestSeasonAvailableParams{ID: pending.ID, Season: 1}); n != 0 {
		t.Errorf("mark on a pending request = %d, want 0", n)
	}
}

// seedShowTree builds show → seasons → episodes in lib; files[s] lists the
// episode numbers of season s that get an active file.
func seedShowTree(ctx context.Context, t *testing.T, pool *pgxpool.Pool, q *gen.Queries, lib uuid.UUID, tmdb int32, rating string, files map[int32][]int32) uuid.UUID {
	t.Helper()
	show := seedChild(ctx, t, q, lib, uuid.Nil, "show", fmt.Sprintf("Show %d", tmdb), -1, rating)
	if _, err := pool.Exec(ctx, `UPDATE media_items SET tmdb_id = $2 WHERE id = $1`, show, tmdb); err != nil {
		t.Fatal(err)
	}
	for sn, eps := range files {
		season := seedChild(ctx, t, q, lib, show, "season", "Season", sn, "")
		for _, en := range eps {
			ep := seedChild(ctx, t, q, lib, season, "episode", "Ep", en, "")
			seedMediaFile(ctx, t, q, ep, "/tv/"+uuid.New().String()+".mkv")
		}
	}
	return show
}

func TestRequestSeasons_Integration_OwnedEpisodeCounts(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	libA := seedLibrary(ctx, t, q, "rs-a-"+uuid.New().String()[:8])
	libB := seedLibrary(ctx, t, q, "rs-b-"+uuid.New().String()[:8])

	// Library A: S1E1-3 and S2E1; library B holds a second copy of S1E1-2
	// plus S1E4 (distinct episodes count once across both).
	showA := seedShowTree(ctx, t, pool, q, libA, 1399, "TV-14", map[int32][]int32{1: {1, 2, 3}, 2: {1}})
	seedShowTree(ctx, t, pool, q, libB, 1399, "TV-MA", map[int32][]int32{1: {1, 2, 4}})
	// Another show, not asked about.
	seedShowTree(ctx, t, pool, q, libA, 42, "TV-14", map[int32][]int32{1: {1}})

	// An episode whose file went missing and a deleted episode don't count.
	var s2 uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM media_items WHERE parent_id = $1 AND index = 2`, showA).Scan(&s2); err != nil {
		t.Fatal(err)
	}
	missing := seedChild(ctx, t, q, libA, s2, "episode", "Missing", 2, "")
	f := seedMediaFile(ctx, t, q, missing, "/tv/missing.mkv")
	deleted := seedChild(ctx, t, q, libA, s2, "episode", "Deleted", 3, "")
	seedMediaFile(ctx, t, q, deleted, "/tv/deleted.mkv")
	if _, err := pool.Exec(ctx, `UPDATE media_files SET status = 'missing', missing_since = now() WHERE id = $1`, f); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = now() WHERE id = $1`, deleted); err != nil {
		t.Fatal(err)
	}
	// An episode with no file at all doesn't count either.
	seedChild(ctx, t, q, libA, s2, "episode", "No file", 4, "")

	counts := func(libs []uuid.UUID, maxRank *int32) map[int32]int32 {
		t.Helper()
		rows, err := q.ListOwnedEpisodeCountsByShowTMDB(ctx, gen.ListOwnedEpisodeCountsByShowTMDBParams{
			TmdbIds: []int32{1399}, LibraryIds: libs, MaxRatingRank: maxRank,
		})
		if err != nil {
			t.Fatalf("ListOwnedEpisodeCountsByShowTMDB: %v", err)
		}
		out := map[int32]int32{}
		for _, r := range rows {
			if r.TmdbID != 1399 {
				t.Errorf("row for tmdb %d, asked only 1399", r.TmdbID)
			}
			out[r.SeasonNumber] = r.EpisodeCount
		}
		return out
	}

	if got := counts(nil, nil); !reflect.DeepEqual(got, map[int32]int32{1: 4, 2: 1}) {
		t.Errorf("unrestricted = %v, want S1:4 S2:1", got)
	}
	if got := counts([]uuid.UUID{libA}, nil); !reflect.DeepEqual(got, map[int32]int32{1: 3, 2: 1}) {
		t.Errorf("library A only = %v, want S1:3 S2:1", got)
	}
	if got := counts([]uuid.UUID{}, nil); len(got) != 0 {
		t.Errorf("no grants = %v, want nothing", got)
	}
	// A TV-14 ceiling (rank 2) hides library B's TV-MA copy of the show.
	if got := counts(nil, rank(2)); !reflect.DeepEqual(got, map[int32]int32{1: 3, 2: 1}) {
		t.Errorf("TV-14 ceiling = %v, want library A's counts", got)
	}
}

func TestRequestSeasons_Integration_RecoverRespectsSeasonOverlap(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	user := seedUser(ctx, t, q, "rs-rec-"+uuid.New().String()[:8])
	svc := seedDownloadArrService(ctx, t, q, "sonarr")

	failed := seedDownloadRequest(ctx, t, pool, q, user, "show", 1399, "failed", &svc)
	if _, err := pool.Exec(ctx, `UPDATE media_requests SET seasons = '[1]' WHERE id = $1`, failed.ID); err != nil {
		t.Fatal(err)
	}
	queued := "queued"
	tryRecover := func() int64 {
		t.Helper()
		n, err := q.RecoverMediaRequestDownload(ctx, gen.RecoverMediaRequestDownloadParams{ID: failed.ID, DownloadState: &queued})
		if err != nil {
			t.Fatalf("RecoverMediaRequestDownload: %v", err)
		}
		return n
	}

	// Blocked by a newer request for an overlapping season, and by an
	// all-seasons request.
	overlap, err := createSeasonRequest(ctx, q, user, "show", 1399, "[1,2]")
	if err != nil {
		t.Fatal(err)
	}
	if n := tryRecover(); n != 0 {
		t.Errorf("recover with seasons [1,2] active = %d, want 0", n)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM media_requests WHERE id = $1`, overlap.ID); err != nil {
		t.Fatal(err)
	}
	all, err := createSeasonRequest(ctx, q, user, "show", 1399, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := tryRecover(); n != 0 {
		t.Errorf("recover with an all-seasons request active = %d, want 0", n)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM media_requests WHERE id = $1`, all.ID); err != nil {
		t.Fatal(err)
	}

	// A request for other seasons doesn't supersede it.
	if _, err := createSeasonRequest(ctx, q, user, "show", 1399, "[2,3]"); err != nil {
		t.Fatal(err)
	}
	if n := tryRecover(); n != 1 {
		t.Errorf("recover with only seasons [2,3] active = %d, want 1", n)
	}
}

// 00028's Down keeps each user's oldest active request per title (declining
// later season requests so the old index can be restored), restores the old
// index and drops the column; Up re-applies. Executed directly (see
// migrationSections) so later migrations don't have to roll back.
func TestRequestSeasons_Integration_MigrationDownUp(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	user := seedUser(ctx, t, q, "rs-mig-"+uuid.New().String()[:8])
	first, err := createSeasonRequest(ctx, q, user, "show", 1399, "[1]")
	if err != nil {
		t.Fatal(err)
	}
	second, err := createSeasonRequest(ctx, q, user, "show", 1399, "[2]")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE media_requests SET created_at = created_at + interval '1 second' WHERE id = $1`, second.ID); err != nil {
		t.Fatal(err)
	}

	up, down := migrationSections(t, "00028_request_seasons.sql")
	column := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_name = 'media_requests' AND column_name = 'seasons_available'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if column() != 1 {
		t.Fatal("seasons_available missing after migrate")
	}
	// indexDef is an index's definition, "" when it doesn't exist.
	indexDef := func(name string) string {
		t.Helper()
		var def string
		err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1`, name).Scan(&def)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		return def
	}
	assertUpIndexes := func(when string) {
		t.Helper()
		// The fulfilment lookup by title covers failed requests, so its
		// partial index must too.
		if def := indexDef("media_requests_tmdb_lookup"); !strings.Contains(def, "'failed'") {
			t.Errorf("%s: media_requests_tmdb_lookup = %q, want it to cover failed", when, def)
		}
		if indexDef("media_requests_unique_active_seasons") == "" {
			t.Errorf("%s: media_requests_unique_active_seasons missing", when)
		}
	}
	assertUpIndexes("after migrate")

	execSQL(ctx, t, pool, "00028 down", down)
	if column() != 0 {
		t.Error("seasons_available survived the down")
	}
	if def := indexDef("media_requests_tmdb_lookup"); def == "" || strings.Contains(def, "'failed'") {
		t.Errorf("after down: media_requests_tmdb_lookup = %q, want the original approved/downloading predicate", def)
	}
	if def := indexDef("media_requests_unique_active_seasons"); def != "" {
		t.Errorf("after down: media_requests_unique_active_seasons survived: %q", def)
	}
	status := func(id uuid.UUID) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, `SELECT status FROM media_requests WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if status(first.ID) != "pending" || status(second.ID) != "declined" {
		t.Errorf("after down: first=%s second=%s, want pending / declined", status(first.ID), status(second.ID))
	}
	// The old index is back: one active request per user and show.
	if _, err := pool.Exec(ctx, `INSERT INTO media_requests (user_id, type, tmdb_id, title, status, seasons)
		VALUES ($1, 'show', 1399, 'T', 'pending', '[5]')`, user); !isUniqueViolation(err) {
		t.Errorf("old index not restored: err = %v", err)
	}

	execSQL(ctx, t, pool, "00028 up", up)
	if column() != 1 {
		t.Error("seasons_available not re-added")
	}
	assertUpIndexes("after re-up")
	if _, err := pool.Exec(ctx, `INSERT INTO media_requests (user_id, type, tmdb_id, title, status, seasons)
		VALUES ($1, 'show', 1399, 'T', 'pending', '[5]')`, user); err != nil {
		t.Errorf("season request after re-up: %v", err)
	}
}
