//go:build integration

// Face, people and memories queries against a real Postgres (migration
// 00057): visibility under a rating ceiling, the case-insensitive person
// name key, suggestions, the memories windows, and deleting face data.
//
// Run with: go test -tags=integration ./cmd/server/ -run Integration_Photo
package main

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestIntegration_Photo_FacesPeopleMemoriesSQL(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name: "Photos-" + uuid.NewString()[:6], Type: "photo", ScanPaths: []string{t.TempDir()},
		Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	photo := func(title, rating string, taken time.Time) uuid.UUID {
		t.Helper()
		id, err := q.CreatePhotoItem(ctx, gen.CreatePhotoItemParams{LibraryID: lib.ID, Title: title, SortTitle: title})
		if err != nil {
			t.Fatal(err)
		}
		if rating != "" {
			if _, err := pool.Exec(ctx, `UPDATE media_items SET content_rating = $2 WHERE id = $1`, id, rating); err != nil {
				t.Fatal(err)
			}
		}
		if err := q.UpsertPhotoMetadata(ctx, gen.UpsertPhotoMetadataParams{
			ItemID: id, TakenAt: pgtype.Timestamptz{Time: taken, Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	face := func(item uuid.UUID, x float32) uuid.UUID {
		t.Helper()
		f, err := q.CreatePhotoFace(ctx, gen.CreatePhotoFaceParams{
			ItemID: item, LibraryID: lib.ID, X: x, Y: 0.1, W: 0.2, H: 0.2, Score: 9, Source: "auto",
		})
		if err != nil {
			t.Fatal(err)
		}
		return f.ID
	}
	// Two photos with the same title are two photos now (00057).
	party := time.Date(2022, 6, 4, 15, 0, 0, 0, time.UTC)
	p1 := photo("IMG 0001", "G", party)
	p2 := photo("IMG 0001", "G", party.Add(2*time.Hour))
	adult := photo("late", "R", party.Add(3*time.Hour))
	other := photo("other day", "G", party.AddDate(0, 0, 10))
	f1, f2, fAdult, fOther := face(p1, 0.1), face(p2, 0.1), face(adult, 0.5), face(other, 0.1)

	alice, err := q.FindOrCreatePhotoPerson(ctx, gen.FindOrCreatePhotoPersonParams{LibraryID: lib.ID, Name: "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := q.FindOrCreatePhotoPerson(ctx, gen.FindOrCreatePhotoPersonParams{LibraryID: lib.ID, Name: "alice"})
	if err != nil || again.ID != alice.ID {
		t.Fatalf("names are case-insensitive per library: %v %v", again.ID, err)
	}
	set := func(face, person uuid.UUID) {
		t.Helper()
		if err := q.SetPhotoFacePerson(ctx, gen.SetPhotoFacePersonParams{ID: face, PersonID: pgtype.UUID{Bytes: person, Valid: true}}); err != nil {
			t.Fatal(err)
		}
	}
	set(f1, alice.ID)
	bob, err := q.FindOrCreatePhotoPerson(ctx, gen.FindOrCreatePhotoPersonParams{LibraryID: lib.ID, Name: "Bob"})
	if err != nil {
		t.Fatal(err)
	}
	set(fAdult, bob.ID) // Bob is only on an R-rated photo

	pg := int32(1) // a ceiling below R
	people, err := q.ListPhotoPeople(ctx, gen.ListPhotoPeopleParams{LibraryID: lib.ID, MaxRatingRank: &pg})
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 1 || people[0].Name != "Alice" || people[0].CoverItemID != p1 || people[0].PhotoCount != 1 {
		t.Fatalf("capped people list = %+v, want only Alice", people)
	}
	all, err := q.ListPhotoPeople(ctx, gen.ListPhotoPeopleParams{LibraryID: lib.ID})
	if err != nil || len(all) != 2 {
		t.Fatalf("unrestricted people list = %+v, %v", all, err)
	}
	if n, _ := q.CountPhotoPersonItems(ctx, gen.CountPhotoPersonItemsParams{PersonID: bob.ID, MaxRatingRank: &pg}); n != 0 {
		t.Fatalf("Bob's visible photos under the ceiling = %d", n)
	}

	sugg, err := q.ListPhotoPersonSuggestions(ctx, gen.ListPhotoPersonSuggestionsParams{
		LibraryID: lib.ID, PersonID: alice.ID, Lim: 10, MaxRatingRank: &pg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sugg) != 1 || sugg[0].ID != f2 {
		t.Fatalf("suggestions = %+v, want the same-party face %v (not %v)", sugg, f2, fOther)
	}

	// Memories: the party's day.
	from := []pgtype.Timestamptz{{Time: time.Date(2022, 6, 4, 0, 0, 0, 0, time.UTC), Valid: true}}
	to := []pgtype.Timestamptz{{Time: time.Date(2022, 6, 5, 0, 0, 0, 0, time.UTC), Valid: true}}
	mem, err := q.ListPhotoMemoryCandidates(ctx, gen.ListPhotoMemoryCandidatesParams{
		LibraryIds: []uuid.UUID{lib.ID}, RangeFrom: from, RangeTo: to, Lim: 100, MaxRatingRank: &pg,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mem) != 2 || mem[0].ID != p1 || mem[0].FaceCount != 1 {
		t.Fatalf("memory candidates = %+v", mem)
	}
	if n, _ := q.CountDatedPhotos(ctx, []uuid.UUID{lib.ID}); n != 4 {
		t.Fatalf("dated photos = %d", n)
	}

	// Face detection queue + delete all face data.
	if err := q.RecordPhotoFaceScan(ctx, gen.RecordPhotoFaceScanParams{ItemID: p1, LibraryID: lib.ID, Faces: 1, Status: "ok"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := q.CountPhotosForFaceScan(ctx, pgtype.UUID{Bytes: lib.ID, Valid: true}); n != 3 {
		t.Fatalf("photos awaiting detection = %d, want 3", n)
	}
	lid := pgtype.UUID{Bytes: lib.ID, Valid: true}
	if n, err := q.DeletePhotoFacesForLibrary(ctx, lid); err != nil || n != 4 {
		t.Fatalf("deleted faces %d, %v", n, err)
	}
	if n, err := q.DeletePhotoPeopleForLibrary(ctx, lid); err != nil || n != 2 {
		t.Fatalf("deleted people %d, %v", n, err)
	}
	if n, err := q.DeletePhotoFaceScansForLibrary(ctx, lid); err != nil || n != 1 {
		t.Fatalf("deleted scans %d, %v", n, err)
	}
	if left, _ := q.ListPhotoFacesForItem(ctx, p1); len(left) != 0 {
		t.Fatalf("faces left: %v", left)
	}
}
