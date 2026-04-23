package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned by Service lookup methods when no matching row
// exists. Handlers map this to HTTP 404.
var ErrNotFound = errors.New("livetv: not found")

// TunerDevice mirrors a `tuner_devices` row at the domain level. Drivers
// are looked up by ID through the DriverManager — the row itself just
// carries the persisted config + descriptive fields.
type TunerDevice struct {
	ID          uuid.UUID
	Type        TunerType
	Name        string
	Config      json.RawMessage
	TuneCount   int
	Enabled     bool
	LastSeenAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Channel mirrors a `channels` row. Joined-in tuner metadata appears on
// ChannelWithTuner since the channel table doesn't carry it.
type Channel struct {
	ID           uuid.UUID
	TunerID      uuid.UUID
	Number       string
	Callsign     *string
	Name         string
	LogoURL      *string
	Enabled      bool
	SortOrder    int32
	EPGChannelID *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// EPGSource mirrors a `epg_sources` row. Config is opaque JSON whose
// shape depends on Type: XMLTV uses {"url": "..."}; Schedules Direct
// uses {"username":"...","password_hash":"...","lineup":"..."}.
type EPGSource struct {
	ID                  uuid.UUID
	Type                EPGSourceType
	Name                string
	Config              json.RawMessage
	RefreshIntervalMin  int32
	Enabled             bool
	LastPullAt          *time.Time
	LastError           *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// EPGSourceType identifies an EPG backend.
type EPGSourceType string

const (
	EPGSourceTypeXMLTVURL  EPGSourceType = "xmltv_url"
	EPGSourceTypeXMLTVFile EPGSourceType = "xmltv_file"
)

// XMLTVSourceConfig is the per-source config blob for XMLTV sources.
// Source is a URL ("https://provider/grid.xml") or local file path.
type XMLTVSourceConfig struct {
	Source string `json:"source"`
}

// CreateEPGSourceParams is the input to Querier.CreateEPGSource.
type CreateEPGSourceParams struct {
	Type               EPGSourceType
	Name               string
	Config             json.RawMessage
	RefreshIntervalMin int32
}

// UpsertEPGProgramParams is the input to Querier.UpsertEPGProgram. Times
// are normalized to UTC by the ingester before reaching this struct.
type UpsertEPGProgramParams struct {
	ChannelID       uuid.UUID
	SourceProgramID string
	Title           string
	Subtitle        *string
	Description     *string
	Category        []string
	Rating          *string
	SeasonNum       *int32
	EpisodeNum      *int32
	OriginalAirDate *time.Time
	StartsAt        time.Time
	EndsAt          time.Time
	RawData         []byte
}

// RefreshResult is the outcome of one EPG source refresh — surfaced in
// the API response so the settings UI can show "ingested 8,432 programs,
// auto-mapped 7 channels."
type RefreshResult struct {
	ProgramsIngested    int
	ChannelsAutoMatched int
	UnmappedChannels    int
	Skipped             int // programs with bad timestamps
}

// ChannelWithTuner is what the channels-list endpoint returns — the
// channel row plus the parent tuner's name/type so the UI can group or
// label streams by source.
type ChannelWithTuner struct {
	Channel
	TunerName string
	TunerType TunerType
}

// EPGProgram is one row in the guide-grid response. Matches the columns
// surfaced by ListEPGProgramsInWindow — enough to render a clickable
// time-slot tile (title, subtitle, episode tag, time) without a follow-up
// per-program fetch.
type EPGProgram struct {
	ID              uuid.UUID
	ChannelID       uuid.UUID
	Title           string
	Subtitle        *string
	Description     *string
	Category        []string
	Rating          *string
	SeasonNum       *int32
	EpisodeNum      *int32
	OriginalAirDate *time.Time
	StartsAt        time.Time
	EndsAt          time.Time
}

// NowNextEntry is one upcoming program in the channels-page now/next
// display — at most two rows per channel (current + next). Channels
// with no EPG data don't appear here; the client merges by channel_id
// against the channels list and renders "no guide data" for missing IDs.
type NowNextEntry struct {
	ChannelID  uuid.UUID
	ProgramID  uuid.UUID
	Title      string
	Subtitle   *string
	StartsAt   time.Time
	EndsAt     time.Time
	SeasonNum  *int32
	EpisodeNum *int32
}

// Querier is the slice of generated sqlc methods the service uses. Kept
// narrow so a mock can stand in for it in tests without dragging the rest
// of the gen package along.
type Querier interface {
	CreateTunerDevice(ctx context.Context, p CreateTunerDeviceParams) (TunerDevice, error)
	GetTunerDevice(ctx context.Context, id uuid.UUID) (TunerDevice, error)
	ListTunerDevices(ctx context.Context) ([]TunerDevice, error)
	UpdateTunerDevice(ctx context.Context, p UpdateTunerDeviceParams) (TunerDevice, error)
	SetTunerEnabled(ctx context.Context, id uuid.UUID, enabled bool) error
	TouchTunerLastSeen(ctx context.Context, id uuid.UUID) error
	DeleteTunerDevice(ctx context.Context, id uuid.UUID) error

	UpsertChannel(ctx context.Context, p UpsertChannelParams) (Channel, error)
	GetChannel(ctx context.Context, id uuid.UUID) (Channel, error)
	ListChannels(ctx context.Context, enabled *bool) ([]ChannelWithTuner, error)
	ListChannelsByTuner(ctx context.Context, tunerID uuid.UUID) ([]Channel, error)
	SetChannelEnabled(ctx context.Context, id uuid.UUID, enabled bool) error
	GetNowAndNextForChannels(ctx context.Context) ([]NowNextEntry, error)
	ListEPGProgramsInWindow(ctx context.Context, from, to time.Time) ([]EPGProgram, error)

	// EPG sources + ingestion.
	ListEPGSources(ctx context.Context) ([]EPGSource, error)
	GetEPGSource(ctx context.Context, id uuid.UUID) (EPGSource, error)
	CreateEPGSource(ctx context.Context, p CreateEPGSourceParams) (EPGSource, error)
	DeleteEPGSource(ctx context.Context, id uuid.UUID) error
	SetEPGSourceEnabled(ctx context.Context, id uuid.UUID, enabled bool) error
	RecordEPGPull(ctx context.Context, id uuid.UUID, lastError *string) error
	ListUnmappedChannels(ctx context.Context) ([]Channel, error)
	SetChannelEPGID(ctx context.Context, id uuid.UUID, epgChannelID *string) error
	GetChannelByEPGID(ctx context.Context, epgChannelID string) (Channel, error)
	UpsertEPGProgram(ctx context.Context, p UpsertEPGProgramParams) error
	TrimOldEPGPrograms(ctx context.Context) error
}

// CreateTunerDeviceParams is the input to Querier.CreateTunerDevice.
type CreateTunerDeviceParams struct {
	Type      TunerType
	Name      string
	Config    json.RawMessage
	TuneCount int
}

// UpdateTunerDeviceParams is the input to Querier.UpdateTunerDevice.
type UpdateTunerDeviceParams struct {
	ID        uuid.UUID
	Name      string
	Config    json.RawMessage
	TuneCount int
}

// UpsertChannelParams is the input to Querier.UpsertChannel.
type UpsertChannelParams struct {
	TunerID  uuid.UUID
	Number   string
	Callsign *string
	Name     string
	LogoURL  *string
}

// Service ties together the Querier (persistence), the Registry (per-row
// Driver factories), and an in-memory cache of live Drivers. A Driver
// instance is constructed once per tuner_devices row and reused so that
// tuner state (e.g. cached tune count from /discover.json) doesn't get
// thrown away on every API call.
type Service struct {
	q        Querier
	registry *Registry
	logger   *slog.Logger

	mu      sync.RWMutex
	drivers map[uuid.UUID]Driver
}

// NewService wires the dependencies. Drivers are built lazily on first
// access so a flaky tuner doesn't block startup.
func NewService(q Querier, registry *Registry, logger *slog.Logger) *Service {
	return &Service{
		q:        q,
		registry: registry,
		logger:   logger,
		drivers:  make(map[uuid.UUID]Driver),
	}
}

// ── Tuner CRUD ────────────────────────────────────────────────────────────────

// CreateTuner persists a tuner row, builds the Driver, and runs an initial
// Discover so the channels table is populated before the user even hits
// the channels page. Discover failure doesn't roll back the row — the
// device might just be temporarily down; the settings UI surfaces the
// error and the user can retry.
func (s *Service) CreateTuner(ctx context.Context, p CreateTunerDeviceParams) (TunerDevice, error) {
	row, err := s.q.CreateTunerDevice(ctx, p)
	if err != nil {
		return TunerDevice{}, fmt.Errorf("create tuner: %w", err)
	}
	if _, err := s.discoverAndPersist(ctx, row); err != nil {
		s.logger.WarnContext(ctx, "initial tuner discover failed",
			"tuner_id", row.ID, "err", err)
	}
	return row, nil
}

// GetTuner returns a row by ID.
func (s *Service) GetTuner(ctx context.Context, id uuid.UUID) (TunerDevice, error) {
	row, err := s.q.GetTunerDevice(ctx, id)
	if err != nil {
		return TunerDevice{}, fmt.Errorf("get tuner: %w", err)
	}
	return row, nil
}

// ListTuners returns all tuner rows for the settings UI.
func (s *Service) ListTuners(ctx context.Context) ([]TunerDevice, error) {
	rows, err := s.q.ListTunerDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tuners: %w", err)
	}
	return rows, nil
}

// UpdateTuner persists changes and invalidates the cached Driver so the
// next access rebuilds with the new config. Discover is not auto-rerun on
// update — operator can click "Rescan channels" in the settings UI.
func (s *Service) UpdateTuner(ctx context.Context, p UpdateTunerDeviceParams) (TunerDevice, error) {
	row, err := s.q.UpdateTunerDevice(ctx, p)
	if err != nil {
		return TunerDevice{}, fmt.Errorf("update tuner: %w", err)
	}
	s.invalidateDriver(p.ID)
	return row, nil
}

// SetTunerEnabled toggles the device on/off. Disabled tuners stay in the
// table but are excluded from channel listings and HLS proxy lookups.
func (s *Service) SetTunerEnabled(ctx context.Context, id uuid.UUID, enabled bool) error {
	if err := s.q.SetTunerEnabled(ctx, id, enabled); err != nil {
		return fmt.Errorf("set tuner enabled: %w", err)
	}
	if !enabled {
		s.invalidateDriver(id)
	}
	return nil
}

// DeleteTuner cascades through channels and EPG programs at the DB level.
// In-memory driver cache is invalidated on the way out.
func (s *Service) DeleteTuner(ctx context.Context, id uuid.UUID) error {
	if err := s.q.DeleteTunerDevice(ctx, id); err != nil {
		return fmt.Errorf("delete tuner: %w", err)
	}
	s.invalidateDriver(id)
	return nil
}

// RescanTuner re-runs Discover for an existing tuner and upserts the
// channel list. Returns the count of channels currently visible after the
// scan. Used by the settings UI's "Rescan channels" button.
func (s *Service) RescanTuner(ctx context.Context, id uuid.UUID) (int, error) {
	row, err := s.q.GetTunerDevice(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("get tuner: %w", err)
	}
	return s.discoverAndPersist(ctx, row)
}

// ── Channels ─────────────────────────────────────────────────────────────────

// ListChannels returns channels across all enabled tuners, optionally
// filtered to enabled-only. Each row carries the parent tuner's name +
// type so the UI can render badges without a second query.
func (s *Service) ListChannels(ctx context.Context, enabledOnly bool) ([]ChannelWithTuner, error) {
	var enabledFilter *bool
	if enabledOnly {
		t := true
		enabledFilter = &t
	}
	rows, err := s.q.ListChannels(ctx, enabledFilter)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	return rows, nil
}

// GetChannel returns one channel row.
func (s *Service) GetChannel(ctx context.Context, id uuid.UUID) (Channel, error) {
	c, err := s.q.GetChannel(ctx, id)
	if err != nil {
		return Channel{}, fmt.Errorf("get channel: %w", err)
	}
	return c, nil
}

// SetChannelEnabled toggles a channel. Disabled channels stay in the
// table — operators sometimes want to hide IPTV junk from the guide
// without losing the discovered metadata.
func (s *Service) SetChannelEnabled(ctx context.Context, id uuid.UUID, enabled bool) error {
	if err := s.q.SetChannelEnabled(ctx, id, enabled); err != nil {
		return fmt.Errorf("set channel enabled: %w", err)
	}
	return nil
}

// NowAndNext returns current + next program per enabled channel, suitable
// for direct rendering in the channels page. Programs may be nil when EPG
// data hasn't been ingested yet.
func (s *Service) NowAndNext(ctx context.Context) ([]NowNextEntry, error) {
	rows, err := s.q.GetNowAndNextForChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("now and next: %w", err)
	}
	return rows, nil
}

// Guide returns every program across visible channels overlapping the
// window [from, to]. Caller is responsible for sensible window sizing
// (the UI currently uses 4-hour windows snapped to the half-hour). An
// empty result just means no EPG data has been ingested for that range
// — handlers should render an empty grid rather than 404.
func (s *Service) Guide(ctx context.Context, from, to time.Time) ([]EPGProgram, error) {
	if !to.After(from) {
		return nil, fmt.Errorf("guide: window must have to > from")
	}
	rows, err := s.q.ListEPGProgramsInWindow(ctx, from, to)
	if err != nil {
		return nil, fmt.Errorf("guide window: %w", err)
	}
	return rows, nil
}

// ── Streaming ────────────────────────────────────────────────────────────────

// OpenChannelStream resolves the channel's tuner, builds (or reuses) the
// Driver, and opens an upstream MPEG-TS stream. Caller is responsible for
// closing the returned Stream — that's what releases the tune slot.
//
// Returns ErrNotFound if the channel doesn't exist or its tuner is
// disabled. ErrAllTunersBusy bubbles up unchanged from the Driver.
func (s *Service) OpenChannelStream(ctx context.Context, channelID uuid.UUID) (Stream, error) {
	ch, err := s.q.GetChannel(ctx, channelID)
	if err != nil {
		s.logger.WarnContext(ctx, "open channel stream: channel lookup failed",
			"channel_id", channelID, "err", err)
		return nil, ErrNotFound
	}
	tuner, err := s.q.GetTunerDevice(ctx, ch.TunerID)
	if err != nil {
		s.logger.WarnContext(ctx, "open channel stream: tuner lookup failed",
			"channel_id", channelID, "tuner_id", ch.TunerID, "err", err)
		return nil, ErrNotFound
	}
	if !tuner.Enabled {
		s.logger.WarnContext(ctx, "open channel stream: tuner disabled",
			"channel_id", channelID, "tuner_id", tuner.ID)
		return nil, ErrNotFound
	}
	driver, err := s.driverFor(tuner)
	if err != nil {
		s.logger.ErrorContext(ctx, "open channel stream: driver build failed",
			"tuner_id", tuner.ID, "tuner_type", tuner.Type, "err", err)
		return nil, fmt.Errorf("driver for tuner %s: %w", tuner.ID, err)
	}
	stream, err := driver.OpenStream(ctx, ch.Number)
	if err != nil {
		s.logger.WarnContext(ctx, "open channel stream: driver OpenStream failed",
			"channel_id", channelID, "channel_number", ch.Number,
			"tuner_id", tuner.ID, "err", err)
	}
	return stream, err
}

// ── EPG sources ──────────────────────────────────────────────────────────────

// ListEPGSources returns all configured EPG sources.
func (s *Service) ListEPGSources(ctx context.Context) ([]EPGSource, error) {
	rows, err := s.q.ListEPGSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list epg sources: %w", err)
	}
	return rows, nil
}

