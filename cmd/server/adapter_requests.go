package main

import (
	"context"
	"errors"

	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/tmdb"
)

// requestsTMDBAdapter satisfies requests.TMDB by reaching through agentFn
// for the live TMDB client. The lookup is dynamic so that admins toggling
// the TMDB key in settings take effect without a restart, matching how
// every other consumer of agentFn behaves.
//
// GetTVExternalIDs lives only on *tmdb.Client (not the metadata.Agent
// interface), so this adapter type-asserts to access it. When TMDB is
// unconfigured the adapter returns ErrTMDBUnavailable, which the requests
// service maps to ErrTMDBLookupFailed for the caller.
type requestsTMDBAdapter struct {
	agentFn func() metadata.Agent
}

var errTMDBUnavailable = errors.New("requests: tmdb agent not configured")

func (a *requestsTMDBAdapter) RefreshMovie(ctx context.Context, tmdbID int) (*metadata.MovieResult, error) {
	agent := a.agentFn()
	if agent == nil {
		return nil, errTMDBUnavailable
	}
	return agent.RefreshMovie(ctx, tmdbID)
}

func (a *requestsTMDBAdapter) RefreshTV(ctx context.Context, tmdbID int) (*metadata.TVShowResult, error) {
	agent := a.agentFn()
	if agent == nil {
		return nil, errTMDBUnavailable
	}
	return agent.RefreshTV(ctx, tmdbID)
}

func (a *requestsTMDBAdapter) GetTVExternalIDs(ctx context.Context, tmdbID int) (int, string, error) {
	agent := a.agentFn()
	if agent == nil {
		return 0, "", errTMDBUnavailable
	}
	// Sonarr's TVDB-native lookup needs the external ids resolved; only
	// *tmdb.Client provides that today. If a future agent backend lands
	// without external-id support, return zero so the caller falls back
	// to the title-based Sonarr lookup path.
	tc, ok := agent.(*tmdb.Client)
	if !ok {
		return 0, "", nil
	}
	return tc.GetTVExternalIDs(ctx, tmdbID)
}

// SearchMulti satisfies v1.DiscoverTMDB. Like GetTVExternalIDs, this method
// only exists on *tmdb.Client today; non-TMDB agents (none yet) get an empty
// result rather than an error so the Discover surface degrades to "no hits".
func (a *requestsTMDBAdapter) SearchMulti(ctx context.Context, query string, maxResults int) ([]tmdb.DiscoverResult, error) {
	agent := a.agentFn()
	if agent == nil {
		return nil, errTMDBUnavailable
	}
	tc, ok := agent.(*tmdb.Client)
	if !ok {
		return nil, nil
	}
	return tc.SearchMulti(ctx, query, maxResults)
}
