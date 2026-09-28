package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/onscreen/onscreen/internal/franchise"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/tmdb"
)

// franchiseTMDBAdapter satisfies franchise.TMDB by reaching through agentFn
// for the live TMDB client (so a key set or rotated in settings applies
// without a restart, like every other agentFn consumer). The collection
// calls exist only on *tmdb.Client, so a nil or non-TMDB agent reads as
// "unavailable". TMDB's own error sentinels are mapped onto the franchise
// package's, which is all the service branches on.
type franchiseTMDBAdapter struct {
	agentFn func() metadata.Agent
}

func (a *franchiseTMDBAdapter) client() (*tmdb.Client, error) {
	tc, ok := a.agentFn().(*tmdb.Client)
	if !ok || tc == nil {
		return nil, franchise.ErrUnavailable
	}
	return tc, nil
}

func (a *franchiseTMDBAdapter) MovieCollection(ctx context.Context, movieTMDBID int) (*metadata.CollectionRef, error) {
	tc, err := a.client()
	if err != nil {
		return nil, err
	}
	ref, err := tc.MovieCollection(ctx, movieTMDBID)
	return ref, mapFranchiseTMDBErr(err)
}

func (a *franchiseTMDBAdapter) GetCollection(ctx context.Context, collectionTMDBID int) (*metadata.CollectionDetail, error) {
	tc, err := a.client()
	if err != nil {
		return nil, err
	}
	col, err := tc.GetCollection(ctx, collectionTMDBID)
	return col, mapFranchiseTMDBErr(err)
}

func mapFranchiseTMDBErr(err error) error {
	switch {
	case err == nil:
		return nil
	case tmdb.IsNotFound(err):
		return fmt.Errorf("%w: %w", franchise.ErrNotFound, err)
	case errors.Is(err, tmdb.ErrCircuitOpen):
		return fmt.Errorf("%w: %w", franchise.ErrUnavailable, err)
	default:
		return err
	}
}
