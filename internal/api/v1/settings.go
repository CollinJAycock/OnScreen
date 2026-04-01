package v1

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/transcode"
)

// SettingsServiceIface is the subset of settings.Service used by the handler.
type SettingsServiceIface interface {
	TMDBAPIKey(ctx context.Context) string
	SetTMDBAPIKey(ctx context.Context, key string) error
	TVDBAPIKey(ctx context.Context) string
	SetTVDBAPIKey(ctx context.Context, key string) error
	ArrAPIKey(ctx context.Context) string
	SetArrAPIKey(ctx context.Context, key string) error
	ArrPathMappings(ctx context.Context) map[string]string
	SetArrPathMappings(ctx context.Context, mappings map[string]string) error
	TranscodeEncoders(ctx context.Context) string
	SetTranscodeEncoders(ctx context.Context, value string) error
	WorkerFleet(ctx context.Context) settings.WorkerFleetConfig
	SetWorkerFleet(ctx context.Context, cfg settings.WorkerFleetConfig) error
}

// WorkerLister lists registered transcode workers from the session store.
type WorkerLister interface {
	ListWorkers(ctx context.Context) ([]transcode.WorkerRegistration, error)
}

// SettingsHandler handles GET/PATCH /api/v1/settings.
type SettingsHandler struct {
	svc              SettingsServiceIface
	logger           *slog.Logger
	audit            *audit.Logger
	detectedEncoders []transcode.EncoderEntry // populated at startup by DetectEncoders
	workerLister     WorkerLister             // set at startup to query registered workers
}

// NewSettingsHandler creates a SettingsHandler.
func NewSettingsHandler(svc SettingsServiceIface, logger *slog.Logger) *SettingsHandler {
	return &SettingsHandler{svc: svc, logger: logger}
}

// WithAudit attaches an audit logger. Returns the handler for chaining.
func (h *SettingsHandler) WithAudit(a *audit.Logger) *SettingsHandler {
	h.audit = a
	return h
}

// SetDetectedEncoders stores the encoder entries discovered at startup.
func (h *SettingsHandler) SetDetectedEncoders(entries []transcode.EncoderEntry) {
	h.detectedEncoders = entries
}

// SetWorkerLister wires the session store for querying registered workers.
func (h *SettingsHandler) SetWorkerLister(wl WorkerLister) {
	h.workerLister = wl
}

// GetEncoders handles GET /api/v1/settings/encoders — returns available hw encoders.
func (h *SettingsHandler) GetEncoders(w http.ResponseWriter, r *http.Request) {
	current := h.svc.TranscodeEncoders(r.Context())
	respond.Success(w, r, struct {
		Detected []transcode.EncoderEntry `json:"detected"`
		Current  string                   `json:"current"`
	}{
		Detected: h.detectedEncoders,
		Current:  current,
	})
}

// GetWorkers handles GET /api/v1/settings/workers — returns registered transcode workers.
func (h *SettingsHandler) GetWorkers(w http.ResponseWriter, r *http.Request) {
	if h.workerLister == nil {
		respond.Success(w, r, []transcode.WorkerRegistration{})
		return
	}
	workers, err := h.workerLister.ListWorkers(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list workers", "err", err)
		respond.Success(w, r, []transcode.WorkerRegistration{})
		return
	}
	if workers == nil {
		workers = []transcode.WorkerRegistration{}
	}
	respond.Success(w, r, workers)
}

