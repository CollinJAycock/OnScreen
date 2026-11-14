//go:build integration

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/testdb"
)

// oldContentRatingRank is the PL/pgSQL body 00016 installed, verbatim, so the
// SQL rewrite in 00064 can be compared with it value by value.
const oldContentRatingRank = `
CREATE FUNCTION pg_temp.old_content_rating_rank(rating text) RETURNS integer
    LANGUAGE plpgsql IMMUTABLE
    AS $$
BEGIN
  IF rating IS NULL OR rating = '' THEN
    RETURN 4;
  END IF;
  RETURN CASE rating
    WHEN 'G'     THEN 0  WHEN 'TV-Y'  THEN 0  WHEN 'TV-G' THEN 0
    WHEN 'PG'    THEN 1  WHEN 'TV-Y7' THEN 1  WHEN 'TV-PG' THEN 1
    WHEN 'PG-13' THEN 2  WHEN 'TV-14' THEN 2  WHEN 'R-15' THEN 2
    WHEN 'R'     THEN 3  WHEN 'R-17+' THEN 3
    WHEN 'NC-17' THEN 3  WHEN 'TV-MA' THEN 3  WHEN 'R+'   THEN 3  WHEN 'R-18+' THEN 3
    WHEN 'Rx'    THEN 4
    ELSE 4
  END;
END;
$$;`

// TestContentRatingRank_SQLRewriteMatches_Integration: 00064 turned
// content_rating_rank into an inlinable SQL function. It must return exactly
// what the PL/pgSQL version did for every rating on the ladder, the unrated
// spellings, case and whitespace variants, and NULL.
//
// Run with: go test -tags 'dev integration' -p 1 -run ContentRatingRank ./internal/db/migrations/
func TestContentRatingRank_SQLRewriteMatches_Integration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx) // pg_temp is per connection
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, oldContentRatingRank); err != nil {
		t.Fatalf("create old function: %v", err)
	}

	var lang, parallel, volatility string
	if err := conn.QueryRow(ctx, `
		SELECT l.lanname, p.proparallel::text, p.provolatile::text
		FROM pg_proc p JOIN pg_language l ON l.oid = p.prolang
		WHERE p.oid = 'public.content_rating_rank(text)'::regprocedure`).Scan(&lang, &parallel, &volatility); err != nil {
		t.Fatal(err)
	}
	if lang != "sql" || parallel != "s" || volatility != "i" {
		t.Fatalf("content_rating_rank is %s / parallel %s / volatile %s; want sql / s / i", lang, parallel, volatility)
	}

	ratings := []*string{nil}
	for _, r := range []string{
		"G", "TV-Y", "TV-G", "PG", "TV-Y7", "TV-PG", "PG-13", "TV-14", "R-15",
		"R", "R-17+", "NC-17", "TV-MA", "R+", "R-18+", "Rx",
		"", "NR", "UNRATED", "Unrated", "X", "Approved", "TV-Y7-FV",
		"pg", "pg-13", "tv-ma", "rx", " PG", "PG ", "R ", "PG13", "18+",
	} {
		ratings = append(ratings, &r)
	}
	for _, r := range ratings {
		var got, want int
		if err := conn.QueryRow(ctx,
			`SELECT public.content_rating_rank($1::text), pg_temp.old_content_rating_rank($1::text)`, r).
			Scan(&got, &want); err != nil {
			t.Fatal(err)
		}
		if got != want {
			name := "NULL"
			if r != nil {
				name = "'" + *r + "'"
			}
			t.Errorf("content_rating_rank(%s) = %d, PL/pgSQL version = %d", name, got, want)
		}
	}

	// Inlined: the plan shows the CASE, not a function call.
	rows, err := conn.Query(ctx, `EXPLAIN (VERBOSE, COSTS OFF) SELECT id FROM media_items WHERE content_rating_rank(content_rating) <= 2`)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(lines, "\n")
	if !strings.Contains(plan, "CASE") || strings.Contains(plan, "content_rating_rank(") {
		t.Errorf("expected content_rating_rank inlined as a CASE expression; plan:\n%s", plan)
	}
}