// CreateEPGSource persists a new source. Refresh is not auto-triggered —
// the caller (settings UI) typically follows up with a Refresh call so
// the user gets immediate feedback that auth + URL are valid.
func (s *Service) CreateEPGSource(ctx context.Context, p CreateEPGSourceParams) (EPGSource, error) {
	if p.RefreshIntervalMin <= 0 {
		p.RefreshIntervalMin = 360 // 6h default; matches schema default
	}
	row, err := s.q.CreateEPGSource(ctx, p)
	if err != nil {
		return EPGSource{}, fmt.Errorf("create epg source: %w", err)
	}
	return row, nil
}

// DeleteEPGSource removes a source. Does not delete already-ingested
// programs — they expire naturally via TrimOldEPGPrograms.
func (s *Service) DeleteEPGSource(ctx context.Context, id uuid.UUID) error {
	if err := s.q.DeleteEPGSource(ctx, id); err != nil {
		return fmt.Errorf("delete epg source: %w", err)
	}
	return nil
}

// SetEPGSourceEnabled toggles a source. Disabled sources are skipped
// by the background refresh loop (when implemented in Phase B Round 2).
func (s *Service) SetEPGSourceEnabled(ctx context.Context, id uuid.UUID, enabled bool) error {
	if err := s.q.SetEPGSourceEnabled(ctx, id, enabled); err != nil {
		return fmt.Errorf("set epg source enabled: %w", err)
	}
	return nil
}

