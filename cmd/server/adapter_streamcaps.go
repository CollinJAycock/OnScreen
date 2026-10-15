package main

import (
	"context"

	"github.com/google/uuid"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// streamCapsAdapter reads a user's admin-set streaming caps, and their own
// quality profile, for the play decision and transcode start
// (v1.UserStreamCapsReader). NULL columns map to 0 ("no cap").
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
	if row.MaxRemoteStreamBitrateKbps != nil {
		caps.RemoteBitrateKbps = int(*row.MaxRemoteStreamBitrateKbps)
	}
	if row.MaxVideoBitrateKbps != nil {
		caps.Quality.MaxVideoBitrateKbps = int(*row.MaxVideoBitrateKbps)
	}
	if row.MaxAudioBitrateKbps != nil {
		caps.Quality.MaxAudioBitrateKbps = int(*row.MaxAudioBitrateKbps)
	}
	if row.MaxVideoHeight != nil {
		caps.Quality.MaxVideoHeight = int(*row.MaxVideoHeight)
	}
	if row.PreferredVideoCodec != nil {
		caps.Quality.PreferredVideoCodec = *row.PreferredVideoCodec
	}
	return caps, nil
}

// remoteStreamPolicyAdapter reads the internet streaming limit and the extra
// home networks from Settings ▸ Transcode (v1.RemoteStreamPolicyReader). Read
// live per play decision / transcode start / Now Playing listing — one
// server_settings lookup — so an admin change applies to the next stream with
// no restart. Entries that no longer parse are skipped (the settings API
// refuses them on save, so that only happens to a hand-edited row).
type remoteStreamPolicyAdapter struct{ svc *settings.Service }

func (a remoteStreamPolicyAdapter) RemoteStreamPolicy(ctx context.Context) v1.RemoteStreamPolicy {
	tc := a.svc.TranscodeConfigGet(ctx)
	p := v1.RemoteStreamPolicy{MaxBitrateKbps: max(tc.RemoteMaxBitrateKbps, 0)}
	for _, raw := range tc.LocalNetworks {
		if nets, err := v1.ParseLocalNetworks([]string{raw}); err == nil {
			p.LocalNetworks = append(p.LocalNetworks, nets...)
		}
	}
	return p
}
