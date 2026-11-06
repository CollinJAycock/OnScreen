//go:build integration

package v1

import (
	"context"
	"testing"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestListMediaItemLetters_Integration checks the A-Z jump index against
// the listing it indexes: each letter's first row is where that letter
// starts in ListMediaItems (sort_title, id), whatever the database's
// collation, and the letters fold accents and case.
func TestListMediaItemLetters_Integration(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	libID := mustCreateLibrary(ctx, t, q)

	seed := func(typ, sortTitle string) {
		t.Helper()
		if _, err := q.CreateMediaItem(ctx, gen.CreateMediaItemParams{
			LibraryID: libID, Type: typ, Title: sortTitle, SortTitle: sortTitle,
		}); err != nil {
			t.Fatalf("seed %q: %v", sortTitle, err)
		}
	}
	for _, s := range []string{"Zodiac", "alien", "Amélie", "Ænima", "Batman", "Éclair", "12 Monkeys", "[REC]", "Brazil", "Arrival"} {
		seed("movie", s)
	}
	seed("show", "Andor") // another type: not in a movie listing

	rows, err := q.ListMediaItemLetters(ctx, gen.ListMediaItemLettersParams{LibraryID: libID, Type: "movie"})
	if err != nil {
		t.Fatalf("ListMediaItemLetters: %v", err)
	}
	wantCount := map[string]int64{"#": 2, "A": 4, "B": 2, "E": 1, "Z": 1}
	var total int64
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Letter] += r.Count
		total += r.Count
	}
	for l, n := range wantCount {
		if got[l] != n {
			t.Errorf("letter %q: count %d, want %d (all: %+v)", l, got[l], n, rows)
		}
	}
	if len(got) != len(wantCount) || total != 10 {
		t.Errorf("letters %v (total %d), want %v (total 10)", got, total, wantCount)
	}

	listing, err := q.ListMediaItems(ctx, gen.ListMediaItemsParams{LibraryID: libID, Type: "movie", Limit: 100})
	if err != nil {
		t.Fatalf("ListMediaItems: %v", err)
	}
	letterOf := map[string]string{
		"Zodiac": "Z", "alien": "A", "Amélie": "A", "Ænima": "A", "Batman": "B", "Éclair": "E",
		"12 Monkeys": "#", "[REC]": "#", "Brazil": "B", "Arrival": "A",
	}
	for _, r := range rows {
		if r.FirstRow < 0 || r.LastRow >= int64(len(listing)) || r.FirstRow > r.LastRow {
			t.Fatalf("letter %q: rows %d-%d outside the %d-item listing", r.Letter, r.FirstRow, r.LastRow, len(listing))
		}
		if l := letterOf[listing[r.FirstRow].SortTitle]; l != r.Letter {
			t.Errorf("letter %q: first row %d is %q (%s)", r.Letter, r.FirstRow, listing[r.FirstRow].SortTitle, l)
		}
		if r.FirstRow > 0 && letterOf[listing[r.FirstRow-1].SortTitle] == r.Letter {
			t.Errorf("letter %q: row %d before its first row is %q, the same letter", r.Letter, r.FirstRow-1, listing[r.FirstRow-1].SortTitle)
		}
		if l := letterOf[listing[r.LastRow].SortTitle]; l != r.Letter {
			t.Errorf("letter %q: last row %d is %q (%s)", r.Letter, r.LastRow, listing[r.LastRow].SortTitle, l)
		}
	}
}
