package scanner

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/nfo"
)

type nfoImportRecorder struct {
	lib, item uuid.UUID
	movie     *nfo.Movie
	calls     int
}

func (r *nfoImportRecorder) ImportMovie(_ context.Context, libraryID, itemID uuid.UUID, m *nfo.Movie) (int, error) {
	r.lib, r.item, r.movie = libraryID, itemID, m
	r.calls++
	return 1, nil
}

// Without a metadata agent (no TMDB key) nothing else is enriched, but a
// movie's NFO collections are still imported as it's scanned.
func TestEnrich_NFOCollectionsWithoutAgent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "movie.nfo"),
		[]byte(`<movie><title>Alien</title><set><name>Alien Collection</name></set></movie>`), 0o644); err != nil {
		t.Fatal(err)
	}
	e := NewEnricher(func() metadata.Agent { return nil }, nil, nil, func() []string { return nil }, slog.Default())
	rec := &nfoImportRecorder{}
	e.SetNFOCollectionImporter(rec)

	item := &media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "Alien"}
	file := &media.File{FilePath: filepath.Join(dir, "Alien.mkv")}
	if err := e.Enrich(context.Background(), item, file); err != nil {
		t.Fatal(err)
	}
	if rec.calls != 1 || rec.item != item.ID || rec.lib != item.LibraryID || rec.movie == nil || rec.movie.Set != "Alien Collection" {
		t.Fatalf("import = %+v", rec)
	}

	// No NFO, or no set or tags in it: nothing to import.
	rec.calls = 0
	if err := e.Enrich(context.Background(), item, &media.File{FilePath: filepath.Join(t.TempDir(), "x.mkv")}); err != nil {
		t.Fatal(err)
	}
	if rec.calls != 0 {
		t.Fatalf("imported without an NFO: %d", rec.calls)
	}
	// Shows aren't read for collections.
	if err := e.Enrich(context.Background(), &media.Item{ID: uuid.New(), Type: "show"}, file); err != nil {
		t.Fatal(err)
	}
	if rec.calls != 0 {
		t.Fatalf("a show imported NFO collections: %d", rec.calls)
	}
}
