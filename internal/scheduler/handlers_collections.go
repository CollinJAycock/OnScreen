package scheduler

import (
	"context"
	"encoding/json"
)

// SmartCollectionsRefresher refreshes every smart collection's members from
// its rules. Satisfied by *collections.Smart.
type SmartCollectionsRefresher interface {
	RefreshAll(ctx context.Context) (string, error)
}

// NewSmartCollectionsHandler returns the smart_collections task: bring every
// smart (rule-based) manual collection up to date with its rules, so movies
// and shows added or re-matched since the last run join or leave it.
func NewSmartCollectionsHandler(r SmartCollectionsRefresher) HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		return r.RefreshAll(ctx)
	}
}

// NFOCollectionsSweeper imports movie.nfo groupings for every library that
// asks for it. Satisfied by *collections.NFOImporter.
type NFOCollectionsSweeper interface {
	SweepAll(ctx context.Context) (string, error)
}

// NewNFOCollectionsHandler returns the nfo_collections task: re-read the
// movie.nfo files of libraries that import NFO collections and add their
// movies to the <set> (and, if chosen, <tag>) collections they name. New
// movies are added as they're enriched; this catches NFO files edited since.
func NewNFOCollectionsHandler(s NFOCollectionsSweeper) HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		return s.SweepAll(ctx)
	}
}
