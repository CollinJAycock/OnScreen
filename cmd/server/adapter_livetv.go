package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/livetv"
)

// livetvAdapter implements livetv.Querier on top of the generated sqlc
// methods. The adapter is the only place that knows about the gen.* types;
// the livetv package itself only sees the domain types.
type livetvAdapter struct{ q *gen.Queries }

func newLiveTVAdapter(q *gen.Queries) *livetvAdapter { return &livetvAdapter{q: q} }

// ── Tuners ────────────────────────────────────────────────────────────────────

func (a *livetvAdapter) CreateTunerDevice(ctx context.Context, p livetv.CreateTunerDeviceParams) (livetv.TunerDevice, error) {
	row, err := a.q.CreateTunerDevice(ctx, gen.CreateTunerDeviceParams{
		Type:      string(p.Type),
		Name:      p.Name,
		Config:    p.Config,
		TuneCount: int32(p.TuneCount),
	})
	if err != nil {
		return livetv.TunerDevice{}, err
	}
	return tunerFromGen(row), nil
}

func (a *livetvAdapter) GetTunerDevice(ctx context.Context, id uuid.UUID) (livetv.TunerDevice, error) {
	row, err := a.q.GetTunerDevice(ctx, id)
	if err != nil {
		return livetv.TunerDevice{}, err
	}
	return tunerFromGen(row), nil
}

func (a *livetvAdapter) ListTunerDevices(ctx context.Context) ([]livetv.TunerDevice, error) {
	rows, err := a.q.ListTunerDevices(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]livetv.TunerDevice, len(rows))
	for i, r := range rows {
		out[i] = tunerFromGen(r)
	}
	return out, nil
}

func (a *livetvAdapter) UpdateTunerDevice(ctx context.Context, p livetv.UpdateTunerDeviceParams) (livetv.TunerDevice, error) {
	row, err := a.q.UpdateTunerDevice(ctx, gen.UpdateTunerDeviceParams{
		ID:        p.ID,
		Name:      p.Name,
		Config:    p.Config,
		TuneCount: int32(p.TuneCount),
	})
	if err != nil {
		return livetv.TunerDevice{}, err
	}
	return tunerFromGen(row), nil
}

func (a *livetvAdapter) SetTunerEnabled(ctx context.Context, id uuid.UUID, enabled bool) error {
	return a.q.SetTunerEnabled(ctx, gen.SetTunerEnabledParams{ID: id, Enabled: enabled})
}

func (a *livetvAdapter) TouchTunerLastSeen(ctx context.Context, id uuid.UUID) error {
	return a.q.TouchTunerLastSeen(ctx, id)
}

func (a *livetvAdapter) DeleteTunerDevice(ctx context.Context, id uuid.UUID) error {
	return a.q.DeleteTunerDevice(ctx, id)
}

// ── Channels ─────────────────────────────────────────────────────────────────

func (a *livetvAdapter) UpsertChannel(ctx context.Context, p livetv.UpsertChannelParams) (livetv.Channel, error) {
	row, err := a.q.UpsertChannel(ctx, gen.UpsertChannelParams{
		TunerID:  p.TunerID,
		Number:   p.Number,
		Callsign: p.Callsign,
		Name:     p.Name,
		LogoUrl:  p.LogoURL,
	})
	if err != nil {
		return livetv.Channel{}, err
	}
	return channelFromGen(row), nil
}

func (a *livetvAdapter) GetChannel(ctx context.Context, id uuid.UUID) (livetv.Channel, error) {
	row, err := a.q.GetChannel(ctx, id)
	if err != nil {
		return livetv.Channel{}, err
	}
	return channelFromGen(row), nil
}

