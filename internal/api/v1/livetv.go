package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/livetv"
)

// LiveTVService is the slice of livetv.Service the HTTP layer uses. Kept
// narrow so handler tests can stub it without standing up real DB / driver
// machinery.
type LiveTVService interface {
	ListTuners(ctx context.Context) ([]livetv.TunerDevice, error)
	GetTuner(ctx context.Context, id uuid.UUID) (livetv.TunerDevice, error)
	CreateTuner(ctx context.Context, p livetv.CreateTunerDeviceParams) (livetv.TunerDevice, error)
	UpdateTuner(ctx context.Context, p livetv.UpdateTunerDeviceParams) (livetv.TunerDevice, error)
	SetTunerEnabled(ctx context.Context, id uuid.UUID, enabled bool) error
	DeleteTuner(ctx context.Context, id uuid.UUID) error
	RescanTuner(ctx context.Context, id uuid.UUID) (int, error)
	DiscoverHDHomeRuns(ctx context.Context) ([]livetv.DiscoveredDevice, error)

	ListChannels(ctx context.Context, enabledOnly bool) ([]livetv.ChannelWithTuner, error)
	GetChannel(ctx context.Context, id uuid.UUID) (livetv.Channel, error)
	SetChannelEnabled(ctx context.Context, id uuid.UUID, enabled bool) error
	NowAndNext(ctx context.Context) ([]livetv.NowNextEntry, error)
	Guide(ctx context.Context, from, to time.Time) ([]livetv.EPGProgram, error)

	ListEPGSources(ctx context.Context) ([]livetv.EPGSource, error)
	CreateEPGSource(ctx context.Context, p livetv.CreateEPGSourceParams) (livetv.EPGSource, error)
	DeleteEPGSource(ctx context.Context, id uuid.UUID) error
	SetEPGSourceEnabled(ctx context.Context, id uuid.UUID, enabled bool) error
	RefreshEPGSource(ctx context.Context, id uuid.UUID) (livetv.RefreshResult, error)
	SetChannelEPGID(ctx context.Context, id uuid.UUID, epgChannelID *string) error
	ListKnownEPGIDs(ctx context.Context) ([]string, error)
	ListUnmappedChannels(ctx context.Context) ([]livetv.Channel, error)
	ReorderChannels(ctx context.Context, orderedIDs []uuid.UUID) error
}

// LiveTVHandler serves the live-TV HTTP API: tuner CRUD (admin),
// channel listing + now/next (any authenticated user). The HLS proxy
// endpoint lives in livetv_stream.go to keep the file sizes manageable.
type LiveTVHandler struct {
	svc    LiveTVService
	proxy  LiveTVStreamProxy
	dvr    LiveTVDVRService
	logger *slog.Logger
}

// NewLiveTVHandler wires the dependencies. svc may be nil — when it is,
// every method returns 503 so the API surface stays consistent even
// without a configured live-TV subsystem.
func NewLiveTVHandler(svc LiveTVService, logger *slog.Logger) *LiveTVHandler {
	return &LiveTVHandler{svc: svc, logger: logger}
}

// ── Response shapes ──────────────────────────────────────────────────────────

