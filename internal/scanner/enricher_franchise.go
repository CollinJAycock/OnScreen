package scanner

import (
	"context"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/metadata"
)

// FranchiseRecorder is told which TMDB collection (franchise) an enriched
// movie belongs to, so franchise collections stay in sync with enrichment.
// Implemented by franchise.Service; a narrow interface so the scanner doesn't
// import it.
type FranchiseRecorder interface {
	// RecordMovie stores the movie's collection (ref nil = belongs to none)
	// as answered for movieTMDBID, and re-syncs the affected collections.
	RecordMovie(ctx context.Context, itemID uuid.UUID, movieTMDBID int, ref *metadata.CollectionRef) error
}

// SetFranchiseRecorder wires the franchise hook. nil (the default) disables
// it; movies are then picked up later by the franchise maintenance task.
func (e *Enricher) SetFranchiseRecorder(r FranchiseRecorder) {
	e.franchise = r
}

// recordFranchise reports a just-written movie's TMDB collection. Only when
// the agent's result actually answered the question (a details payload —
// CollectionChecked) and only for the TMDB id that ended up on the row: an
// NFO-declared id that differs from the agent's match means the result's
// collection describes a different film. Failures are logged, never
// returned — a franchise hiccup must not fail enrichment; the maintenance
// task's reconcile pass heals it.
func (e *Enricher) recordFranchise(ctx context.Context, itemID uuid.UUID, storedTMDBID *int, result *metadata.MovieResult) {
	if e.franchise == nil || result == nil || !result.CollectionChecked {
		return
	}
	if storedTMDBID == nil || *storedTMDBID != result.TMDBID || result.TMDBID <= 0 {
		return
	}
	if err := e.franchise.RecordMovie(ctx, itemID, result.TMDBID, result.Collection); err != nil {
		e.logger.WarnContext(ctx, "franchise: record movie collection failed",
			"item_id", itemID, "tmdb_id", result.TMDBID, "err", err)
	}
}
