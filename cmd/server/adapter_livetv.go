package main

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

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

func (a *livetvAdapter) SetChannelSortOrder(ctx context.Context, id uuid.UUID, sortOrder int32) error {
	return a.q.SetChannelSortOrder(ctx, gen.SetChannelSortOrderParams{ID: id, SortOrder: sortOrder})
}

func (a *livetvAdapter) ListEPGProgramsInWindow(ctx context.Context, from, to time.Time) ([]livetv.EPGProgram, error) {
	rows, err := a.q.ListEPGProgramsInWindow(ctx, gen.ListEPGProgramsInWindowParams{
		// SQL: ends_at > $1 AND starts_at < $2 → first param is window
		// start (compared against ends_at), second is window end.
		EndsAt:   pgtype.Timestamptz{Time: from, Valid: true},
		StartsAt: pgtype.Timestamptz{Time: to, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	out := make([]livetv.EPGProgram, len(rows))
	for i, r := range rows {
		p := livetv.EPGProgram{
			ID:          r.ID,
			ChannelID:   r.ChannelID,
			Title:       r.Title,
			Subtitle:    r.Subtitle,
			Description: r.Description,
			Category:    r.Category,
			Rating:      r.Rating,
			SeasonNum:   r.SeasonNum,
			EpisodeNum:  r.EpisodeNum,
			StartsAt:    r.StartsAt.Time,
			EndsAt:      r.EndsAt.Time,
		}
		if r.OriginalAirDate.Valid {
			t := r.OriginalAirDate.Time
			p.OriginalAirDate = &t
		}
		out[i] = p
	}
	return out, nil
}

func (a *livetvAdapter) GetNowAndNextForChannels(ctx context.Context) ([]livetv.NowNextEntry, error) {
	rows, err := a.q.GetNowAndNextForChannels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]livetv.NowNextEntry, len(rows))
	for i, r := range rows {
		out[i] = livetv.NowNextEntry{
			ChannelID:  r.ChannelID,
			ProgramID:  r.ProgramID,
			Title:      r.Title,
			Subtitle:   r.Subtitle,
			StartsAt:   r.StartsAt.Time,
			EndsAt:     r.EndsAt.Time,
			SeasonNum:  r.SeasonNum,
			EpisodeNum: r.EpisodeNum,
		}
	}
	return out, nil
}

// ── EPG sources + ingestion ──────────────────────────────────────────────────

func (a *livetvAdapter) ListEPGSources(ctx context.Context) ([]livetv.EPGSource, error) {
	rows, err := a.q.ListEPGSources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]livetv.EPGSource, len(rows))
	for i, r := range rows {
		out[i] = epgSourceFromGen(r)
	}
	return out, nil
}

func (a *livetvAdapter) GetEPGSource(ctx context.Context, id uuid.UUID) (livetv.EPGSource, error) {
	row, err := a.q.GetEPGSource(ctx, id)
	if err != nil {
		return livetv.EPGSource{}, err
	}
	return epgSourceFromGen(row), nil
}

func (a *livetvAdapter) CreateEPGSource(ctx context.Context, p livetv.CreateEPGSourceParams) (livetv.EPGSource, error) {
	row, err := a.q.CreateEPGSource(ctx, gen.CreateEPGSourceParams{
		Type:               string(p.Type),
		Name:               p.Name,
		Config:             p.Config,
		RefreshIntervalMin: p.RefreshIntervalMin,
	})
	if err != nil {
		return livetv.EPGSource{}, err
	}
	return epgSourceFromGen(row), nil
}

func (a *livetvAdapter) DeleteEPGSource(ctx context.Context, id uuid.UUID) error {
	return a.q.DeleteEPGSource(ctx, id)
}

func (a *livetvAdapter) SetEPGSourceEnabled(ctx context.Context, id uuid.UUID, enabled bool) error {
	return a.q.SetEPGSourceEnabled(ctx, gen.SetEPGSourceEnabledParams{ID: id, Enabled: enabled})
}

