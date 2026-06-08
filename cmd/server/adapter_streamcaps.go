package main

import (
	"context"

	"github.com/google/uuid"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// streamCapsAdapter reads a user's admin-set streaming caps for transcode-start
// enforcement (v1.UserStreamCapsReader). NULL columns map to 0 ("no cap").
type streamCapsAdapter struct{ q *gen.Queries }

func (a streamCapsAdapter) GetStreamCaps(ctx context.Context, userID uuid.UUID) (v1.StreamCaps, error) {
	row, err := a.q.GetUserStreamCaps(ctx, userID)
	if err != nil {
		return v1.StreamCaps{}, err
	}
	var caps v1.StreamCaps
	if row.MaxConcurrentStreams != nil {
		caps.ConcurrentStreams = int(*row.MaxConcurrentStreams)
	}
	if row.MaxStreamBitrateKbps != nil {
		caps.BitrateKbps = int(*row.MaxStreamBitrateKbps)
	}
	return caps, nil
}
