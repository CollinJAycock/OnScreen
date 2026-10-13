// Package externalratings keeps movies' and shows' IMDb, Rotten Tomatoes
// (critics) and Metacritic scores, fetched from OMDb by IMDb ID. The daily
// external_ratings task fetches the scores titles are missing and refreshes
// stale ones, within a request budget under OMDb's free daily limit. Without
// an OMDb key it does nothing.
package externalratings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata/omdb"
)

const (
	// DefaultBudget is how many titles one run looks up: under the free
	// key's 1000 requests a day, leaving room for a manual run.
	DefaultBudget = 900
	// recentRefresh: a title from this year or last still gathers reviews.
	recentRefresh = 7 * 24 * time.Hour
	// oldRefresh: an older title's scores barely move.
	oldRefresh = 90 * 24 * time.Hour
	// missingRetry: OMDb may add a title it didn't know.
	missingRetry = 30 * 24 * time.Hour
	// requestInterval paces lookups (OMDb publishes no per-second limit).
	requestInterval = 250 * time.Millisecond
	// maxFailures in a row stop a run: OMDb is down or unreachable.
	maxFailures = 5
)

// Querier is the database surface the service needs. Satisfied by *gen.Queries.
type Querier interface {
	ListExternalRatingCandidates(ctx context.Context, arg gen.ListExternalRatingCandidatesParams) ([]string, error)
	UpsertExternalRatings(ctx context.Context, arg gen.UpsertExternalRatingsParams) error
	CountExternalRatingCandidates(ctx context.Context) (gen.CountExternalRatingCandidatesRow, error)
}

// KeySource returns the OMDb API key ("" = not set). Satisfied by
// *settings.Service, read on every run so a key saved in the admin UI
// takes effect without a restart.
type KeySource interface {
	OMDbAPIKey(ctx context.Context) string
}

// Fetcher looks up one title. Satisfied by *omdb.Client.
type Fetcher interface {
	Get(ctx context.Context, imdbID string) (omdb.Ratings, error)
}

// Service refreshes external ratings.
type Service struct {
	q        Querier
	keys     KeySource
	fetcher  func(key string) Fetcher
	logger   *slog.Logger
	now      func() time.Time
	budget   int
	interval time.Duration
}

// New builds the service.
func New(q Querier, keys KeySource, logger *slog.Logger) *Service {
	return &Service{
		q: q, keys: keys, logger: logger, now: time.Now, budget: DefaultBudget, interval: requestInterval,
		fetcher: func(key string) Fetcher { return omdb.New(key) },
	}
}

// Refresh is the external_ratings task: look up the titles whose scores are
// missing or stale, up to the budget. It returns a summary for the task log.
func (s *Service) Refresh(ctx context.Context) (string, error) {
	key := s.keys.OMDbAPIKey(ctx)
	if key == "" {
		return "skipped: no OMDb API key set (Settings ▸ General)", nil
	}
	now := s.now()
	ids, err := s.q.ListExternalRatingCandidates(ctx, gen.ListExternalRatingCandidatesParams{
		MissingBefore: ts(now.Add(-missingRetry)),
		RecentYear:    int32(now.Year() - 1),
		RecentBefore:  ts(now.Add(-recentRefresh)),
		OldBefore:     ts(now.Add(-oldRefresh)),
		MaxIds:        int32(s.budget),
	})
	if err != nil {
		return "", fmt.Errorf("list titles: %w", err)
	}
	fetch := s.fetcher(key)
	var rated, missing, failed, inARow int
	stopped := ""
	for i, id := range ids {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if i > 0 && s.interval > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(s.interval):
			}
		}
		r, err := fetch.Get(ctx, id)
		switch {
		case err == nil:
			inARow = 0
			if err := s.q.UpsertExternalRatings(ctx, gen.UpsertExternalRatingsParams{
				ImdbID: id, ImdbRating: r.IMDb, ImdbVotes: r.IMDbVotes,
				RottenTomatoes: r.RottenTomatoes, Metacritic: r.Metacritic, Found: true,
			}); err != nil {
				return "", fmt.Errorf("save ratings: %w", err)
			}
			rated++
		case errors.Is(err, omdb.ErrNotFound):
			inARow = 0
			if err := s.q.UpsertExternalRatings(ctx, gen.UpsertExternalRatingsParams{ImdbID: id, Found: false}); err != nil {
				return "", fmt.Errorf("save ratings: %w", err)
			}
			missing++
		case errors.Is(err, omdb.ErrInvalidKey):
			return "", errors.New("OMDb refused the API key")
		case errors.Is(err, omdb.ErrLimitReached):
			stopped = "; stopped: the OMDb key's daily limit is used up"
		default:
			failed++
			inARow++
			s.logger.WarnContext(ctx, "external ratings: lookup failed", "imdb_id", id, "err", err)
			if inARow >= maxFailures {
				return "", fmt.Errorf("OMDb lookups keep failing: %w", err)
			}
		}
		if stopped != "" {
			break
		}
	}
	summary := fmt.Sprintf("looked up %d titles: %d rated, %d unknown to OMDb, %d failed%s",
		rated+missing+failed, rated, missing, failed, stopped)
	if c, err := s.q.CountExternalRatingCandidates(ctx); err == nil {
		summary += fmt.Sprintf(" (%d of %d titles with an IMDb ID have scores)", c.Rated, c.WithImdbID)
	}
	return summary, nil
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
