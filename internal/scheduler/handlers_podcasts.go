package scheduler

import (
	"context"
	"encoding/json"
)

// PodcastRefresher refreshes every podcast feed subscription. Satisfied by
// *podcast.Service.
type PodcastRefresher interface {
	RefreshAll(ctx context.Context) (string, error)
}

// NewPodcastRefreshHandler returns the podcast_refresh task: re-read every
// subscribed feed, list new episodes and download them where auto-download
// is on, and delete downloads past each feed's retention.
func NewPodcastRefreshHandler(r PodcastRefresher) HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		return r.RefreshAll(ctx)
	}
}