// RefreshEPGSource pulls the source's grid, parses it, auto-matches
// any unmapped channels, and upserts programs.
//
// Auto-match strategy: for each unmapped enabled channel, try matching
// the source's <channel> entries against (in order):
//  1. lcn → channel.number (most reliable, e.g. "5.1" matches HDHomeRun
//     guide numbers exactly)
//  2. display-name → channel.callsign (case-insensitive substring)
//  3. display-name → channel.name (case-insensitive substring)
//
// Programs whose `channel` attribute doesn't resolve to a known channel
// are silently dropped — there's no point recording EPG for channels
// the user hasn't tuned.
//
// last_pull_at + last_error are recorded on every call regardless of
// outcome so the settings UI can show "last pulled 12s ago, 8 errors".
func (s *Service) RefreshEPGSource(ctx context.Context, id uuid.UUID) (RefreshResult, error) {
	src, err := s.q.GetEPGSource(ctx, id)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("get epg source: %w", err)
	}

	result, err := s.refreshXMLTV(ctx, src)
	// Always record the pull, success or failure, so the UI can surface
	// the error inline. Wrap the original error for the caller.
	var lastErr *string
	if err != nil {
		msg := err.Error()
		lastErr = &msg
	}
	if recErr := s.q.RecordEPGPull(ctx, src.ID, lastErr); recErr != nil {
		s.logger.WarnContext(ctx, "record epg pull",
			"source_id", src.ID, "err", recErr)
	}
	if err != nil {
		return result, err
	}
	// Best-effort cleanup of expired programs after a successful pull —
	// keeps the table from growing unboundedly across many sources.
	if trimErr := s.q.TrimOldEPGPrograms(ctx); trimErr != nil {
		s.logger.WarnContext(ctx, "trim old epg programs", "err", trimErr)
	}
	return result, nil
}

