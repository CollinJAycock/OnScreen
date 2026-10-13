package v1

import (
	"context"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// ItemExternalRatingsDB reads a title's IMDb / Rotten Tomatoes / Metacritic
// scores by IMDb ID. Satisfied by *gen.Queries.
type ItemExternalRatingsDB interface {
	GetExternalRatings(ctx context.Context, imdbID string) (gen.ExternalRating, error)
}

// WithExternalRatings enables the "external_ratings" field on movie and
// show details.
func (h *ItemHandler) WithExternalRatings(db ItemExternalRatingsDB) *ItemHandler {
	h.externalRatings = db
	return h
}

// ExternalRatingsResponse is a title's scores from elsewhere, fetched from
// OMDb by the external_ratings task. Each score is omitted when unknown.
type ExternalRatingsResponse struct {
	IMDbID         string   `json:"imdb_id"`
	IMDbRating     *float64 `json:"imdb_rating,omitempty"` // 0-10
	IMDbVotes      *int32   `json:"imdb_votes,omitempty"`
	RottenTomatoes *int16   `json:"rotten_tomatoes,omitempty"` // critics' Tomatometer, 0-100
	Metacritic     *int16   `json:"metacritic,omitempty"`      // 0-100
}

// externalRatingsFor is a movie's or show's scores, or nil when it has no
// IMDb ID, no scores yet, or the lookup isn't wired.
func (h *ItemHandler) externalRatingsFor(ctx context.Context, item *media.Item) *ExternalRatingsResponse {
	if h.externalRatings == nil || item.IMDBID == nil || *item.IMDBID == "" ||
		(item.Type != "movie" && item.Type != "show") {
		return nil
	}
	r, err := h.externalRatings.GetExternalRatings(ctx, *item.IMDBID)
	if err != nil || !r.Found || (r.ImdbRating == nil && r.RottenTomatoes == nil && r.Metacritic == nil) {
		return nil
	}
	return &ExternalRatingsResponse{
		IMDbID:         r.ImdbID,
		IMDbRating:     r.ImdbRating,
		IMDbVotes:      r.ImdbVotes,
		RottenTomatoes: r.RottenTomatoes,
		Metacritic:     r.Metacritic,
	}
}
