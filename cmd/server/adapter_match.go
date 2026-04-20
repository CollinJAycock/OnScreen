package main

import (
	"context"

	"github.com/google/uuid"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/scanner"
)

// matchSearchAdapter bridges scanner.Enricher search methods to the v1.ItemMatchSearcher interface.
type matchSearchAdapter struct {
	enricher *scanner.Enricher
}

func (a *matchSearchAdapter) SearchTVCandidates(ctx context.Context, query string) ([]v1.MatchCandidate, error) {
	results, err := a.enricher.SearchTVCandidates(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]v1.MatchCandidate, len(results))
	for i, r := range results {
		out[i] = v1.MatchCandidate{
			TMDBID:    r.TMDBID,
			Title:     r.Title,
			Year:      r.Year,
			Summary:   r.Summary,
			PosterURL: r.PosterURL,
			Rating:    r.Rating,
		}
	}
	return out, nil
}

func (a *matchSearchAdapter) SearchMovieCandidates(ctx context.Context, query string) ([]v1.MatchCandidate, error) {
	results, err := a.enricher.SearchMovieCandidates(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]v1.MatchCandidate, len(results))
	for i, r := range results {
		out[i] = v1.MatchCandidate{
			TMDBID:    r.TMDBID,
			Title:     r.Title,
			Year:      r.Year,
			Summary:   r.Summary,
			PosterURL: r.PosterURL,
			Rating:    r.Rating,
		}
	}
	return out, nil
}

// favoritesChecker adapts gen.Queries.IsFavorite to the v1.ItemFavoriteChecker interface.
type favoritesChecker struct{ q *gen.Queries }

func (f *favoritesChecker) IsFavorite(ctx context.Context, userID, mediaID uuid.UUID) (bool, error) {
	return f.q.IsFavorite(ctx, gen.IsFavoriteParams{UserID: userID, MediaID: mediaID})
}
