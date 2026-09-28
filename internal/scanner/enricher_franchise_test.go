package scanner

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata"
)

type franchiseCall struct {
	itemID uuid.UUID
	tmdbID int
	ref    *metadata.CollectionRef
}

type recordingFranchise struct {
	calls []franchiseCall
	err   error
}

func (r *recordingFranchise) RecordMovie(_ context.Context, itemID uuid.UUID, tmdbID int, ref *metadata.CollectionRef) error {
	r.calls = append(r.calls, franchiseCall{itemID, tmdbID, ref})
	return r.err
}

func franchiseMovieFixture(t *testing.T, result *metadata.MovieResult) (*Enricher, *mockUpdater, *recordingFranchise, uuid.UUID) {
	t.Helper()
	updater := newMockUpdater()
	itemID := uuid.New()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "Alien"}
	e := newTestEnricher(&mockAgent{searchMovieResult: result}, updater, nil)
	rec := &recordingFranchise{}
	e.SetFranchiseRecorder(rec)
	return e, updater, rec, itemID
}

func TestEnrichMovie_RecordsFranchiseFromDetails(t *testing.T) {
	ref := &metadata.CollectionRef{TMDBID: 8091, Name: "Alien Collection"}
	e, updater, rec, itemID := franchiseMovieFixture(t, &metadata.MovieResult{
		TMDBID: 348, Title: "Alien", Year: 1979, Collection: ref, CollectionChecked: true,
	})
	if err := e.Enrich(context.Background(), updater.items[itemID], &media.File{FilePath: "/media/movies/Alien.mkv"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("RecordMovie calls = %d, want 1", len(rec.calls))
	}
	c := rec.calls[0]
	if c.itemID != itemID || c.tmdbID != 348 || c.ref != ref {
		t.Fatalf("call = %+v", c)
	}
}

// A details payload with belongs_to_collection null is a definitive "none",
// which must still be recorded (so the movie leaves the backfill queue and a
// previous membership is dropped).
func TestEnrichMovie_RecordsDefinitiveNoCollection(t *testing.T) {
	e, updater, rec, itemID := franchiseMovieFixture(t, &metadata.MovieResult{
		TMDBID: 550, Title: "Fight Club", CollectionChecked: true,
	})
	if err := e.Enrich(context.Background(), updater.items[itemID], &media.File{FilePath: "/media/movies/FC.mkv"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 || rec.calls[0].ref != nil {
		t.Fatalf("calls = %+v", rec.calls)
	}
}

// Search-derived results never carried the field: nothing is recorded, the
// maintenance backfill looks the movie up later.
func TestEnrichMovie_UncheckedCollectionNotRecorded(t *testing.T) {
	e, updater, rec, itemID := franchiseMovieFixture(t, &metadata.MovieResult{TMDBID: 348, Title: "Alien"})
	if err := e.Enrich(context.Background(), updater.items[itemID], &media.File{FilePath: "/media/movies/Alien.mkv"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("calls = %+v, want none", rec.calls)
	}
}

// A recorder failure is logged, never fails enrichment.
func TestEnrichMovie_FranchiseErrorDoesNotFailEnrich(t *testing.T) {
	e, updater, rec, itemID := franchiseMovieFixture(t, &metadata.MovieResult{
		TMDBID: 348, Title: "Alien", CollectionChecked: true,
		Collection: &metadata.CollectionRef{TMDBID: 8091, Name: "Alien Collection"},
	})
	rec.err = errors.New("db down")
	if err := e.Enrich(context.Background(), updater.items[itemID], &media.File{FilePath: "/media/movies/Alien.mkv"}); err != nil {
		t.Fatalf("enrich must not fail on a franchise error: %v", err)
	}
	if len(updater.updateCalls) != 1 {
		t.Fatalf("metadata must still be written, got %d updates", len(updater.updateCalls))
	}
}

func TestMatchItem_Movie_RecordsFranchise(t *testing.T) {
	ref := &metadata.CollectionRef{TMDBID: 8091, Name: "Alien Collection"}
	e, updater, rec, itemID := franchiseMovieFixture(t, &metadata.MovieResult{
		TMDBID: 679, Title: "Aliens", Year: 1986, Collection: ref, CollectionChecked: true,
	})
	updater.files[itemID] = []media.File{{ID: uuid.New(), FilePath: "/media/movies/Aliens.mkv", Status: "active"}}
	if err := e.MatchItem(context.Background(), itemID, 679); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 || rec.calls[0].tmdbID != 679 || rec.calls[0].itemID != itemID {
		t.Fatalf("calls = %+v", rec.calls)
	}
}

func TestRecordFranchise_NilRecorderIsNoop(t *testing.T) {
	updater := newMockUpdater()
	itemID := uuid.New()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "Alien"}
	e := newTestEnricher(&mockAgent{searchMovieResult: &metadata.MovieResult{TMDBID: 348, Title: "Alien", CollectionChecked: true}}, updater, nil)
	if err := e.Enrich(context.Background(), updater.items[itemID], &media.File{FilePath: "/media/movies/Alien.mkv"}); err != nil {
		t.Fatal(err)
	}
}
