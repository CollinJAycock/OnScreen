//go:build integration

package gen_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// An audiobook's original_title holds its author (the scanner stashes it
// there), so the dedupe queries must compare audiobooks by title alone.
// Comparing original_title first made every book by one author a duplicate
// of the others: a scan merged them into one book and soft-deleted the
// rest. Albums still compare by original_title, their tag spelling.
func TestAudiobookDedupe_DoesNotMergeAnAuthorsBooks(t *testing.T) {
	ctx := context.Background()
	q := gen.New(testdb.New(t))
	lib := seedLibrary(ctx, t, q, "books-"+uuid.NewString()[:8])

	item := func(typ, title, original string, parent uuid.UUID) uuid.UUID {
		p := gen.CreateMediaItemParams{LibraryID: lib, Type: typ, Title: title, SortTitle: title}
		if original != "" {
			p.OriginalTitle = &original
		}
		if parent != uuid.Nil {
			p.ParentID = pgtype.UUID{Bytes: parent, Valid: true}
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatalf("create %s %q: %v", typ, title, err)
		}
		return row.ID
	}
	author := item("book_author", "Same Author", "", uuid.Nil)
	item("audiobook", "Alpha", "Same Author", author)
	item("audiobook", "Beta", "Same Author", author)
	dupA := item("audiobook", "Gamma", "Same Author", author)
	dupB := item("audiobook", "Gamma", "Same Author", author)

	pairs, err := q.ListDuplicateChildItems(ctx, gen.ListDuplicateChildItemsParams{
		Type: "audiobook", ParentID: pgtype.UUID{Bytes: author, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || !((pairs[0].LoserID == dupB && pairs[0].SurvivorID == dupA) || (pairs[0].LoserID == dupA && pairs[0].SurvivorID == dupB)) {
		t.Errorf("same-author dedupe: %+v; want only the two Gammas", pairs)
	}

	// Across authors: only the same book under two authors is a duplicate.
	other := item("book_author", "Other Author", "", uuid.Nil)
	item("audiobook", "Delta", "Same Author", other)
	cross := item("audiobook", "Alpha", "Other Author", other)
	lpairs, err := q.ListLibraryAudiobookDuplicates(ctx, lib)
	if err != nil {
		t.Fatal(err)
	}
	var sawCrossAlpha bool
	for _, p := range lpairs {
		if p.LoserID == cross || p.SurvivorID == cross {
			sawCrossAlpha = true
		}
	}
	// Alpha x2 and Gamma x2: two pairs. Delta shares only an author name.
	if len(lpairs) != 2 || !sawCrossAlpha {
		t.Errorf("library dedupe: %d pairs %+v; want the two Alphas and the two Gammas", len(lpairs), lpairs)
	}

	// Albums keep comparing by original_title (tag spellings vary).
	artist := item("artist", "Band", "", uuid.Nil)
	item("album", "Abbey Road", "Abbey Road", artist)
	item("album", "Abbey Road (Remastered)", "Abbey Road", artist)
	apairs, err := q.ListDuplicateChildItems(ctx, gen.ListDuplicateChildItemsParams{
		Type: "album", ParentID: pgtype.UUID{Bytes: artist, Valid: true},
	})
	if err != nil || len(apairs) != 1 {
		t.Errorf("album variants must still merge: %+v, %v", apairs, err)
	}
}
