package main

import (
	"context"
	"fmt"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/tmdb"
)

// GetTVSeasons satisfies requests.TMDB and v1.DiscoverSeasonTMDB: a show's
// season list for season-level requests. Like SearchMulti it only exists on
// *tmdb.Client; with no TMDB agent (or another backend) it reports TMDB as
// unavailable, which the callers treat as "can't tell" — the season endpoint
// answers 503, a request naming seasons is refused, fulfilment waits.
func (a *requestsTMDBAdapter) GetTVSeasons(ctx context.Context, tmdbID int) (*metadata.TVSeasonList, error) {
	agent := a.agentFn()
	if agent == nil {
		return nil, fmt.Errorf("%w: %w", errTMDBUnavailable, v1.ErrSeasonsTMDBUnavailable)
	}
	tc, ok := agent.(*tmdb.Client)
	if !ok {
		return nil, fmt.Errorf("%w: %w", errTMDBUnavailable, v1.ErrSeasonsTMDBUnavailable)
	}
	return tc.GetTVSeasons(ctx, tmdbID)
}