// refreshXMLTV is the XMLTV-specific path. Schedules Direct will get its
// own refresh* method when added — RefreshEPGSource picks based on type.
func (s *Service) refreshXMLTV(ctx context.Context, src EPGSource) (RefreshResult, error) {
	if src.Type != EPGSourceTypeXMLTVURL && src.Type != EPGSourceTypeXMLTVFile {
		return RefreshResult{}, fmt.Errorf("xmltv refresh: unsupported source type %q", src.Type)
	}
	var cfg XMLTVSourceConfig
	if err := json.Unmarshal(src.Config, &cfg); err != nil {
		return RefreshResult{}, fmt.Errorf("xmltv config parse: %w", err)
	}
	if cfg.Source == "" {
		return RefreshResult{}, fmt.Errorf("xmltv source: empty source URL/path")
	}

	body, err := FetchXMLTV(ctx, cfg.Source)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("fetch: %w", err)
	}
	defer body.Close()

	xmltvChannels, programs, skipped, err := ParseXMLTV(body)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("parse: %w", err)
	}

	matched, err := s.autoMatchChannels(ctx, xmltvChannels)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("auto-match: %w", err)
	}

	// Resolve EPG channel IDs to OnScreen channel UUIDs in a single pass —
	// then upsert. A program whose channel has no mapping (still NULL after
	// auto-match) is silently dropped.
	idCache := make(map[string]uuid.UUID, len(programs))
	ingested := 0
	for _, p := range programs {
		uid, ok := idCache[p.ChannelID]
		if !ok {
			ch, err := s.q.GetChannelByEPGID(ctx, p.ChannelID)
			if err != nil {
				idCache[p.ChannelID] = uuid.Nil
				continue
			}
			uid = ch.ID
			idCache[p.ChannelID] = uid
		}
		if uid == uuid.Nil {
			continue
		}
		var subPtr, descPtr, ratingPtr *string
		if p.Subtitle != "" {
			s := p.Subtitle
			subPtr = &s
		}
		if p.Description != "" {
			s := p.Description
			descPtr = &s
		}
		if p.Rating != "" {
			s := p.Rating
			ratingPtr = &s
		}
		if err := s.q.UpsertEPGProgram(ctx, UpsertEPGProgramParams{
			ChannelID:       uid,
			SourceProgramID: p.SourceProgramID(),
			Title:           p.Title,
			Subtitle:        subPtr,
			Description:     descPtr,
			Category:        p.Category,
			Rating:          ratingPtr,
			SeasonNum:       p.SeasonNum,
			EpisodeNum:      p.EpisodeNum,
			OriginalAirDate: p.OriginalAirDate,
			StartsAt:        p.StartsAt,
			EndsAt:          p.EndsAt,
		}); err != nil {
			return RefreshResult{}, fmt.Errorf("upsert program %s: %w", p.SourceProgramID(), err)
		}
		ingested++
	}

	// Count channels that remain unmapped after auto-match — surfaced in
	// the UI so users know how many they need to map manually.
	unmapped, err := s.q.ListUnmappedChannels(ctx)
	unmappedCount := 0
	if err == nil {
		unmappedCount = len(unmapped)
	}

	return RefreshResult{
		ProgramsIngested:    ingested,
		ChannelsAutoMatched: matched,
		UnmappedChannels:    unmappedCount,
		Skipped:             skipped,
	}, nil
}