// TunerDeviceResponse is the JSON shape of a tuner row. Config is passed
// through as-is so the settings UI can render type-specific forms.
type TunerDeviceResponse struct {
	ID         uuid.UUID       `json:"id"`
	Type       string          `json:"type"`
	Name       string          `json:"name"`
	Config     json.RawMessage `json:"config"`
	TuneCount  int             `json:"tune_count"`
	Enabled    bool            `json:"enabled"`
	LastSeenAt *time.Time      `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// ChannelResponse is the JSON shape of a channel in the list endpoint.
type ChannelResponse struct {
	ID           uuid.UUID `json:"id"`
	TunerID      uuid.UUID `json:"tuner_id"`
	TunerName    string    `json:"tuner_name"`
	TunerType    string    `json:"tuner_type"`
	Number       string    `json:"number"`
	Callsign     *string   `json:"callsign,omitempty"`
	Name         string    `json:"name"`
	LogoURL      *string   `json:"logo_url,omitempty"`
	Enabled      bool      `json:"enabled"`
	SortOrder    int32     `json:"sort_order"`
	EPGChannelID *string   `json:"epg_channel_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// NowNextResponse is one upcoming program in /tv/channels/now-next.
// Up to two rows per channel (current + next). Channels with no EPG data
// don't appear here; the client merges by channel_id against the channels
// list and renders "no guide data" for missing channel IDs.
type NowNextResponse struct {
	ChannelID  uuid.UUID `json:"channel_id"`
	ProgramID  uuid.UUID `json:"program_id"`
	Title      string    `json:"title"`
	Subtitle   *string   `json:"subtitle,omitempty"`
	StartsAt   time.Time `json:"starts_at"`
	EndsAt     time.Time `json:"ends_at"`
	SeasonNum  *int32    `json:"season_num,omitempty"`
	EpisodeNum *int32    `json:"episode_num,omitempty"`
}

// ── Tuner CRUD (admin) ───────────────────────────────────────────────────────

// ListTuners handles GET /api/v1/tv/tuners — returns all configured tuner
// devices including disabled ones, since the settings UI needs to show
// them. Admin-gated by the router.
func (h *LiveTVHandler) ListTuners(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	rows, err := h.svc.ListTuners(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list tuners", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]TunerDeviceResponse, len(rows))
	for i, t := range rows {
		out[i] = tunerToResponse(t)
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// GetTuner handles GET /api/v1/tv/tuners/{id}.
func (h *LiveTVHandler) GetTuner(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid tuner id")
		return
	}
	row, err := h.svc.GetTuner(r.Context(), id)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	respond.Success(w, r, tunerToResponse(row))
}

// createTunerRequest is the POST body for /api/v1/tv/tuners.
type createTunerRequest struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"config"`
	TuneCount int             `json:"tune_count,omitempty"`
}

// CreateTuner handles POST /api/v1/tv/tuners. Validates type is a known
// backend; the backend's factory then validates the config blob.
func (h *LiveTVHandler) CreateTuner(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	var body createTunerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid JSON body")
		return
	}
	if body.Name == "" {
		respond.BadRequest(w, r, "name is required")
		return
	}
	if body.Type != string(livetv.TunerTypeHDHomeRun) && body.Type != string(livetv.TunerTypeM3U) {
		respond.BadRequest(w, r, "type must be 'hdhomerun' or 'm3u'")
		return
	}
	row, err := h.svc.CreateTuner(r.Context(), livetv.CreateTunerDeviceParams{
		Type:      livetv.TunerType(body.Type),
		Name:      body.Name,
		Config:    body.Config,
		TuneCount: body.TuneCount,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create tuner", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Created(w, r, tunerToResponse(row))
}

// updateTunerRequest is the PATCH body for /api/v1/tv/tuners/{id}.
type updateTunerRequest struct {
	Name      string          `json:"name"`
	Config    json.RawMessage `json:"config"`
	TuneCount int             `json:"tune_count,omitempty"`
	Enabled   *bool           `json:"enabled,omitempty"`
}

// UpdateTuner handles PATCH /api/v1/tv/tuners/{id}.
func (h *LiveTVHandler) UpdateTuner(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid tuner id")
		return
	}
	var body updateTunerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid JSON body")
		return
	}
	if _, err := h.svc.UpdateTuner(r.Context(), livetv.UpdateTunerDeviceParams{
		ID: id, Name: body.Name, Config: body.Config, TuneCount: body.TuneCount,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "update tuner", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if body.Enabled != nil {
		if err := h.svc.SetTunerEnabled(r.Context(), id, *body.Enabled); err != nil {
			h.logger.ErrorContext(r.Context(), "set tuner enabled", "id", id, "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	row, err := h.svc.GetTuner(r.Context(), id)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	respond.Success(w, r, tunerToResponse(row))
}

// DeleteTuner handles DELETE /api/v1/tv/tuners/{id}. Cascades through
// channels and EPG programs at the DB level.
func (h *LiveTVHandler) DeleteTuner(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid tuner id")
		return
	}
	if err := h.svc.DeleteTuner(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "delete tuner", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DiscoveredDeviceResponse is one HDHomeRun found by auto-discovery.
type DiscoveredDeviceResponse struct {
	DeviceID   string `json:"device_id"`
	BaseURL    string `json:"base_url"`
	TunerCount int    `json:"tune_count"`
	Model      string `json:"model,omitempty"`
}

// DiscoverTuners handles POST /api/v1/tv/tuners/discover (admin-only).
// Broadcasts a Silicondust UDP discovery packet on the local subnet and
// returns HDHomeRun devices that responded. Caller is expected to loop
// over the result and POST /tv/tuners for each device the user wants to
// add — discovery is intentionally decoupled from persistence so users
// can pick which boxes to import.
func (h *LiveTVHandler) DiscoverTuners(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	devices, err := h.svc.DiscoverHDHomeRuns(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "discover hdhomeruns", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]DiscoveredDeviceResponse, len(devices))
	for i, d := range devices {
		out[i] = DiscoveredDeviceResponse{
			DeviceID: d.DeviceID, BaseURL: d.BaseURL,
			TunerCount: d.TunerCount, Model: d.Model,
		}
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// RescanTuner handles POST /api/v1/tv/tuners/{id}/rescan. Re-runs the
// driver's Discover and upserts the resulting channels. Returns the
// number of channels visible after the scan.
func (h *LiveTVHandler) RescanTuner(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid tuner id")
		return
	}
	n, err := h.svc.RescanTuner(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "rescan tuner", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, map[string]int{"channel_count": n})
}

// ── Channels (any authenticated user) ────────────────────────────────────────

// ListChannels handles GET /api/v1/tv/channels?enabled=true. Default is
// enabled-only since this powers the public channels page; admin UI can
// pass `enabled=false` to see disabled channels too.
func (h *LiveTVHandler) ListChannels(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	enabledOnly := r.URL.Query().Get("enabled") != "false"
	rows, err := h.svc.ListChannels(r.Context(), enabledOnly)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list channels", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]ChannelResponse, len(rows))
	for i, c := range rows {
		out[i] = channelToResponse(c)
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// SetChannelEnabledRequest is the PATCH body for toggling a channel.
type setChannelEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

// SetChannelEnabled handles PATCH /api/v1/tv/channels/{id} with
// {"enabled": true|false}. Lets admins hide channels from the guide
// without losing the discovered metadata.
func (h *LiveTVHandler) SetChannelEnabled(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid channel id")
		return
	}
	var body setChannelEnabledRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid JSON body")
		return
	}
	if err := h.svc.SetChannelEnabled(r.Context(), id, body.Enabled); err != nil {
		h.logger.ErrorContext(r.Context(), "set channel enabled", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reorderChannelsRequest is the PUT body for /api/v1/tv/channels/order.
type reorderChannelsRequest struct {
	ChannelIDs []uuid.UUID `json:"channel_ids"`
}

// ReorderChannels handles PUT /api/v1/tv/channels/order (admin-only).
// Body is an ordered array of channel UUIDs; each index becomes that
// channel's sort_order. IDs not in the list are untouched.
func (h *LiveTVHandler) ReorderChannels(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	var body reorderChannelsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid JSON body")
		return
	}
	if len(body.ChannelIDs) == 0 {
		respond.BadRequest(w, r, "channel_ids is required")
		return
	}
	if err := h.svc.ReorderChannels(r.Context(), body.ChannelIDs); err != nil {
		h.logger.ErrorContext(r.Context(), "reorder channels", "err", err)
		respond.InternalError(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// NowAndNext handles GET /api/v1/tv/channels/now-next — returns a flat
// list of (channel, program) pairs with at most 2 programs per channel
// (current + next). Drives the channels page's now/next strip.
func (h *LiveTVHandler) NowAndNext(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	rows, err := h.svc.NowAndNext(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "now and next", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]NowNextResponse, len(rows))
	for i, e := range rows {
		out[i] = NowNextResponse{
			ChannelID:  e.ChannelID,
			ProgramID:  e.ProgramID,
			Title:      e.Title,
			Subtitle:   e.Subtitle,
			StartsAt:   e.StartsAt,
			EndsAt:     e.EndsAt,
			SeasonNum:  e.SeasonNum,
			EpisodeNum: e.EpisodeNum,
		}
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// guideMaxWindow caps the time window the guide endpoint will expand to.
// Without this, a curious client passing from=2020 to=2030 would scan the
// whole programs table. 24 hours is more than enough for any reasonable
// guide UI; anything wider should be split into multiple requests.
const guideMaxWindow = 24 * time.Hour

// guideDefaultWindow is what we serve when the caller passes neither
// `from` nor `to` — the next 4 hours, which matches the UI's default
// scroll position.
const guideDefaultWindow = 4 * time.Hour

// EPGProgramResponse is one program tile in the guide grid response.
type EPGProgramResponse struct {
	ID              uuid.UUID  `json:"id"`
	ChannelID       uuid.UUID  `json:"channel_id"`
	Title           string     `json:"title"`
	Subtitle        *string    `json:"subtitle,omitempty"`
	Description     *string    `json:"description,omitempty"`
	Category        []string   `json:"category,omitempty"`
	Rating          *string    `json:"rating,omitempty"`
	SeasonNum       *int32     `json:"season_num,omitempty"`
	EpisodeNum      *int32     `json:"episode_num,omitempty"`
	OriginalAirDate *time.Time `json:"original_air_date,omitempty"`
	StartsAt        time.Time  `json:"starts_at"`
	EndsAt          time.Time  `json:"ends_at"`
}

// Guide handles GET /api/v1/tv/guide?from=&to=. Returns every program
// across visible channels overlapping [from, to]. Both query params are
// RFC3339 timestamps; missing means "now → now+4h". Window is capped at
// 24h to prevent runaway scans.
//
// Response is a flat list — the client groups by channel_id and lays out
// programs into a (channel × time) matrix.
func (h *LiveTVHandler) Guide(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	q := r.URL.Query()
	now := time.Now().UTC()
	from := now
	to := now.Add(guideDefaultWindow)
	if s := q.Get("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			respond.BadRequest(w, r, "invalid from timestamp")
			return
		}
		from = t
	}
	if s := q.Get("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			respond.BadRequest(w, r, "invalid to timestamp")
			return
		}
		to = t
	}
	if !to.After(from) {
		respond.BadRequest(w, r, "to must be after from")
		return
	}
	if to.Sub(from) > guideMaxWindow {
		respond.BadRequest(w, r, "guide window exceeds 24h cap")
		return
	}

	rows, err := h.svc.Guide(r.Context(), from, to)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "guide", "from", from, "to", to, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]EPGProgramResponse, len(rows))
	for i, p := range rows {
		out[i] = EPGProgramResponse{
			ID:              p.ID,
			ChannelID:       p.ChannelID,
			Title:           p.Title,
			Subtitle:        p.Subtitle,
			Description:     p.Description,
			Category:        p.Category,
			Rating:          p.Rating,
			SeasonNum:       p.SeasonNum,
			EpisodeNum:      p.EpisodeNum,
			OriginalAirDate: p.OriginalAirDate,
			StartsAt:        p.StartsAt,
			EndsAt:          p.EndsAt,
		}
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// ── EPG sources ──────────────────────────────────────────────────────────────

// EPGSourceResponse is the JSON shape of an EPG source row.
type EPGSourceResponse struct {
	ID                 uuid.UUID       `json:"id"`
	Type               string          `json:"type"`
	Name               string          `json:"name"`
	Config             json.RawMessage `json:"config"`
	RefreshIntervalMin int32           `json:"refresh_interval_min"`
	Enabled            bool            `json:"enabled"`
	LastPullAt         *time.Time      `json:"last_pull_at,omitempty"`
	LastError          *string         `json:"last_error,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

// ListEPGSources handles GET /api/v1/tv/epg-sources (admin-only).
func (h *LiveTVHandler) ListEPGSources(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	rows, err := h.svc.ListEPGSources(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list epg sources", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]EPGSourceResponse, len(rows))
	for i, s := range rows {
		out[i] = epgSourceToResponse(s)
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// createEPGSourceRequest is the POST body for /api/v1/tv/epg-sources.
type createEPGSourceRequest struct {
	Type               string          `json:"type"`
	Name               string          `json:"name"`
	Config             json.RawMessage `json:"config"`
	RefreshIntervalMin int32           `json:"refresh_interval_min,omitempty"`
}

// CreateEPGSource handles POST /api/v1/tv/epg-sources (admin-only).
// XMLTV is the only Phase B Round 1 backend; Schedules Direct comes later.
func (h *LiveTVHandler) CreateEPGSource(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	var body createEPGSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid JSON body")
		return
	}
	if body.Name == "" {
		respond.BadRequest(w, r, "name is required")
		return
	}
	if body.Type != string(livetv.EPGSourceTypeXMLTVURL) && body.Type != string(livetv.EPGSourceTypeXMLTVFile) {
		respond.BadRequest(w, r, "type must be 'xmltv_url' or 'xmltv_file'")
		return
	}
	row, err := h.svc.CreateEPGSource(r.Context(), livetv.CreateEPGSourceParams{
		Type:               livetv.EPGSourceType(body.Type),
		Name:               body.Name,
		Config:             body.Config,
		RefreshIntervalMin: body.RefreshIntervalMin,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create epg source", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Created(w, r, epgSourceToResponse(row))
}

// DeleteEPGSource handles DELETE /api/v1/tv/epg-sources/{id} (admin-only).
func (h *LiveTVHandler) DeleteEPGSource(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid epg source id")
		return
	}
	if err := h.svc.DeleteEPGSource(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "delete epg source", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RefreshEPGSource handles POST /api/v1/tv/epg-sources/{id}/refresh
// (admin-only). Synchronously pulls + parses + ingests; can take tens of
// seconds for large sources, so the UI should show a spinner.
func (h *LiveTVHandler) RefreshEPGSource(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid epg source id")
		return
	}
	res, err := h.svc.RefreshEPGSource(r.Context(), id)
	if err != nil {
		// Surface the error message in the response body so the settings
		// UI can show "fetch failed: status 401" inline. The error has
		// also been recorded into epg_sources.last_error by the service.
		respond.Error(w, r, http.StatusBadGateway, "EPG_REFRESH_FAILED", err.Error())
		return
	}
	respond.Success(w, r, map[string]any{
		"programs_ingested":     res.ProgramsIngested,
		"channels_auto_matched": res.ChannelsAutoMatched,
		"unmapped_channels":     res.UnmappedChannels,
		"skipped":               res.Skipped,
	})
}

// UnmappedChannelResponse is one channel lacking an EPG mapping. Client
// renders these in the manual-mapping settings section.
type UnmappedChannelResponse struct {
	ID       uuid.UUID `json:"id"`
	Number   string    `json:"number"`
	Callsign *string   `json:"callsign,omitempty"`
	Name     string    `json:"name"`
	LogoURL  *string   `json:"logo_url,omitempty"`
}

// ListUnmappedChannels handles GET /api/v1/tv/channels/unmapped.
// Admin-only; drives the manual mapping UI.
func (h *LiveTVHandler) ListUnmappedChannels(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	rows, err := h.svc.ListUnmappedChannels(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list unmapped channels", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]UnmappedChannelResponse, len(rows))
	for i, c := range rows {
		out[i] = UnmappedChannelResponse{
			ID: c.ID, Number: c.Number, Callsign: c.Callsign,
			Name: c.Name, LogoURL: c.LogoURL,
		}
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// ListEPGIDs handles GET /api/v1/tv/epg-ids. Admin-only; returns every
// EPG channel ID the system has seen (from current mappings or ingested
// programs) so the mapping UI can show a dropdown.
func (h *LiveTVHandler) ListEPGIDs(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	ids, err := h.svc.ListKnownEPGIDs(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list epg ids", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.List(w, r, ids, int64(len(ids)), "")
}

// setChannelEPGIDRequest is the PATCH body for assigning an EPG channel ID.
type setChannelEPGIDRequest struct {
	EPGChannelID *string `json:"epg_channel_id"`
}

// SetChannelEPGID handles PATCH /api/v1/tv/channels/{id}/epg-id
// (admin-only). Lets operators manually map a channel when auto-match
// got it wrong. Pass {"epg_channel_id": null} to clear the mapping.
func (h *LiveTVHandler) SetChannelEPGID(w http.ResponseWriter, r *http.Request) {
	if !h.available(w, r) {
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid channel id")
		return
	}
	var body setChannelEPGIDRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid JSON body")
		return
	}
	if err := h.svc.SetChannelEPGID(r.Context(), id, body.EPGChannelID); err != nil {
		h.logger.ErrorContext(r.Context(), "set channel epg id", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// epgSourceToResponse maps a domain EPGSource into the JSON shape.
func epgSourceToResponse(s livetv.EPGSource) EPGSourceResponse {
	out := EPGSourceResponse{
		ID:                 s.ID,
		Type:               string(s.Type),
		Name:               s.Name,
		Config:             s.Config,
		RefreshIntervalMin: s.RefreshIntervalMin,
		Enabled:            s.Enabled,
		LastError:          s.LastError,
		CreatedAt:          s.CreatedAt,
		UpdatedAt:          s.UpdatedAt,
	}
	if s.LastPullAt != nil {
		t := *s.LastPullAt
		out.LastPullAt = &t
	}
	return out
}

// ── Internals ────────────────────────────────────────────────────────────────

// available returns true when the handler can serve the request. svc==nil
// happens when live TV isn't configured; we 503 with a stable error code
// so the UI can render "not configured" rather than spin.
func (h *LiveTVHandler) available(w http.ResponseWriter, r *http.Request) bool {
	if h.svc == nil {
		respond.Error(w, r, http.StatusServiceUnavailable,
			"LIVE_TV_NOT_CONFIGURED", "live TV is not enabled on this server")
		return false
	}
	return true
}

func tunerToResponse(t livetv.TunerDevice) TunerDeviceResponse {
	out := TunerDeviceResponse{
		ID: t.ID, Type: string(t.Type), Name: t.Name, Config: t.Config,
		TuneCount: t.TuneCount, Enabled: t.Enabled,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
	if t.LastSeenAt != nil {
		ls := *t.LastSeenAt
		out.LastSeenAt = &ls
	}
	return out
}

func channelToResponse(c livetv.ChannelWithTuner) ChannelResponse {
	return ChannelResponse{
		ID: c.ID, TunerID: c.TunerID, TunerName: c.TunerName, TunerType: string(c.TunerType),
		Number: c.Number, Callsign: c.Callsign, Name: c.Name, LogoURL: c.LogoURL,
		Enabled: c.Enabled, SortOrder: c.SortOrder, EPGChannelID: c.EPGChannelID,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

