package scheduler

import (
	"context"
	"encoding/json"
)

// ExternalRatingsRefresher refreshes IMDb / Rotten Tomatoes / Metacritic
// scores. Satisfied by *externalratings.Service.
type ExternalRatingsRefresher interface {
	Refresh(ctx context.Context) (string, error)
}

// NewExternalRatingsHandler returns the external_ratings task: look up the
// scores movies and shows are missing, and refresh stale ones, on OMDb.
func NewExternalRatingsHandler(r ExternalRatingsRefresher) HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		return r.Refresh(ctx)
	}
}