// autoMatchChannels tries to assign an epg_channel_id to every enabled
// channel that lacks one. Returns the number of newly-mapped channels.
//
// Match priority (first hit wins per channel):
//   - lcn exact → channel.number
//   - display-name case-insensitive → channel.callsign
//   - display-name case-insensitive → channel.name
//
// We don't unset existing mappings even if they don't appear in this
// source's channel list — operator-set or previously-matched mappings
// are sticky; they get cleared only via SetChannelEPGID(..., nil).
func (s *Service) autoMatchChannels(ctx context.Context, xmltvChans []XMLTVChannel) (int, error) {
	unmapped, err := s.q.ListUnmappedChannels(ctx)
	if err != nil {
		return 0, err
	}
	if len(unmapped) == 0 {
		return 0, nil
	}

	matched := 0
	for _, ch := range unmapped {
		var found string
		for _, x := range xmltvChans {
			if x.LCN != "" && x.LCN == ch.Number {
				found = x.ID
				break
			}
		}
		if found == "" && ch.Callsign != nil {
			lcCallsign := strings.ToLower(*ch.Callsign)
			for _, x := range xmltvChans {
				for _, name := range x.DisplayNames {
					if strings.Contains(strings.ToLower(name), lcCallsign) {
						found = x.ID
						break
					}
				}
				if found != "" {
					break
				}
			}
		}
		if found == "" {
			lcName := strings.ToLower(ch.Name)
			for _, x := range xmltvChans {
				for _, name := range x.DisplayNames {
					if strings.Contains(strings.ToLower(name), lcName) {
						found = x.ID
						break
					}
				}
				if found != "" {
					break
				}
			}
		}
		if found == "" {
			continue
		}
		if err := s.q.SetChannelEPGID(ctx, ch.ID, &found); err != nil {
			s.logger.WarnContext(ctx, "set channel epg id",
				"channel_id", ch.ID, "epg_id", found, "err", err)
			continue
		}
		matched++
		s.logger.InfoContext(ctx, "auto-matched channel",
			"channel_id", ch.ID, "channel_number", ch.Number,
			"channel_name", ch.Name, "epg_id", found)
	}
	return matched, nil
}

