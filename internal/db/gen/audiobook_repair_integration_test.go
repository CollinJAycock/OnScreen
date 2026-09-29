//go:build integration

package gen_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// The aftermath of the old author-name dedupe, and what the repair queries
// make of it: only the book it merged away comes back (with its chapter),
// and only its author's live files are marked for re-import.
func TestRepairMergedAudiobooks(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)
	lib := seedLibrary(ctx, t, q, "books-"+uuid.NewString()[:8])

	item := func(typ, title, author string, parent uuid.UUID) uuid.UUID {
		p := gen.CreateMediaItemParams{LibraryID: lib, Type: typ, Title: title, SortTitle: title}
		if author != "" {
			p.OriginalTitle = &author
		}
		if parent != uuid.Nil {
			p.ParentID = pgtype.UUID{Bytes: parent, Valid: true}
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		return row.ID
	}
	file := func(itemID uuid.UUID, name string) uuid.UUID {
		f, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{MediaItemID: itemID, FilePath: "/books/" + name, FileSize: 1})
		if err != nil {
			t.Fatalf("create file %s: %v", name, err)
		}
		return f.ID
	}
	// deleted_at before the cutoff (the migration ran when the database was
	// created), or after it.
	deleteAt := func(id uuid.UUID, before bool) {
		at := "now() + interval '1 minute'"
		if before {
			at = "now() - interval '1 day'"
		}
		if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = `+at+` WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}

	author := item("book_author", "Author", "", uuid.Nil)
	survivor := item("audiobook", "Survivor", "Author", author)
	fOwn := file(survivor, "Author/Survivor.m4b")
	fMoved := file(survivor, "Author/Victim.m4b") // moved here by the merge

	victim := item("audiobook", "Victim", "Author", author)
	victimChapter := item("audiobook_chapter", "Chapter 1", "", victim)
	deleteAt(victimChapter, true)
	deleteAt(victim, true)

	userDeleted := item("audiobook", "Removed", "Author", author) // keeps its file
	file(userDeleted, "Author/Removed.m4b")
	deleteAt(userDeleted, true)

	sameTitle := item("audiobook", "Survivor", "Author", author) // a real duplicate
	deleteAt(sameTitle, true)

	later := item("audiobook", "Pruned Later", "Author", author) // deleted after the cutoff
	deleteAt(later, false)

	other := item("audiobook", "Elsewhere", "Other Author", uuid.Nil)
	fOther := file(other, "Other Author/Elsewhere.m4b")

	rows, err := q.RestoreMergedAudiobooks(ctx, lib)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != victim || rows[0].Author != "Author" {
		t.Fatalf("restored %+v; want only the merged-away book", rows)
	}
	alive := func(id uuid.UUID) bool {
		var deleted bool
		if err := pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM media_items WHERE id = $1`, id).Scan(&deleted); err != nil {
			t.Fatal(err)
		}
		return !deleted
	}
	if !alive(victim) || !alive(victimChapter) {
		t.Error("the book and its chapter must be restored")
	}
	for name, id := range map[string]uuid.UUID{"user-deleted": userDeleted, "same-title duplicate": sameTitle, "deleted after the cutoff": later} {
		if alive(id) {
			t.Errorf("%s book must stay deleted", name)
		}
	}

	n, err := q.ForceReimportAudiobookFiles(ctx, gen.ForceReimportAudiobookFilesParams{LibraryID: lib, Authors: []string{"Author"}})
	if err != nil || n != 2 {
		t.Fatalf("marked %d files, %v; want the survivor's two", n, err)
	}
	scanned := func(id uuid.UUID) time.Time {
		var at time.Time
		if err := pool.QueryRow(ctx, `SELECT scanned_at FROM media_files WHERE id = $1`, id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	if !scanned(fOwn).Equal(time.Unix(0, 0)) || !scanned(fMoved).Equal(time.Unix(0, 0)) {
		t.Error("the author's files must be marked for re-import")
	}
	if scanned(fOther).Equal(time.Unix(0, 0)) {
		t.Error("another author's files must be left alone")
	}

	// Once restored, nothing qualifies again: the repair acts once.
	if again, err := q.RestoreMergedAudiobooks(ctx, lib); err != nil || len(again) != 0 {
		t.Errorf("second run restored %+v, %v; want nothing", again, err)
	}
}
