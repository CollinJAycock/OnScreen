package v1

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
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
}

// SettingsHandler handles GET/PATCH /api/v1/settings.
type SettingsHandler struct {
	svc    SettingsServiceIface
	logger *slog.Logger
	audit  *audit.Logger
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

type settingsResponse struct {
	TMDBAPIKey      string            `json:"tmdb_api_key"`
	TVDBAPIKey      string            `json:"tvdb_api_key"`
	ArrAPIKey       string            `json:"arr_api_key"`
	ArrWebhookURL   string            `json:"arr_webhook_url"`
	ArrPathMappings map[string]string `json:"arr_path_mappings,omitempty"`
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
		TMDBAPIKey:      maskAPIKey(h.svc.TMDBAPIKey(ctx)),
		TVDBAPIKey:      maskAPIKey(h.svc.TVDBAPIKey(ctx)),
		ArrAPIKey:       maskAPIKey(arrKey),
		ArrWebhookURL:   webhookURL,
		ArrPathMappings: h.svc.ArrPathMappings(ctx),
	})
}

// Update handles PATCH /api/v1/settings.
func (h *SettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TMDBAPIKey      *string            `json:"tmdb_api_key"`
		TVDBAPIKey      *string            `json:"tvdb_api_key"`
		ArrAPIKey       *string            `json:"arr_api_key"`
		ArrPathMappings *map[string]string `json:"arr_path_mappings"`
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
		claims := middleware.ClaimsFromContext(r.Context())
		if claims != nil {
			h.audit.Log(r.Context(), &claims.UserID, audit.ActionSettingsUpdate, "", detail, audit.ClientIP(r))
		}
	}
	respond.NoContent(w)
}