// GetFleet handles GET /api/v1/settings/fleet — returns the worker fleet configuration.
// Workers self-register via Valkey; the fleet config stores admin overrides (name, encoder).
// The response merges live worker data with saved overrides.
func (h *SettingsHandler) GetFleet(w http.ResponseWriter, r *http.Request) {
	fleet := h.svc.WorkerFleet(r.Context())

	// Fetch live workers from Valkey.
	var liveWorkers []transcode.WorkerRegistration
	if h.workerLister != nil {
		liveWorkers, _ = h.workerLister.ListWorkers(r.Context())
	}

	// Index saved overrides by worker addr.
	overrides := make(map[string]settings.WorkerSlotConfig, len(fleet.Workers))
	for _, slot := range fleet.Workers {
		overrides[slot.Addr] = slot
	}

	type workerStatus struct {
		ID             string   `json:"id"`
		Addr           string   `json:"addr"`
		Name           string   `json:"name"`
		Encoder        string   `json:"encoder"`
		Online         bool     `json:"online"`
		ActiveSessions int      `json:"active_sessions"`
		MaxSessions    int      `json:"max_sessions"`
		Capabilities   []string `json:"capabilities"`
	}

	// Check embedded worker status.
	embeddedOnline := false
	embeddedActive, embeddedMax := 0, 0
	var embeddedCaps []string

	seen := make(map[string]bool, len(liveWorkers))
	var workers []workerStatus
	for _, live := range liveWorkers {
		// The embedded worker listens on 127.0.0.1:7073 — show it in the
		// embedded section, not as a local worker.
		if live.Addr == "127.0.0.1:7073" {
			embeddedOnline = true
			embeddedActive = live.ActiveSessions
			embeddedMax = live.MaxSessions
			embeddedCaps = live.Capabilities
			continue
		}

		seen[live.Addr] = true
		ws := workerStatus{
			ID:             live.Addr,
			Addr:           live.Addr,
			Online:         true,
			ActiveSessions: live.ActiveSessions,
			MaxSessions:    live.MaxSessions,
			Capabilities:   live.Capabilities,
		}
		if override, ok := overrides[live.Addr]; ok {
			ws.Name = override.Name
			ws.Encoder = override.Encoder
		}
		workers = append(workers, ws)
	}

	// Append saved (manually-added) workers that aren't currently live.
	manualIdx := 0
	for _, slot := range fleet.Workers {
		if slot.Addr == "127.0.0.1:7073" {
			continue
		}
		if slot.Addr != "" && seen[slot.Addr] {
			continue
		}
		id := slot.Addr
		if id == "" {
			manualIdx++
			id = fmt.Sprintf("manual-%d", manualIdx)
		}
		workers = append(workers, workerStatus{
			ID:      id,
			Addr:    slot.Addr,
			Name:    slot.Name,
			Encoder: slot.Encoder,
			Online:  false,
		})
	}

	if workers == nil {
		workers = []workerStatus{}
	}

	respond.Success(w, r, struct {
		EmbeddedEnabled bool           `json:"embedded_enabled"`
		EmbeddedEncoder string         `json:"embedded_encoder"`
		EmbeddedOnline  bool           `json:"embedded_online"`
		EmbeddedActive  int            `json:"embedded_active_sessions"`
		EmbeddedMax     int            `json:"embedded_max_sessions"`
		EmbeddedCaps    []string       `json:"embedded_capabilities"`
		Workers         []workerStatus `json:"workers"`
	}{
		EmbeddedEnabled: fleet.EmbeddedEnabled,
		EmbeddedEncoder: fleet.EmbeddedEncoder,
		EmbeddedOnline:  embeddedOnline,
		EmbeddedActive:  embeddedActive,
		EmbeddedMax:     embeddedMax,
		EmbeddedCaps:    embeddedCaps,
		Workers:         workers,
	})
}

// UpdateFleet handles PUT /api/v1/settings/fleet — saves the worker fleet configuration.
func (h *SettingsHandler) UpdateFleet(w http.ResponseWriter, r *http.Request) {
	var body settings.WorkerFleetConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	if err := h.svc.SetWorkerFleet(ctx, body); err != nil {
		h.logger.ErrorContext(ctx, "update fleet config", "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		claims := middleware.ClaimsFromContext(ctx)
		if claims != nil {
			h.audit.Log(ctx, &claims.UserID, audit.ActionSettingsUpdate, "", map[string]any{
				"worker_fleet": "changed",
			}, audit.ClientIP(r))
		}
	}
	respond.NoContent(w)
}

type settingsResponse struct {
	TMDBAPIKey        string            `json:"tmdb_api_key"`
	TVDBAPIKey        string            `json:"tvdb_api_key"`
	ArrAPIKey         string            `json:"arr_api_key"`
	ArrWebhookURL     string            `json:"arr_webhook_url"`
	ArrPathMappings   map[string]string `json:"arr_path_mappings,omitempty"`
	TranscodeEncoders string            `json:"transcode_encoders"`
}

// maskAPIKey returns the first 4 chars + "****" if the key is longer than 4,
// "****" if 1-4 chars, or empty if empty.
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) > 4 {
		return key[:4] + "****"
	}
	return "****"
}