// TestPhotoTakenAtTrigger_Integration: media_items.photo_taken_at is the
// timeline's sort key and must follow photo_metadata.taken_at through every
// write: insert, update, clear, delete, a row moved to another item, and the
// cascade when the photo item itself is deleted. The migration's backfill
// is covered by running Down then Up over existing EXIF rows.
func TestPhotoTakenAtTrigger_Integration(t *testing.T) {
	pool, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()

	var lib, a, b string
	if err := pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('ph', 'photo', '{/tmp/ph}') RETURNING id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	for _, dst := range []*string{&a, &b} {
		if err := pool.QueryRow(ctx, `INSERT INTO media_items (library_id, type, title, sort_title) VALUES ($1, 'photo', 'p', 'p') RETURNING id`, lib).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	takenOf := func(id string) *time.Time {
		t.Helper()
		var at *time.Time
		if err := pool.QueryRow(ctx, `SELECT photo_taken_at FROM media_items WHERE id = $1`, id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	want := func(id string, at *time.Time) {
		t.Helper()
		got := takenOf(id)
		if (got == nil) != (at == nil) || (got != nil && !got.Equal(*at)) {
			t.Fatalf("photo_taken_at(%s) = %v, want %v", id, got, at)
		}
	}

	t1 := time.Date(2019, 7, 4, 18, 30, 5, 0, time.UTC)
	t2 := t1.Add(36 * time.Hour)
	exec(`INSERT INTO photo_metadata (item_id, taken_at) VALUES ($1, $2)`, a, t1)
	want(a, &t1)
	// The scanner's upsert path (ON CONFLICT DO UPDATE).
	exec(`INSERT INTO photo_metadata (item_id, taken_at) VALUES ($1, $2)
	      ON CONFLICT (item_id) DO UPDATE SET taken_at = EXCLUDED.taken_at`, a, t2)
	want(a, &t2)
	exec(`UPDATE photo_metadata SET taken_at = NULL WHERE item_id = $1`, a)
	want(a, nil)
	exec(`UPDATE photo_metadata SET taken_at = $2 WHERE item_id = $1`, a, t1)
	exec(`UPDATE photo_metadata SET item_id = $2 WHERE item_id = $1`, a, b)
	want(a, nil)
	want(b, &t1)
	exec(`DELETE FROM photo_metadata WHERE item_id = $1`, b)
	want(b, nil)

	// Cascade from the item itself: the trigger's UPDATE finds no live row.
	exec(`INSERT INTO photo_metadata (item_id, taken_at) VALUES ($1, $2)`, b, t2)
	exec(`DELETE FROM media_items WHERE id = $1`, b)
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM photo_metadata WHERE item_id = $1`, b).Scan(&n); err != nil || n != 0 {
		t.Fatalf("cascade left %d photo_metadata rows (err %v)", n, err)
	}

	// Down then Up: the column goes and comes back, backfilled from the
	// EXIF rows that exist; Down restores hub_recently_added, populated, and
	// the PL/pgSQL content_rating_rank.
	exec(`INSERT INTO photo_metadata (item_id, taken_at) VALUES ($1, $2)`, a, t2)
	exec(`INSERT INTO media_items (library_id, type, title, sort_title) VALUES ($1, 'movie', 'M', 'M')`, lib)
	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownToContext(ctx, db, ".", 63); err != nil {
		t.Fatalf("down to 63: %v", err)
	}
	var hubRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hub_recently_added`).Scan(&hubRows); err != nil || hubRows != 1 {
		t.Fatalf("hub_recently_added after Down: %d rows, err %v; want 1", hubRows, err)
	}
	var lang string
	if err := pool.QueryRow(ctx, `SELECT l.lanname FROM pg_proc p JOIN pg_language l ON l.oid = p.prolang
		WHERE p.oid = 'public.content_rating_rank(text)'::regprocedure`).Scan(&lang); err != nil || lang != "plpgsql" {
		t.Fatalf("content_rating_rank after Down is %q (err %v), want plpgsql", lang, err)
	}
	if err := goose.UpToContext(ctx, db, ".", 64); err != nil {
		t.Fatalf("up to 64: %v", err)
	}
	want(a, &t2)
	var dummy int
	err = pool.QueryRow(ctx, `SELECT 1 FROM hub_recently_added LIMIT 1`).Scan(&dummy)
	if err == nil || errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("hub_recently_added should be dropped after Up, got err %v", err)
	}
}
