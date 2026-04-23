package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	ID         uuid.UUID
	TunerID    uuid.UUID
	Number     string
	Callsign   *string
	Name       string
	LogoURL    *string
	Enabled    bool
	SortOrder  int32
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ChannelWithTuner is what the channels-list endpoint returns — the
// channel row plus the parent tuner's name/type so the UI can group or
// label streams by source.
type ChannelWithTuner struct {
	Channel
	TunerName string
	TunerType TunerType
}

// NowNextEntry is one row in the channels-page now/next display. The same
// channel can appear twice (program=now, program=next).
type NowNextEntry struct {
	ChannelID   uuid.UUID
	Number      string
	ChannelName string
	LogoURL     *string
	ProgramID   *uuid.UUID
	Title       *string
	Subtitle    *string
	StartsAt    *time.Time
	EndsAt      *time.Time
	SeasonNum   *int32
	EpisodeNum  *int32
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
		return nil, ErrNotFound
	}
	tuner, err := s.q.GetTunerDevice(ctx, ch.TunerID)
	if err != nil {
		return nil, ErrNotFound
	}
	if !tuner.Enabled {
		return nil, ErrNotFound
	}
	driver, err := s.driverFor(tuner)
	if err != nil {
		return nil, fmt.Errorf("driver for tuner %s: %w", tuner.ID, err)
	}
	return driver.OpenStream(ctx, ch.Number)
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
