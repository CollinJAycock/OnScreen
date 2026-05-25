package scheduler

import (
	"context"
	"encoding/json"
)

// staticABRRunner runs one static-ABR pre-encode pass. *staticabr.Service
// satisfies it; an interface keeps the scheduler package free of the staticabr
// import.
type staticABRRunner interface {
	RunOnce(ctx context.Context) (string, error)
}

// NewStaticABRPreencodeHandler returns a scheduler handler that runs one
// static-ABR pre-encode pass — pick popular titles and pre-encode any missing or
// stale ABR ladders to the media store, so their segments serve from object
// storage / CDN instead of the live-transcode fleet (HA roadmap §5). Runs off the
// request path; wired only when STATIC_ABR_ENABLED is set, since a pass spawns
// ffmpeg encodes.
func NewStaticABRPreencodeHandler(runner staticABRRunner) HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		return runner.RunOnce(ctx)
	}
}