// generateAPIKey creates a random 32-character hex API key.
func generateAPIKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Get handles GET /api/v1/settings.
func (h *SettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	arrKey := h.svc.ArrAPIKey(ctx)

	// Auto-generate an arr API key if none exists.
	if arrKey == "" {
		arrKey = generateAPIKey()
		if err := h.svc.SetArrAPIKey(ctx, arrKey); err != nil {
			h.logger.ErrorContext(ctx, "auto-generate arr api key", "err", err)
		}
	}

	// Build the webhook URL from the request so the admin can copy it into arr apps.
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	webhookURL := scheme + "://" + r.Host + "/api/v1/arr/webhook?apikey=" + arrKey

	respond.Success(w, r, settingsResponse{
		TMDBAPIKey:        maskAPIKey(h.svc.TMDBAPIKey(ctx)),
		TVDBAPIKey:        maskAPIKey(h.svc.TVDBAPIKey(ctx)),
		ArrAPIKey:         maskAPIKey(arrKey),
		ArrWebhookURL:     webhookURL,
		ArrPathMappings:   h.svc.ArrPathMappings(ctx),
		TranscodeEncoders: h.svc.TranscodeEncoders(ctx),
	})
}

// Update handles PATCH /api/v1/settings.
func (h *SettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TMDBAPIKey        *string            `json:"tmdb_api_key"`
		TVDBAPIKey        *string            `json:"tvdb_api_key"`
		ArrAPIKey         *string            `json:"arr_api_key"`
		ArrPathMappings   *map[string]string `json:"arr_path_mappings"`
		TranscodeEncoders *string            `json:"transcode_encoders"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	if body.TMDBAPIKey != nil {
		if err := h.svc.SetTMDBAPIKey(ctx, *body.TMDBAPIKey); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "tmdb_api_key", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.TVDBAPIKey != nil {
		if err := h.svc.SetTVDBAPIKey(ctx, *body.TVDBAPIKey); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "tvdb_api_key", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.ArrAPIKey != nil {
		if err := h.svc.SetArrAPIKey(ctx, *body.ArrAPIKey); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "arr_api_key", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.ArrPathMappings != nil {
		if err := h.svc.SetArrPathMappings(ctx, *body.ArrPathMappings); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "arr_path_mappings", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if body.TranscodeEncoders != nil {
		if err := h.svc.SetTranscodeEncoders(ctx, *body.TranscodeEncoders); err != nil {
			h.logger.ErrorContext(ctx, "update settings", "key", "transcode_encoders", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	if h.audit != nil {
		detail := map[string]any{}
		if body.TMDBAPIKey != nil {
			detail["tmdb_api_key"] = "changed"
		}
		if body.TVDBAPIKey != nil {
			detail["tvdb_api_key"] = "changed"
		}
		if body.ArrAPIKey != nil {
			detail["arr_api_key"] = "changed"
		}
		if body.ArrPathMappings != nil {
			detail["arr_path_mappings"] = "changed"
		}
		if body.TranscodeEncoders != nil {
			detail["transcode_encoders"] = *body.TranscodeEncoders
		}
		claims := middleware.ClaimsFromContext(r.Context())
		if claims != nil {
			h.audit.Log(r.Context(), &claims.UserID, audit.ActionSettingsUpdate, "", detail, audit.ClientIP(r))
		}
	}
	respond.NoContent(w)
}
