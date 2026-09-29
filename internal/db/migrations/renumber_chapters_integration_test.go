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

// TestRenumberZeroLedChapters_Integration applies 00034 over chapters the
// first numbering rule got wrong: a book with a "00 - Prologue" file loses
// its chapter numbers (the next scan renumbers it), and a book without one
// keeps them.
//
// Run with: go test -tags 'dev integration' -run RenumberZeroLed ./internal/db/migrations/
func TestRenumberZeroLedChapters_Integration(t *testing.T) {
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
	if err := goose.DownToContext(ctx, db, ".", 33); err != nil {
		t.Fatalf("down to 33: %v", err)
	}

	var lib string
	if err := pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('books', 'audiobook', '{/books}') RETURNING id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	item := func(typ, title string, parent *string, index *int) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO media_items (library_id, type, title, sort_title, parent_id, index) VALUES ($1, $2, $3, $3, $4, $5) RETURNING id`,
			lib, typ, title, parent, index).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", title, err)
		}
		return id
	}
	file := func(itemID, path string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO media_files (media_item_id, file_path, file_size) VALUES ($1, $2, 1)`, itemID, path); err != nil {
			t.Fatalf("insert file %s: %v", path, err)
		}
	}
	num := func(n int) *int { return &n }

	// Numbered under the old rule: the prologue took its tag's 1, chapter 1
	// was refused and left unnumbered, chapter 2 kept 2.
	affected := item("audiobook", "A Clash of Kings", nil, nil)
	prologue := item("audiobook_chapter", "00 - Prologue", &affected, num(1))
	file(prologue, "/books/Martin/A Clash of Kings/00 - Prologue.mp3")
	one := item("audiobook_chapter", "01 - Arya I", &affected, nil)
	file(one, "/books/Martin/A Clash of Kings/01 - Arya I.mp3")
	two := item("audiobook_chapter", "02 - Sansa I", &affected, num(2))
	file(two, "/books/Martin/A Clash of Kings/02 - Sansa I.mp3")

	// No zero-led file: its numbers were right and stay.
	other := item("audiobook", "Gatsby", nil, nil)
	g1 := item("audiobook_chapter", "01 Gatsby", &other, num(1))
	file(g1, "/books/Fitzgerald/Gatsby/01 Gatsby.mp3")
	g10 := item("audiobook_chapter", "10 Gatsby", &other, num(10))
	file(g10, "/books/Fitzgerald/Gatsby/10 Gatsby.mp3")

	if err := goose.UpToContext(ctx, db, ".", 34); err != nil {
		t.Fatalf("up to 34: %v", err)
	}

	index := func(id string) *int {
		t.Helper()
		var n *int
		if err := pool.QueryRow(ctx, `SELECT index FROM media_items WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for name, id := range map[string]string{"prologue": prologue, "chapter 1": one, "chapter 2": two} {
		if n := index(id); n != nil {
			t.Errorf("%s of the zero-led book = %d, want cleared for the next scan", name, *n)
		}
	}
	if n := index(g1); n == nil || *n != 1 {
		t.Errorf("unaffected chapter 1 = %v, want 1", n)
	}
	if n := index(g10); n == nil || *n != 10 {
		t.Errorf("unaffected chapter 10 = %v, want 10", n)
	}
}
