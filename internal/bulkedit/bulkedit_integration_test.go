//go:build integration

package bulkedit_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/bulkedit"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestBulkEdit_Integration runs a bulk edit against Postgres: a mixed
// selection gets the content rating and genre add where they apply and
// aren't locked, every written field is locked, a locked field is kept
// unless the request unlocks it, labels reach every live item, and a
// failure part-way rolls the whole edit back.
//
// Run with: go test -tags 'dev integration' -run Integration ./internal/bulkedit/
func TestBulkEdit_Integration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	svc := bulkedit.New(pool)

	var lib uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ('M', 'movie', '{/m}') RETURNING id`).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	insert := func(typ, title string, locked []string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if locked == nil {
			locked = []string{}
		}
		if err := pool.QueryRow(ctx, `
			INSERT INTO media_items (library_id, type, title, sort_title, content_rating, genres, locked_fields)
			VALUES ($1, $2, $3, $3, 'PG', '{Drama}', $4) RETURNING id`, lib, typ, title, locked).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	free := insert("movie", "Free", nil)
	locked := insert("movie", "Locked", []string{"content_rating"})
	photo := insert("photo", "Photo", nil)

	type state struct {
		rating string
		genres []string
		locked []string
		labels []string
	}
	read := func(id uuid.UUID) state {
		t.Helper()
		var s state
		var cr *string
		if err := pool.QueryRow(ctx, `SELECT content_rating, genres, locked_fields, labels FROM media_items WHERE id = $1`, id).
			Scan(&cr, &s.genres, &s.locked, &s.labels); err != nil {
			t.Fatal(err)
		}
		if cr != nil {
			s.rating = *cr
		}
		return s
	}

	r := "R"
	res, err := svc.Apply(ctx, bulkedit.Request{
		ItemIDs:          []uuid.UUID{free, locked, photo},
		SetContentRating: true, ContentRating: &r,
		AddGenres: []string{"Thriller"},
		AddLabels: []string{"late night"}, MaxLabels: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 3 || len(res.Updated) != 2 {
		t.Errorf("matched=%d updated=%v", res.Matched, res.Updated)
	}
	if s := read(free); s.rating != "R" || !slices.Equal(s.genres, []string{"Drama", "Thriller"}) ||
		!slices.Equal(s.locked, []string{"content_rating", "genres"}) || !slices.Equal(s.labels, []string{"late night"}) {
		t.Errorf("free = %+v", s)
	}
	if s := read(locked); s.rating != "PG" || !slices.Equal(s.genres, []string{"Drama", "Thriller"}) {
		t.Errorf("locked item's rating was overwritten or genres missed: %+v", s)
	}
	if s := read(photo); s.rating != "PG" || len(s.locked) != 0 || !slices.Equal(s.labels, []string{"late night"}) {
		t.Errorf("photo = %+v", s)
	}

	// Unlocking in the same request lets the value through, and it's
	// locked again.
	g := "G"
	if _, err := svc.Apply(ctx, bulkedit.Request{
		ItemIDs: []uuid.UUID{locked}, SetContentRating: true, ContentRating: &g,
		Unlock: []string{"content_rating"},
	}); err != nil {
		t.Fatal(err)
	}
	if s := read(locked); s.rating != "G" || !slices.Contains(s.locked, "content_rating") {
		t.Errorf("unlock+set = %+v", s)
	}

	// One transaction: when a later item's write fails, the earlier item's
	// write is rolled back too. Items are written in id order, so the first
	// id below is written before the second one fails (a test trigger).
	first := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	boom := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	for id, title := range map[uuid.UUID]string{first: "First", boom: "Boom"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO media_items (id, library_id, type, title, sort_title, genres)
			VALUES ($1, $2, 'movie', $3, $3, '{Drama}')`, id, lib, title); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION bulk_edit_test_fail() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.title = 'Boom' THEN RAISE EXCEPTION 'boom'; END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER bulk_edit_test_fail BEFORE UPDATE ON media_items
			FOR EACH ROW EXECUTE FUNCTION bulk_edit_test_fail();`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Apply(ctx, bulkedit.Request{
		ItemIDs: []uuid.UUID{boom, first}, SetGenres: true, Genres: []string{"Western"},
		AddLabels: []string{"x"}, MaxLabels: 50,
	}); err == nil {
		t.Fatal("edit with a failing item succeeded")
	}
	if s := read(first); !slices.Equal(s.genres, []string{"Drama"}) || len(s.locked) != 0 || len(s.labels) != 0 {
		t.Errorf("failed bulk edit left a partial write: %+v", s)
	}
}