func (a *livetvAdapter) ListChannels(ctx context.Context, enabled *bool) ([]livetv.ChannelWithTuner, error) {
	rows, err := a.q.ListChannels(ctx, enabled)
	if err != nil {
		return nil, err
	}
	out := make([]livetv.ChannelWithTuner, len(rows))
	for i, r := range rows {
		out[i] = livetv.ChannelWithTuner{
			Channel: livetv.Channel{
				ID: r.ID, TunerID: r.TunerID, Number: r.Number,
				Callsign: r.Callsign, Name: r.Name, LogoURL: r.LogoUrl,
				Enabled: r.Enabled, SortOrder: r.SortOrder,
			},
			TunerName: r.TunerName,
			TunerType: livetv.TunerType(r.TunerType),
		}
		if r.CreatedAt.Valid {
			out[i].Channel.CreatedAt = r.CreatedAt.Time
		}
		if r.UpdatedAt.Valid {
			out[i].Channel.UpdatedAt = r.UpdatedAt.Time
		}
	}
	return out, nil
}

func (a *livetvAdapter) ListChannelsByTuner(ctx context.Context, tunerID uuid.UUID) ([]livetv.Channel, error) {
	rows, err := a.q.ListChannelsByTuner(ctx, tunerID)
	if err != nil {
		return nil, err
	}
	out := make([]livetv.Channel, len(rows))
	for i, r := range rows {
		out[i] = channelFromGen(r)
	}
	return out, nil
}

func (a *livetvAdapter) SetChannelEnabled(ctx context.Context, id uuid.UUID, enabled bool) error {
	return a.q.SetChannelEnabled(ctx, gen.SetChannelEnabledParams{ID: id, Enabled: enabled})
}

func (a *livetvAdapter) GetNowAndNextForChannels(ctx context.Context) ([]livetv.NowNextEntry, error) {
	rows, err := a.q.GetNowAndNextForChannels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]livetv.NowNextEntry, len(rows))
	for i, r := range rows {
		entry := livetv.NowNextEntry{
			ChannelID:   r.ChannelID,
			Number:      r.Number,
			ChannelName: r.ChannelName,
			LogoURL:     r.LogoUrl,
			Subtitle:    r.Subtitle,
			SeasonNum:   r.SeasonNum,
			EpisodeNum:  r.EpisodeNum,
		}
		// LEFT JOIN LATERAL may produce a NULL program row, but sqlc can't
		// see that and emits the columns as non-nullable. uuid.Nil + invalid
		// Timestamptz is the actual signal we get from pgx for "no row."
		if r.ProgramID != uuid.Nil {
			pid := r.ProgramID
			entry.ProgramID = &pid
			title := r.Title
			entry.Title = &title
		}
		if r.StartsAt.Valid {
			t := r.StartsAt.Time
			entry.StartsAt = &t
		}
		if r.EndsAt.Valid {
			t := r.EndsAt.Time
			entry.EndsAt = &t
		}
		out[i] = entry
	}
	return out, nil
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func tunerFromGen(r gen.TunerDevice) livetv.TunerDevice {
	t := livetv.TunerDevice{
		ID:        r.ID,
		Type:      livetv.TunerType(r.Type),
		Name:      r.Name,
		Config:    r.Config,
		TuneCount: int(r.TuneCount),
		Enabled:   r.Enabled,
	}
	if r.LastSeenAt.Valid {
		ts := r.LastSeenAt.Time
		t.LastSeenAt = &ts
	}
	if r.CreatedAt.Valid {
		t.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		t.UpdatedAt = r.UpdatedAt.Time
	}
	return t
}

func channelFromGen(r gen.Channel) livetv.Channel {
	c := livetv.Channel{
		ID:        r.ID,
		TunerID:   r.TunerID,
		Number:    r.Number,
		Callsign:  r.Callsign,
		Name:      r.Name,
		LogoURL:   r.LogoUrl,
		Enabled:   r.Enabled,
		SortOrder: r.SortOrder,
	}
	if r.CreatedAt.Valid {
		c.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		c.UpdatedAt = r.UpdatedAt.Time
	}
	return c
}