// SetChannelEPGID is the manual-override path for the settings UI. nil
// clears the mapping so the next ingest re-runs auto-match.
func (s *Service) SetChannelEPGID(ctx context.Context, id uuid.UUID, epgChannelID *string) error {
	if err := s.q.SetChannelEPGID(ctx, id, epgChannelID); err != nil {
		return fmt.Errorf("set channel epg id: %w", err)
	}
	return nil
}

// ── Internals ────────────────────────────────────────────────────────────────

// driverFor returns the cached Driver for a tuner row, building it (and
// caching) on first access. Concurrent first-access for the same tuner is
// safe — extra builds are wasted but harmless.
func (s *Service) driverFor(tuner TunerDevice) (Driver, error) {
	s.mu.RLock()
	d, ok := s.drivers[tuner.ID]
	s.mu.RUnlock()
	if ok {
		return d, nil
	}
	built, err := s.registry.Build(tuner.Type, tuner.Name, tuner.Config)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	// Re-check after acquiring write lock to avoid replacing a Driver
	// another goroutine just installed.
	if existing, ok := s.drivers[tuner.ID]; ok {
		s.mu.Unlock()
		return existing, nil
	}
	s.drivers[tuner.ID] = built
	s.mu.Unlock()
	return built, nil
}

func (s *Service) invalidateDriver(id uuid.UUID) {
	s.mu.Lock()
	delete(s.drivers, id)
	s.mu.Unlock()
}

