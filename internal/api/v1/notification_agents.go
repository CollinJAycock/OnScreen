package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/notifyagents"
)

// NotificationAgentService is the notifyagents surface the admin API needs
// (*notifyagents.Service satisfies it).
type NotificationAgentService interface {
	List(ctx context.Context) ([]notifyagents.Agent, error)
	Get(ctx context.Context, id uuid.UUID) (notifyagents.Agent, error)
	Create(ctx context.Context, in notifyagents.CreateInput) (notifyagents.Agent, error)
	Update(ctx context.Context, id uuid.UUID, in notifyagents.UpdateInput) (notifyagents.Agent, error)
	Delete(ctx context.Context, id uuid.UUID) error
	SendTest(ctx context.Context, id uuid.UUID) error
}

// notificationAgentBodyMax bounds a create/update body.
const notificationAgentBodyMax = 64 << 10

// NotificationAgentHandler serves /api/v1/admin/notification-agents: the
// server-wide Discord / Telegram / ntfy / Gotify / email channels. Admin
// only; every change and test send is audited. Secrets are write-only — the
// responses carry secret_configured, never the value.
type NotificationAgentHandler struct {
	svc    NotificationAgentService
	logger *slog.Logger
	audit  *audit.Logger
}

// NewNotificationAgentHandler creates the handler.
func NewNotificationAgentHandler(svc NotificationAgentService, logger *slog.Logger) *NotificationAgentHandler {
	return &NotificationAgentHandler{svc: svc, logger: logger}
}

// WithAudit attaches the audit logger. Returns the handler for chaining.
func (h *NotificationAgentHandler) WithAudit(a *audit.Logger) *NotificationAgentHandler {
	h.audit = a
	return h
}

// NotificationAgentTestResult is the body of POST …/{id}/test.
type NotificationAgentTestResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (h *NotificationAgentHandler) actor(r *http.Request) *uuid.UUID {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return nil
	}
	uid := claims.UserID
	return &uid
}

// auditDetail describes an agent for the audit log: kind, name, events and
// the destination HOST for self-hosted kinds — never the secret, and never a
// Discord webhook URL (it is the secret).
func auditDetail(a notifyagents.Agent) map[string]any {
	d := map[string]any{
		"kind":    a.Kind,
		"name":    a.Name,
		"events":  a.Events,
		"enabled": a.Enabled,
	}
	if a.Config.ServerURL != "" {
		if u, err := url.Parse(a.Config.ServerURL); err == nil {
			d["host"] = u.Host
		}
		d["allow_private_network"] = a.AllowPrivateNetwork
	}
	if len(a.Config.Recipients) > 0 {
		d["recipients"] = len(a.Config.Recipients)
	}
	return d
}

func (h *NotificationAgentHandler) writeError(w http.ResponseWriter, r *http.Request, op string, err error) {
	var verr *notifyagents.ValidationError
	switch {
	case errors.As(err, &verr):
		respond.ValidationError(w, r, verr.Msg)
	case errors.Is(err, notifyagents.ErrNotFound):
		respond.NotFound(w, r)
	default:
		h.logger.ErrorContext(r.Context(), "notification agents: "+op, "err", err)
		respond.InternalError(w, r)
	}
}

// Events handles GET /api/v1/admin/notification-agents/events: the event
// keys an agent can subscribe to, with labels and per-kind defaults.
func (h *NotificationAgentHandler) Events(w http.ResponseWriter, r *http.Request) {
	respond.Success(w, r, notifyagents.Events())
}

// List handles GET /api/v1/admin/notification-agents.
func (h *NotificationAgentHandler) List(w http.ResponseWriter, r *http.Request) {
	agents, err := h.svc.List(r.Context())
	if err != nil {
		h.writeError(w, r, "list", err)
		return
	}
	respond.List(w, r, agents, int64(len(agents)), "")
}

// Create handles POST /api/v1/admin/notification-agents.
func (h *NotificationAgentHandler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, notificationAgentBodyMax)
	var in notifyagents.CreateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	a, err := h.svc.Create(r.Context(), in)
	if err != nil {
		h.writeError(w, r, "create", err)
		return
	}
	if h.audit != nil {
		d := auditDetail(a)
		d["secret_set"] = a.SecretConfigured
		h.audit.Log(r.Context(), h.actor(r), audit.ActionNotificationAgentCreate, a.ID.String(), d, audit.ClientIP(r))
	}
	respond.Created(w, r, a)
}

// Update handles PATCH /api/v1/admin/notification-agents/{id}. A blank or
// omitted secret keeps the stored one; changing an ntfy / Gotify server URL
// without re-entering the token is refused with 422.
func (h *NotificationAgentHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid notification agent id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, notificationAgentBodyMax)
	var in notifyagents.UpdateInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	a, err := h.svc.Update(r.Context(), id, in)
	if err != nil {
		h.writeError(w, r, "update", err)
		return
	}
	if h.audit != nil {
		d := auditDetail(a)
		d["secret_changed"] = (in.Secret != nil && *in.Secret != "") || in.ClearSecret
		h.audit.Log(r.Context(), h.actor(r), audit.ActionNotificationAgentUpdate, id.String(), d, audit.ClientIP(r))
	}
	respond.Success(w, r, a)
}

// Delete handles DELETE /api/v1/admin/notification-agents/{id}.
func (h *NotificationAgentHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid notification agent id")
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		h.writeError(w, r, "delete", err)
		return
	}
	if h.audit != nil {
		h.audit.Log(r.Context(), h.actor(r), audit.ActionNotificationAgentDelete, id.String(), nil, audit.ClientIP(r))
	}
	respond.NoContent(w)
}

// Test handles POST /api/v1/admin/notification-agents/{id}/test: sends the
// Test event synchronously (enabled or not) and reports {ok, error?}. A
// delivery failure is a 200 with ok=false — the error text is sanitized (no
// secret, no webhook URL) and meant for the admin.
func (h *NotificationAgentHandler) Test(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid notification agent id")
		return
	}
	a, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.writeError(w, r, "get for test", err)
		return
	}
	res := NotificationAgentTestResult{OK: true}
	if err := h.svc.SendTest(r.Context(), id); err != nil {
		var derr *notifyagents.DeliveryError
		if !errors.As(err, &derr) {
			h.writeError(w, r, "test", err)
			return
		}
		res = NotificationAgentTestResult{OK: false, Error: derr.Msg}
	}
	if h.audit != nil {
		h.audit.Log(r.Context(), h.actor(r), audit.ActionNotificationAgentTest, id.String(),
			map[string]any{"kind": a.Kind, "name": a.Name, "ok": res.OK}, audit.ClientIP(r))
	}
	respond.Success(w, r, res)
}
