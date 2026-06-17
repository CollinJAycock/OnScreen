// Package ratings implements per-(user, item) star ratings (0-10) plus the
// community average computed over all users' ratings at read time.
//
// Distinct from the external provider scores stored on media_items
// (rating = critic, audience_rating = audience): this is what *your* users
// think of a title, surfaced on the item detail page alongside the provider
// numbers.
package ratings

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
)

const (
	// ScoreMin / ScoreMax bound a rating and match the DB CHECK constraint.
	// 0-10 with halves lets a 5-star UI map each star to two points.
	ScoreMin = 0.0
	ScoreMax = 10.0
)

// ErrNotFound is returned by Get when the user hasn't rated the item. The API
// layer maps it to a 404 on the explicit rating endpoint, or to "no user
// rating" (field omitted) when building the item detail response.
var ErrNotFound = errors.New("rating not found")

// ErrInvalidScore is returned by Set when the score is outside the allowed
// range. The API layer maps it to a 400 (client error); any other Set error is
// a DB failure and must map to 500, not be echoed back to the client.
var ErrInvalidScore = errors.New("rating out of range")

// Querier is the subset of generated queries this service needs. *gen.Queries
// satisfies it.
type Querier interface {
	UpsertUserRating(ctx context.Context, arg gen.UpsertUserRatingParams) error
	GetUserRating(ctx context.Context, arg gen.GetUserRatingParams) (float64, error)
	DeleteUserRating(ctx context.Context, arg gen.DeleteUserRatingParams) error
	GetCommunityRating(ctx context.Context, mediaItemID uuid.UUID) (gen.GetCommunityRatingRow, error)
}

// Service is the ratings domain service.
type Service struct{ q Querier }

// New constructs a ratings Service over the given queries.
func New(q Querier) *Service { return &Service{q: q} }

// Get returns the user's own rating for the item, or ErrNotFound when unset.
func (s *Service) Get(ctx context.Context, userID, mediaID uuid.UUID) (float64, error) {
	score, err := s.q.GetUserRating(ctx, gen.GetUserRatingParams{UserID: userID, MediaItemID: mediaID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return score, nil
}

// Set creates or updates the user's rating. Scores outside [ScoreMin, ScoreMax]
// are rejected before they reach the DB so the caller gets a clear 400.
func (s *Service) Set(ctx context.Context, userID, mediaID uuid.UUID, score float64) error {
	if score < ScoreMin || score > ScoreMax {
		return fmt.Errorf("%w: %.1f not in [%.0f, %.0f]", ErrInvalidScore, score, ScoreMin, ScoreMax)
	}
	return s.q.UpsertUserRating(ctx, gen.UpsertUserRatingParams{UserID: userID, MediaItemID: mediaID, Score: score})
}

// Clear removes the user's rating entirely (the "un-rate" action). Removing a
// non-existent rating is a no-op, not an error.
func (s *Service) Clear(ctx context.Context, userID, mediaID uuid.UUID) error {
	return s.q.DeleteUserRating(ctx, gen.DeleteUserRatingParams{UserID: userID, MediaItemID: mediaID})
}

// CommunityAverage returns the mean of all users' ratings for the item and the
// number of ratings. Zero ratings → (0, 0, nil).
func (s *Service) CommunityAverage(ctx context.Context, mediaID uuid.UUID) (float64, int, error) {
	row, err := s.q.GetCommunityRating(ctx, mediaID)
	if err != nil {
		return 0, 0, err
	}
	return row.Average, int(row.Count), nil
}