// discoverAndPersist runs Discover on the tuner's Driver and upserts the
// returned channels, refreshing tune_count and last_seen_at on success.
// Returns the number of channels persisted.
func (s *Service) discoverAndPersist(ctx context.Context, tuner TunerDevice) (int, error) {
	driver, err := s.driverFor(tuner)
	if err != nil {
		return 0, err
	}
	chans, err := driver.Discover(ctx)
	if err != nil {
		return 0, fmt.Errorf("discover: %w", err)
	}
	for _, c := range chans {
		callsign := nullIfEmpty(c.Callsign)
		logo := nullIfEmpty(c.LogoURL)
		if _, err := s.q.UpsertChannel(ctx, UpsertChannelParams{
			TunerID:  tuner.ID,
			Number:   c.Number,
			Callsign: callsign,
			Name:     c.Name,
			LogoURL:  logo,
		}); err != nil {
			return 0, fmt.Errorf("upsert channel %s: %w", c.Number, err)
		}
	}
	// Refresh persisted tune_count if Discover learned a new value.
	if tc := driver.TuneCount(); tc > 0 && tc != tuner.TuneCount {
		if _, err := s.q.UpdateTunerDevice(ctx, UpdateTunerDeviceParams{
			ID:        tuner.ID,
			Name:      tuner.Name,
			Config:    tuner.Config,
			TuneCount: tc,
		}); err != nil {
			s.logger.WarnContext(ctx, "update tune_count after discover",
				"tuner_id", tuner.ID, "err", err)
		}
	}
	if err := s.q.TouchTunerLastSeen(ctx, tuner.ID); err != nil {
		// Non-fatal — last_seen_at is just observability.
		s.logger.WarnContext(ctx, "touch last_seen_at",
			"tuner_id", tuner.ID, "err", err)
	}
	return len(chans), nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