func (a *livetvAdapter) RecordEPGPull(ctx context.Context, id uuid.UUID, lastError *string) error {
	return a.q.RecordEPGPull(ctx, gen.RecordEPGPullParams{ID: id, LastError: lastError})
}

func (a *livetvAdapter) ListUnmappedChannels(ctx context.Context) ([]livetv.Channel, error) {
	rows, err := a.q.ListUnmappedChannels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]livetv.Channel, len(rows))
	for i, r := range rows {
		out[i] = channelFromGen(r)
	}
	return out, nil
}

func (a *livetvAdapter) SetChannelEPGID(ctx context.Context, id uuid.UUID, epgChannelID *string) error {
	return a.q.SetChannelEPGID(ctx, gen.SetChannelEPGIDParams{ID: id, EpgChannelID: epgChannelID})
}

func (a *livetvAdapter) GetChannelByEPGID(ctx context.Context, epgChannelID string) (livetv.Channel, error) {
	row, err := a.q.GetChannelByEPGID(ctx, &epgChannelID)
	if err != nil {
		return livetv.Channel{}, err
	}
	return channelFromGen(row), nil
}

func (a *livetvAdapter) UpsertEPGProgram(ctx context.Context, p livetv.UpsertEPGProgramParams) error {
	// category is `TEXT[] NOT NULL DEFAULT '{}'` — a nil Go slice gets
	// sent as NULL by pgx, which violates the constraint even though the
	// column has a default (defaults only kick in when the column is
	// omitted from the INSERT, not when an explicit NULL is provided).
	// Coalesce nil → empty slice so XMLTV programs without any
	// <category> tags still ingest cleanly.
	cats := p.Category
	if cats == nil {
		cats = []string{}
	}
	args := gen.UpsertEPGProgramParams{
		ChannelID:       p.ChannelID,
		SourceProgramID: p.SourceProgramID,
		Title:           p.Title,
		Subtitle:        p.Subtitle,
		Description:     p.Description,
		Category:        cats,
		Rating:          p.Rating,
		SeasonNum:       p.SeasonNum,
		EpisodeNum:      p.EpisodeNum,
		StartsAt:        pgtype.Timestamptz{Time: p.StartsAt, Valid: true},
		EndsAt:          pgtype.Timestamptz{Time: p.EndsAt, Valid: true},
		RawData:         p.RawData,
	}
	if p.OriginalAirDate != nil {
		args.OriginalAirDate = pgtype.Date{Time: *p.OriginalAirDate, Valid: true}
	}
	return a.q.UpsertEPGProgram(ctx, args)
}

func (a *livetvAdapter) TrimOldEPGPrograms(ctx context.Context) error {
	return a.q.TrimOldEPGPrograms(ctx)
}

func (a *livetvAdapter) ListAllKnownEPGChannelIDs(ctx context.Context) ([]string, error) {
	return a.q.ListAllKnownEPGChannelIDs(ctx)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

func epgSourceFromGen(r gen.EpgSource) livetv.EPGSource {
	out := livetv.EPGSource{
		ID:                 r.ID,
		Type:               livetv.EPGSourceType(r.Type),
		Name:               r.Name,
		Config:             r.Config,
		RefreshIntervalMin: r.RefreshIntervalMin,
		Enabled:            r.Enabled,
		LastError:          r.LastError,
	}
	if r.LastPullAt.Valid {
		t := r.LastPullAt.Time
		out.LastPullAt = &t
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		out.UpdatedAt = r.UpdatedAt.Time
	}
	return out
}

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
		ID:           r.ID,
		TunerID:      r.TunerID,
		Number:       r.Number,
		Callsign:     r.Callsign,
		Name:         r.Name,
		LogoURL:      r.LogoUrl,
		Enabled:      r.Enabled,
		SortOrder:    r.SortOrder,
		EPGChannelID: r.EpgChannelID,
	}
	if r.CreatedAt.Valid {
		c.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		c.UpdatedAt = r.UpdatedAt.Time
	}
	return c
}
