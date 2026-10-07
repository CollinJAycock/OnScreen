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

// TestCollectionsPhase2_UpDown_Integration runs 00038 down and up again with
// its data present: Down drops the cover table and the new columns while the
// collections (smart, promoted, imported) and their members stay; Up
// re-applies cleanly, with the defaults back on existing rows.
func TestCollectionsPhase2_UpDown_Integration(t *testing.T) {
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

	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return n
	}

	var lib, movie, col string
	if err := pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths, nfo_collections) VALUES ('p2', 'movie', '{/tmp/p2}', 'sets') RETURNING id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_items (library_id, type, title, sort_title) VALUES ($1, 'movie', 'Alien', 'Alien') RETURNING id`, lib).Scan(&movie); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO collections (name, type, item_order, promoted, source, source_key, rules)
		VALUES ('Alien Collection', 'manual', 'release', true, 'nfo', 'set:alien collection', '{"types":["movie"]}') RETURNING id`).Scan(&col); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO collection_items (collection_id, media_item_id, position) VALUES ($1, $2, 0)`, col, movie)
	must(`INSERT INTO collection_posters (collection_id, content_type, data) VALUES ($1, 'image/jpeg', '\xffd8')`, col)
	if _, err := pool.Exec(ctx, `INSERT INTO collections (name, type, source, source_key) VALUES ('dupe', 'manual', 'nfo', 'set:alien collection')`); err == nil {
		t.Fatal("(source, source_key) must be unique")
	}

	if err := goose.DownToContext(ctx, db, ".", 37); err != nil {
		t.Fatalf("down to 37: %v", err)
	}
	if n := count(`SELECT COUNT(*) FROM collection_items WHERE collection_id = $1`, col); n != 1 {
		t.Fatalf("members must survive the down: %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'collections' AND column_name IN ('promoted', 'source', 'source_key')`); n != 0 {
		t.Fatalf("%d phase-2 collection columns left after down", n)
	}
	if n := count(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'libraries' AND column_name = 'nfo_collections'`); n != 0 {
		t.Fatal("nfo_collections must be dropped")
	}
	if n := count(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'collection_posters'`); n != 0 {
		t.Fatal("collection_posters must be dropped")
	}

	if err := goose.UpToContext(ctx, db, ".", 38); err != nil {
		t.Fatalf("up to 38: %v", err)
	}
	if n := count(`SELECT COUNT(*) FROM collections WHERE id = $1 AND NOT promoted`, col); n != 1 {
		t.Fatal("promoted must default to false on existing rows")
	}
	if n := count(`SELECT COUNT(*) FROM libraries WHERE id = $1 AND nfo_collections = 'off'`, lib); n != 1 {
		t.Fatal("nfo_collections must default to off on existing rows")
	}
	if _, err := pool.Exec(ctx, `UPDATE libraries SET nfo_collections = 'all' WHERE id = $1`, lib); err == nil {
		t.Fatal("nfo_collections CHECK must reject other values")
	}
	// The cover goes with its collection.
	must(`INSERT INTO collection_posters (collection_id, content_type, data) VALUES ($1, 'image/jpeg', '\xffd8')`, col)
	must(`DELETE FROM collections WHERE id = $1`, col)
	if n := count(`SELECT COUNT(*) FROM collection_posters`); n != 0 {
		t.Fatalf("covers must cascade with the collection: %d", n)
	}
}
