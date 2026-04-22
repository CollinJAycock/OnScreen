package v1

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/plugin"
)

// Audit actions for plugin admin CRUD. These describe who configured which
// outbound egress target — a high-value audit signal since a registered
// plugin defines a channel along which event metadata leaves the server.
const (
	ActionPluginCreate = "plugin.create"
	ActionPluginUpdate = "plugin.update"
	ActionPluginDelete = "plugin.delete"
)

// PluginHandler exposes admin-only CRUD for outbound MCP plugin registrations.
type PluginHandler struct {
	registry   *plugin.Registry
	dispatcher *plugin.NotificationDispatcher
	logger     *slog.Logger
	audit      *audit.Logger
}

// NewPluginHandler builds a PluginHandler. registry must be non-nil.
// dispatcher is optional — without it the Test endpoint returns 503.
func NewPluginHandler(registry *plugin.Registry, dispatcher *plugin.NotificationDispatcher, logger *slog.Logger) *PluginHandler {
	return &PluginHandler{registry: registry, dispatcher: dispatcher, logger: logger}
}

// WithAudit attaches an audit logger. Returns the handler for chaining.
func (h *PluginHandler) WithAudit(a *audit.Logger) *PluginHandler {
	h.audit = a
	return h
}

// auditEvent writes an admin-CRUD audit row if an audit logger is attached.
// actor is derived from the PASETO claims on the request context.
func (h *PluginHandler) auditEvent(r *http.Request, action, target string, detail map[string]any) {
	if h.audit == nil {
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return
	}
	actor := claims.UserID
	h.audit.Log(r.Context(), &actor, action, target, detail, audit.ClientIP(r))
}

// pluginDTO is the JSON shape returned to admin clients. allowed_hosts is
// always an array (never null) so the UI doesn't have to special-case it.
type pluginDTO struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Role         string   `json:"role"`
	Transport    string   `json:"transport"`
	EndpointURL  string   `json:"endpoint_url"`
	AllowedHosts []string `json:"allowed_hosts"`
	Enabled      bool     `json:"enabled"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

func toDTO(p plugin.Plugin) pluginDTO {
	hosts := p.AllowedHosts
	if hosts == nil {
		hosts = []string{}
	}
	return pluginDTO{
		ID:           p.ID.String(),
		Name:         p.Name,
		Role:         string(p.Role),
		Transport:    string(p.Transport),
		EndpointURL:  p.EndpointURL,
		AllowedHosts: hosts,
		Enabled:      p.Enabled,
		CreatedAt:    p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:    p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// List handles GET /api/v1/admin/plugins.
func (h *PluginHandler) List(w http.ResponseWriter, r *http.Request) {
	plugins, err := h.registry.List(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list plugins", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]pluginDTO, 0, len(plugins))
	for _, p := range plugins {
		out = append(out, toDTO(p))
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// Create handles POST /api/v1/admin/plugins.
func (h *PluginHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string   `json:"name"`
		Role         string   `json:"role"`
		EndpointURL  string   `json:"endpoint_url"`
		AllowedHosts []string `json:"allowed_hosts"`
		Enabled      *bool    `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	created, err := h.registry.Create(r.Context(), plugin.CreateInput{
		Name:         body.Name,
		Role:         plugin.Role(body.Role),
		EndpointURL:  body.EndpointURL,
		AllowedHosts: body.AllowedHosts,
		Enabled:      enabled,
	})
	if err != nil {
		respond.ValidationError(w, r, err.Error())
		return
	}
	h.auditEvent(r, ActionPluginCreate, created.ID.String(), map[string]any{
		"name":          created.Name,
		"role":          string(created.Role),
		"endpoint_url":  created.EndpointURL,
		"allowed_hosts": created.AllowedHosts,
		"enabled":       created.Enabled,
	})
	respond.Created(w, r, toDTO(created))
}

// Update handles PATCH /api/v1/admin/plugins/{id}. Role and transport are
// immutable after creation — change of role would invalidate every dispatch
// site that already queried by role.
func (h *PluginHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid plugin id")
		return
	}
	existing, err := h.registry.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, plugin.ErrPluginNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get plugin", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	var body struct {
		Name         *string  `json:"name"`
		EndpointURL  *string  `json:"endpoint_url"`
		AllowedHosts []string `json:"allowed_hosts"`
		Enabled      *bool    `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	in := plugin.UpdateInput{
		Name:         existing.Name,
		EndpointURL:  existing.EndpointURL,
		AllowedHosts: existing.AllowedHosts,
		Enabled:      existing.Enabled,
	}
	if body.Name != nil {
		in.Name = *body.Name
	}
	if body.EndpointURL != nil {
		in.EndpointURL = *body.EndpointURL
	}
	if body.AllowedHosts != nil {
		in.AllowedHosts = body.AllowedHosts
	}
	if body.Enabled != nil {
		in.Enabled = *body.Enabled
	}
	updated, err := h.registry.Update(r.Context(), id, in)
	if err != nil {
		respond.ValidationError(w, r, err.Error())
		return
	}
	// Record only the fields that actually changed — keeps the audit trail
	// focused on the delta the operator approved, not the full post-state.
	detail := map[string]any{}
	if updated.Name != existing.Name {
		detail["name"] = map[string]any{"from": existing.Name, "to": updated.Name}
	}
	if updated.EndpointURL != existing.EndpointURL {
		detail["endpoint_url"] = map[string]any{"from": existing.EndpointURL, "to": updated.EndpointURL}
	}
	if !equalHostList(existing.AllowedHosts, updated.AllowedHosts) {
		detail["allowed_hosts"] = map[string]any{"from": existing.AllowedHosts, "to": updated.AllowedHosts}
	}
	if updated.Enabled != existing.Enabled {
		detail["enabled"] = map[string]any{"from": existing.Enabled, "to": updated.Enabled}
	}
	if len(detail) > 0 {
		h.auditEvent(r, ActionPluginUpdate, updated.ID.String(), detail)
	}
	respond.Success(w, r, toDTO(updated))
}

func equalHostList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Test handles POST /api/v1/admin/plugins/{id}/test. Sends a synthetic
// notify event to the plugin and returns the result synchronously so the
// admin UI can surface success/failure in the same click.
func (h *PluginHandler) Test(w http.ResponseWriter, r *http.Request) {
	if h.dispatcher == nil {
		respond.InternalError(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid plugin id")
		return
	}
	evt := plugin.NotificationEvent{
		Event: "media.play",
		Title: "OnScreen plugin test",
		Body:  "Synthetic event generated by the admin test button.",
	}
	if err := h.dispatcher.TestDispatch(r.Context(), id, evt); err != nil {
		respond.ValidationError(w, r, err.Error())
		return
	}
	respond.NoContent(w)
}

// Delete handles DELETE /api/v1/admin/plugins/{id}.
func (h *PluginHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid plugin id")
		return
	}
	// Fetch pre-delete so the audit row captures what was removed — the DTO
	// is gone the moment the row is. Not-found is logged as a no-op delete.
	existing, getErr := h.registry.Get(r.Context(), id)
	if err := h.registry.Delete(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "delete plugin", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if getErr == nil {
		h.auditEvent(r, ActionPluginDelete, id.String(), map[string]any{
			"name":         existing.Name,
			"role":         string(existing.Role),
			"endpoint_url": existing.EndpointURL,
		})
	}
	respond.NoContent(w)
}
