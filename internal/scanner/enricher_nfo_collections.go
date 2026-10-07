package scanner

import (
	"context"
	"os"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata/nfo"
)

// NFOCollectionImporter adds an enriched movie to the collections its
// movie.nfo names (<set>, and <tag> in sets_and_tags mode) when its library
// imports them. Implemented by collections.NFOImporter; a narrow interface so
// the scanner doesn't import it.
type NFOCollectionImporter interface {
	ImportMovie(ctx context.Context, libraryID, itemID uuid.UUID, m *nfo.Movie) (int, error)
}

// SetNFOCollectionImporter wires the NFO collections hook. nil (the default)
// disables it; movies are then picked up by the nfo_collections task.
func (e *Enricher) SetNFOCollectionImporter(i NFOCollectionImporter) {
	e.nfoCollections = i
}

// readMovieNFO parses a movie file's sidecar NFO (movie.nfo or
// <basename>.nfo); nil when there is none or it doesn't parse.
func (e *Enricher) readMovieNFO(ctx context.Context, file *media.File) *nfo.Movie {
	nfoPath, err := nfo.FindMovieNFO(file.FilePath)
	if err != nil {
		return nil
	}
	f, err := os.Open(nfoPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	parsed, err := nfo.ParseMovie(f)
	if err != nil {
		e.logger.WarnContext(ctx, "nfo parse failed; falling through to TMDB", "path", nfoPath, "err", err)
		return nil
	}
	return parsed
}

// importNFOCollections reports a movie's NFO groupings. Failures are logged,
// never returned: a collection hiccup must not fail enrichment, and the
// nfo_collections task picks the movie up again.
func (e *Enricher) importNFOCollections(ctx context.Context, libraryID, itemID uuid.UUID, m *nfo.Movie) {
	if e.nfoCollections == nil || m == nil || (m.Set == "" && len(m.Tags) == 0) {
		return
	}
	if _, err := e.nfoCollections.ImportMovie(ctx, libraryID, itemID, m); err != nil {
		e.logger.WarnContext(ctx, "nfo collections: import failed", "item_id", itemID, "err", err)
	}
}
