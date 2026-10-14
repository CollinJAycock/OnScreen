//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestEpisodeNumbering_Integration runs 00044 down and up: Down drops the
// new columns and puts episode_ancestry back to its 00023 shape (without
// episode_span) — media_watch_bucket keeps working across it — and Up
// restores them with existing libraries on 'aired'.
//
// Run with: go test -tags 'dev integration' -run EpisodeNumbering ./internal/db/migrations/
func TestEpisodeNumbering_Integration(t *testing.T) {
	pool, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()
	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}

	column := func(table, col string) bool {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2`, table, col).Scan(&n); err != nil {
			t.Fatalf("columns: %v", err)
		}
		return n == 1
	}
	bucket := func() {
		t.Helper()
		var b string
		if err := pool.QueryRow(ctx, `SELECT media_watch_bucket(gen_random_uuid(), gen_random_uuid(), 'show', NULL)`).Scan(&b); err != nil || b != "unwatched" {
			t.Fatalf("media_watch_bucket = %q (err %v)", b, err)
		}
	}

	for _, c := range [][2]string{{"media_items", "index_end"}, {"media_items", "episode_order"}, {"libraries", "episode_order"}, {"episode_ancestry", "episode_span"}} {
		if !column(c[0], c[1]) {
			t.Fatalf("after up: %s.%s missing", c[0], c[1])
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('m44', 'show', '{/tmp/m44}')`); err != nil {
		t.Fatalf("seed library: %v", err)
	}

	if err := goose.DownToContext(ctx, db, ".", 43); err != nil {
		t.Fatalf("down past 44: %v", err)
	}
	for _, c := range [][2]string{{"media_items", "index_end"}, {"media_items", "episode_order"}, {"libraries", "episode_order"}, {"episode_ancestry", "episode_span"}} {
		if column(c[0], c[1]) {
			t.Errorf("after down: %s.%s still there", c[0], c[1])
		}
	}
	if !column("episode_ancestry", "container_id") {
		t.Fatalf("after down: episode_ancestry not restored")
	}
	bucket()

	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("up again: %v", err)
	}
	var order string
	if err := pool.QueryRow(ctx, `SELECT episode_order FROM libraries WHERE name = 'm44'`).Scan(&order); err != nil || order != "aired" {
		t.Errorf("existing library episode_order = %q (err %v), want aired", order, err)
	}
	bucket()
}
