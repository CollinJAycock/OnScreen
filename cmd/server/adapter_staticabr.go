package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/staticabr"
)

// staticPopularity adapts the watch_plays-backed getTopPlayed query to
// staticabr.PopularitySource.
type staticPopularity struct{ q *gen.Queries }

func (p staticPopularity) TopPlayed(ctx context.Context) ([]staticabr.Popular, error) {
	// 90-day popularity window — matches the query's fixed window from before
	// the analytics range selector made it a parameter.
	rows, err := p.q.GetTopPlayed(ctx, 90)
	if err != nil {
		return nil, err
	}
	out := make([]staticabr.Popular, 0, len(rows))
	for _, r := range rows {
		out = append(out, staticabr.Popular{ItemID: r.ID, PlayCount: int(r.PlayCount)})
	}
	return out, nil
}

// staticResolver adapts the media service to staticabr.SourceResolver. Only
// single-file video items (movies, episodes) resolve; everything else (show
// parents, music, books) returns ok=false and is skipped. The output ladder is
// H.264 for the broadest CDN/device reach.
type staticResolver struct{ media *media.Service }

func (r staticResolver) Resolve(ctx context.Context, itemID uuid.UUID) (staticabr.Source, bool, error) {
	item, err := r.media.GetItem(ctx, itemID)
	if err != nil {
		return staticabr.Source{}, false, err
	}
	if item.Type != "movie" && item.Type != "episode" {
		return staticabr.Source{}, false, nil
	}
	files, err := r.media.GetFiles(ctx, itemID)
	if err != nil {
		return staticabr.Source{}, false, err
	}
	for _, f := range files {
		// Need a content hash (for invalidation) and source dimensions (to build
		// the ladder); skip files still missing technical metadata.
		if f.FileHash == nil || f.ResolutionW == nil || f.ResolutionH == nil {
			continue
		}
		bitrateKbps := 0
		if f.Bitrate != nil {
			bitrateKbps = int(*f.Bitrate / 1000) // bits/s → kbps
		}
		return staticabr.Source{
			FileID:      f.ID,
			FilePath:    f.FilePath,
			Hash:        *f.FileHash,
			Width:       *f.ResolutionW,
			Height:      *f.ResolutionH,
			BitrateKbps: bitrateKbps,
			Codec:       "h264",
		}, true, nil
	}
	return staticabr.Source{}, false, nil
}
