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

// TestFranchiseCollections_UpDown_Integration runs 00027 down and up again
// with franchise data present: Down must drop the new tables and column and
// remove franchise rows (the restored CHECK would reject them) while leaving
// other collections and their items alone; Up must re-apply cleanly.
//
// Run with: go test -tags 'dev integration' -run FranchiseCollections ./internal/db/migrations/
func TestFranchiseCollections_UpDown_Integration(t *testing.T) {
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

	var lib, movie, user, franchiseID, playlistID string
	if err := pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('fc', 'movie', '{/tmp/fc}') RETURNING id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_items (library_id, type, title, sort_title, tmdb_id) VALUES ($1, 'movie', 'Alien', 'Alien', 348) RETURNING id`, lib).Scan(&movie); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (username) VALUES ('fc-user') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO collections (name, type, tmdb_collection_id) VALUES ('Alien Collection', 'franchise', 8091) RETURNING id`).Scan(&franchiseID); err != nil {
		t.Fatalf("franchise type must be accepted after 00027: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO collections (user_id, name, type) VALUES ($1, 'Mine', 'playlist') RETURNING id`, user).Scan(&playlistID); err != nil {
		t.Fatal(err)
	}
	must(`INSERT INTO collection_items (collection_id, media_item_id, position) VALUES ($1, $2, 0), ($3, $2, 0)`, franchiseID, movie, playlistID)
	must(`INSERT INTO tmdb_collections (tmdb_collection_id, name, parts) VALUES (8091, 'Alien Collection', '[{"tmdb_id":348}]')`)
	must(`INSERT INTO media_item_tmdb_collections (media_item_id, tmdb_id, tmdb_collection_id) VALUES ($1, 348, 8091)`, movie)

	// A second row for the same TMDB collection is rejected; rows without
	// one don't collide.
	if _, err := pool.Exec(ctx, `INSERT INTO collections (name, type, tmdb_collection_id) VALUES ('dupe', 'franchise', 8091)`); err == nil {
		t.Fatal("tmdb_collection_id must be unique")
	}

	if err := goose.DownToContext(ctx, db, ".", 26); err != nil {
		t.Fatalf("down to 26: %v", err)
	}
	if n := count(`SELECT COUNT(*) FROM collections WHERE type = 'franchise'`); n != 0 {
		t.Fatalf("franchise rows after down = %d, want 0", n)
	}
	if n := count(`SELECT COUNT(*) FROM collection_items WHERE collection_id = $1`, playlistID); n != 1 {
		t.Fatalf("playlist items must survive the down: %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name = 'collections' AND column_name = 'tmdb_collection_id'`); n != 0 {
		t.Fatal("tmdb_collection_id must be dropped")
	}
	if n := count(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name IN ('tmdb_collections', 'media_item_tmdb_collections')`); n != 0 {
		t.Fatal("franchise tables must be dropped")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO collections (name, type) VALUES ('x', 'franchise')`); err == nil {
		t.Fatal("restored CHECK must reject the franchise type")
	}

	if err := goose.UpToContext(ctx, db, ".", 27); err != nil {
		t.Fatalf("up to 27: %v", err)
	}
	must(`INSERT INTO collections (name, type, tmdb_collection_id) VALUES ('Alien Collection', 'franchise', 8091)`)
	must(`INSERT INTO media_item_tmdb_collections (media_item_id, tmdb_id) VALUES ($1, 348)`, movie)
	// The link row cascades with its movie.
	must(`DELETE FROM media_items WHERE id = $1`, movie)
	if n := count(`SELECT COUNT(*) FROM media_item_tmdb_collections`); n != 0 {
		t.Fatalf("link rows must cascade with the movie: %d", n)
	}
}
