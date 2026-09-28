package media

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func intp(v int) *int { return &v }

// seedTrack puts a track under album into the mock and returns it.
func seedTrack(q *mockQuerier, album uuid.UUID, title string, disc, index *int) Item {
	it := Item{ID: uuid.New(), Type: "track", Title: title, ParentID: &album, Index: index, DiscNumber: disc}
	q.items[it.ID] = it
	return it
}

func findTrack(t *testing.T, svc *Service, album uuid.UUID, title string, disc, index *int) *Item {
	t.Helper()
	got, err := svc.FindOrCreateHierarchyItem(context.Background(), CreateItemParams{
		Type: "track", Title: title, ParentID: &album, Index: index, DiscNumber: disc,
	})
	if err != nil {
		t.Fatalf("FindOrCreateHierarchyItem(%q): %v", title, err)
	}
	return got
}

// Tracks are numbered per disc. Matching a file to its sibling by number
// alone folded disc 2 track 1 into disc 1 track 1 (its file then played
// under the other song's title and disc 2's track vanished).
func TestFindOrCreateHierarchyItem_TrackNumberIsPerDisc(t *testing.T) {
	svc, q := newService(t)
	album := uuid.New()
	d1 := seedTrack(q, album, "Intro", intp(1), intp(1))

	got := findTrack(t, svc, album, "Overture", intp(2), intp(1))
	if got.ID == d1.ID {
		t.Fatal("disc 2 track 1 matched disc 1 track 1")
	}
	if got.DiscNumber == nil || *got.DiscNumber != 2 || got.Index == nil || *got.Index != 1 {
		t.Errorf("created track position = %v/%v, want disc 2 track 1", got.DiscNumber, got.Index)
	}
	// The same disc and number still resolves to the stored track, even
	// under a different title (the reason index matching exists).
	if again := findTrack(t, svc, album, "Intro (Remastered)", intp(1), intp(1)); again.ID != d1.ID {
		t.Error("disc 1 track 1 under a new title did not resolve to the stored track")
	}
}

// A track with no disc number reads as disc 1 — the single-disc case, and
// a file whose tags don't carry DISCNUMBER.
func TestFindOrCreateHierarchyItem_TrackMissingDiscIsDiscOne(t *testing.T) {
	svc, q := newService(t)
	album := uuid.New()
	stored := seedTrack(q, album, "Song", intp(1), intp(4))

	if got := findTrack(t, svc, album, "Song v2", nil, intp(4)); got.ID != stored.ID {
		t.Error("no-disc track 4 did not match disc 1 track 4")
	}
}

// The same title on two discs ("Intro" opening each) is two tracks once
// both discs are known.
func TestFindOrCreateHierarchyItem_SameTitleOnAnotherDisc(t *testing.T) {
	svc, q := newService(t)
	album := uuid.New()
	d1 := seedTrack(q, album, "Intro", intp(1), intp(1))

	if got := findTrack(t, svc, album, "Intro", intp(2), intp(1)); got.ID == d1.ID {
		t.Fatal("disc 2's Intro matched disc 1's Intro by title")
	}
}

// Rows from before disc numbers were stored have none. A disc-2 file must
// still find its own row by title — that is how the row gets its disc —
// and must not take a pre-disc row that is plainly another song.
func TestFindOrCreateHierarchyItem_PreDiscRows(t *testing.T) {
	svc, q := newService(t)
	album := uuid.New()
	// Disc 2 track 11 of a 10-track disc 1: its own row, disc unknown.
	own := seedTrack(q, album, "Encore", nil, intp(11))
	// Disc 1 track 1, disc unknown.
	other := seedTrack(q, album, "Intro", nil, intp(1))

	if got := findTrack(t, svc, album, "Encore", intp(2), intp(11)); got.ID != own.ID {
		t.Error("disc 2 file did not re-find its pre-disc row by title")
	}
	if got := findTrack(t, svc, album, "Overture", intp(2), intp(1)); got.ID == other.ID {
		t.Error("disc 2 track 1 matched a pre-disc row that is disc 1's track 1")
	}
}

// When two siblings share a number, the one that also has the file's title
// is the file's track — not whichever sorts first.
func TestFindOrCreateHierarchyItem_ExactMatchBeatsNumberOnly(t *testing.T) {
	svc, q := newService(t)
	album := uuid.New()
	seedTrack(q, album, "Other Song", nil, intp(3))
	mine := seedTrack(q, album, "My Song", nil, intp(3))

	// Mock children come back in map order; repeat so either order is hit.
	for i := 0; i < 20; i++ {
		if got := findTrack(t, svc, album, "My Song", nil, intp(3)); got.ID != mine.ID {
			t.Fatalf("resolved to %q, want the sibling with the same title and number", got.Title)
		}
	}
}

// The heal case: a track stored without a number is found by title when the
// file now reports one, and FillTrackPosition gives it the number.
func TestFillTrackPosition_HealsUnnumberedTrack(t *testing.T) {
	svc, q := newService(t)
	album := uuid.New()
	stored := seedTrack(q, album, "Second", nil, nil)
	numbered := seedTrack(q, album, "First", nil, intp(1))

	got := findTrack(t, svc, album, "Second", intp(1), intp(2))
	if got.ID != stored.ID {
		t.Fatal("unnumbered track was not found by title")
	}
	changed, err := svc.FillTrackPosition(context.Background(), got.ID, intp(2), intp(1))
	if err != nil || !changed {
		t.Fatalf("FillTrackPosition = %v, %v; want a change", changed, err)
	}
	if it := q.items[stored.ID]; it.Index == nil || *it.Index != 2 || it.DiscNumber == nil || *it.DiscNumber != 1 {
		t.Errorf("stored position = %v/%v, want disc 1 track 2", it.DiscNumber, it.Index)
	}

	// Fill-only: an existing number is never rewritten.
	if changed, _ := svc.FillTrackPosition(context.Background(), numbered.ID, intp(9), nil); changed {
		t.Error("FillTrackPosition overwrote an existing track number")
	}
	if it := q.items[numbered.ID]; *it.Index != 1 {
		t.Errorf("track number = %d, want 1 kept", *it.Index)
	}
	// Nothing to fill: no write at all.
	if changed, err := svc.FillTrackPosition(context.Background(), stored.ID, nil, nil); changed || err != nil {
		t.Errorf("FillTrackPosition(nil, nil) = %v, %v; want no-op", changed, err)
	}
}

// Merging two copies of an album (album dedupe) collapses tracks that share a
// position — and disc 1 track 1 and disc 2 track 1 don't share one.
func TestMergeChildren_TrackCollisionKeysOnDisc(t *testing.T) {
	svc, q := newService(t)
	survivor, loser := uuid.New(), uuid.New()
	seedTrack(q, survivor, "Intro", intp(1), intp(1))
	dup := seedTrack(q, loser, "Intro", intp(1), intp(1))
	disc2 := seedTrack(q, loser, "Overture", intp(2), intp(1))

	_, _, reparented, err := svc.mergeChildren(context.Background(), loser, survivor, "album")
	if err != nil {
		t.Fatalf("mergeChildren: %v", err)
	}
	if _, ok := q.items[dup.ID]; ok {
		t.Error("the duplicate disc 1 track 1 was not merged away")
	}
	if _, ok := q.items[disc2.ID]; !ok {
		t.Error("disc 2 track 1 was merged into disc 1 track 1")
	}
	if reparented != 1 {
		t.Errorf("reparented = %d, want 1 (disc 2 track 1 moved over)", reparented)
	}
}
