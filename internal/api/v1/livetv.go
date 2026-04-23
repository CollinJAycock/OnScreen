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

	ListChannels(ctx context.Context, enabledOnly bool) ([]livetv.ChannelWithTuner, error)
	GetChannel(ctx context.Context, id uuid.UUID) (livetv.Channel, error)
	SetChannelEnabled(ctx context.Context, id uuid.UUID, enabled bool) error
	NowAndNext(ctx context.Context) ([]livetv.NowNextEntry, error)
}

// LiveTVHandler serves the live-TV HTTP API: tuner CRUD (admin),
// channel listing + now/next (any authenticated user). The HLS proxy
// endpoint lives in livetv_stream.go to keep the file sizes manageable.
type LiveTVHandler struct {
	svc    LiveTVService
	proxy  LiveTVStreamProxy
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
	ID        uuid.UUID `json:"id"`
	TunerID   uuid.UUID `json:"tuner_id"`
	TunerName string    `json:"tuner_name"`
	TunerType string    `json:"tuner_type"`
	Number    string    `json:"number"`
	Callsign  *string   `json:"callsign,omitempty"`
	Name      string    `json:"name"`
	LogoURL   *string   `json:"logo_url,omitempty"`
	Enabled   bool      `json:"enabled"`
	SortOrder int32     `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NowNextResponse is one entry in /tv/channels/now-next. The same channel
// can appear twice (current program + next program). When `program_id` is
// missing, the channel has no upcoming EPG data — the UI should render
// the channel without a "now playing" line.
type NowNextResponse struct {
	ChannelID   uuid.UUID  `json:"channel_id"`
	Number      string     `json:"number"`
	ChannelName string     `json:"channel_name"`
	LogoURL     *string    `json:"logo_url,omitempty"`
	ProgramID   *uuid.UUID `json:"program_id,omitempty"`
	Title       *string    `json:"title,omitempty"`
	Subtitle    *string    `json:"subtitle,omitempty"`
	StartsAt    *time.Time `json:"starts_at,omitempty"`
	EndsAt      *time.Time `json:"ends_at,omitempty"`
	SeasonNum   *int32     `json:"season_num,omitempty"`
	EpisodeNum  *int32     `json:"episode_num,omitempty"`
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
	respond.JSON(w, r, http.StatusOK, tunerToResponse(row))
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
	respond.JSON(w, r, http.StatusCreated, tunerToResponse(row))
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
	respond.JSON(w, r, http.StatusOK, tunerToResponse(row))
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
	respond.JSON(w, r, http.StatusOK, map[string]int{"channel_count": n})
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
			ChannelID:   e.ChannelID,
			Number:      e.Number,
			ChannelName: e.ChannelName,
			LogoURL:     e.LogoURL,
			ProgramID:   e.ProgramID,
			Title:       e.Title,
			Subtitle:    e.Subtitle,
			StartsAt:    e.StartsAt,
			EndsAt:      e.EndsAt,
			SeasonNum:   e.SeasonNum,
			EpisodeNum:  e.EpisodeNum,
		}
	}
	respond.List(w, r, out, int64(len(out)), "")
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
		Enabled: c.Enabled, SortOrder: c.SortOrder,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

