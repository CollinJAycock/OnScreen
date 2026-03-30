package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/respond"
)

// validateWebhookURL checks that a webhook URL is a valid HTTPS/HTTP URL pointing
// to a public (non-private, non-loopback, non-link-local) IP address.
// This prevents SSRF attacks where an attacker registers a webhook pointing at
// internal services (e.g. 127.0.0.1, 169.254.x.x, 10.x.x.x).
//
// NOTE: DNS rebinding risk — the hostname is resolved here at validation time, but
// DNS records can change between validation and actual delivery. A production
// hardening step would be to use a custom http.Transport with a DialContext hook
// that re-validates resolved IPs at connection time, rejecting private/loopback
// addresses before the TCP handshake completes.
func validateWebhookURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return errors.New("invalid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("URL scheme must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("URL must include a hostname")
	}

	// Resolve the hostname to IP addresses.
	ips, err := net.LookupHost(host)
	if err != nil {
		return errors.New("cannot resolve hostname")
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return errors.New("URL must not point to a private or loopback address")
		}
	}
	return nil
}

// WebhookEndpoint is the domain model for a webhook endpoint.
type WebhookEndpoint struct {
	ID      uuid.UUID `json:"id"`
	URL     string    `json:"url"`
	Events  []string  `json:"events"`
	Enabled bool      `json:"enabled"`
}

// WebhookService defines the domain interface for webhook management.
type WebhookService interface {
	List(ctx context.Context) ([]WebhookEndpoint, error)
	Get(ctx context.Context, id uuid.UUID) (*WebhookEndpoint, error)
	Create(ctx context.Context, url string, secret string, events []string) (*WebhookEndpoint, error)
	Update(ctx context.Context, id uuid.UUID, url, secret string, events []string, enabled bool) (*WebhookEndpoint, error)
	Delete(ctx context.Context, id uuid.UUID) error
	SendTest(ctx context.Context, id uuid.UUID) error
}

var ErrWebhookNotFound = errors.New("webhook not found")

// WebhookHandler handles /api/v1/webhooks.
type WebhookHandler struct {
	svc    WebhookService
	logger *slog.Logger
}

// NewWebhookHandler creates a WebhookHandler.
func NewWebhookHandler(svc WebhookService, logger *slog.Logger) *WebhookHandler {
	return &WebhookHandler{svc: svc, logger: logger}
}

// List handles GET /api/v1/webhooks.
func (h *WebhookHandler) List(w http.ResponseWriter, r *http.Request) {
	endpoints, err := h.svc.List(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list webhooks", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.List(w, r, endpoints, int64(len(endpoints)), "")
}

// Create handles POST /api/v1/webhooks.
func (h *WebhookHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string   `json:"url"`
		Secret string   `json:"secret"`
		Events []string `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if body.URL == "" {
		respond.ValidationError(w, r, "url is required")
		return
	}
	if err := validateWebhookURL(body.URL); err != nil {
		respond.ValidationError(w, r, err.Error())
		return
	}
	if len(body.Events) == 0 {
		respond.ValidationError(w, r, "at least one event is required")
		return
	}

	ep, err := h.svc.Create(r.Context(), body.URL, body.Secret, body.Events)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create webhook", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Created(w, r, ep)
}

// Update handles PATCH /api/v1/webhooks/:id.
func (h *WebhookHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid webhook id")
		return
	}

	var body struct {
		URL     string   `json:"url"`
		Secret  string   `json:"secret"`
		Events  []string `json:"events"`
		Enabled *bool    `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}

	if body.URL != "" {
		if err := validateWebhookURL(body.URL); err != nil {
			respond.ValidationError(w, r, err.Error())
			return
		}
	}

	// For PATCH semantics, always fetch the existing record so we can fall back
	// to current values for any omitted fields (URL, Events, Enabled).
	existing, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrWebhookNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get webhook for patch", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	var enabled bool
	if body.Enabled != nil {
		enabled = *body.Enabled
	} else {
		enabled = existing.Enabled
	}
	if body.URL == "" {
		body.URL = existing.URL
	}
	if body.Events == nil {
		body.Events = existing.Events
	}

	ep, err := h.svc.Update(r.Context(), id, body.URL, body.Secret, body.Events, enabled)
	if err != nil {
		if errors.Is(err, ErrWebhookNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "update webhook", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, ep)
}

// Delete handles DELETE /api/v1/webhooks/:id.
func (h *WebhookHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid webhook id")
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		if errors.Is(err, ErrWebhookNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "delete webhook", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// Test handles POST /api/v1/webhooks/:id/test — sends a test payload.
func (h *WebhookHandler) Test(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid webhook id")
		return
	}
	if err := h.svc.SendTest(r.Context(), id); err != nil {
		if errors.Is(err, ErrWebhookNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.WarnContext(r.Context(), "webhook test failed", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}
