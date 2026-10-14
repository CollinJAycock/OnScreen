package v1

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/digest"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// DigestDB is the query subset the digest endpoints use. Satisfied by
// *gen.Queries.
type DigestDB interface {
	GetDigestPrefs(ctx context.Context, userID uuid.UUID) (gen.UserDigestPref, error)
	SetDigestFrequency(ctx context.Context, arg gen.SetDigestFrequencyParams) (gen.UserDigestPref, error)
	UnsubscribeDigest(ctx context.Context, userID uuid.UUID) (int64, error)
	GetDigestRecipient(ctx context.Context, id uuid.UUID) (gen.GetDigestRecipientRow, error)
}

// DigestSettingsStore reads and writes the server-wide digest settings.
// *settings.Service satisfies it.
type DigestSettingsStore interface {
	Digest(ctx context.Context) settings.DigestConfig
	SetDigest(ctx context.Context, cfg settings.DigestConfig) error
}

// DigestTestSender sends the admin's "Send me a test digest now". Satisfied
// by *digest.Service.
type DigestTestSender interface {
	SendTest(ctx context.Context, userID uuid.UUID) (string, error)
}

// SMTPState reports whether SMTP is configured. Satisfied by *email.Sender.
type SMTPState interface {
	Enabled(ctx context.Context) bool
}

// DigestHandler serves the digest email's endpoints: a user's own opt-in, the
// admin settings, the admin's test send, and the sign-in-free unsubscribe
// link.
type DigestHandler struct {
	db       DigestDB
	settings DigestSettingsStore
	sender   DigestTestSender
	smtp     SMTPState
	signer   *digest.Signer
	baseURL  func(ctx context.Context) string // the general settings' Base URL ("" = unset)
	audit    *audit.Logger
	logger   *slog.Logger
}

// NewDigestHandler creates a DigestHandler. baseURL returns the configured
// public URL ("" when the admin hasn't set one).
func NewDigestHandler(db DigestDB, store DigestSettingsStore, sender DigestTestSender, smtp SMTPState,
	signer *digest.Signer, baseURL func(ctx context.Context) string, logger *slog.Logger) *DigestHandler {
	return &DigestHandler{db: db, settings: store, sender: sender, smtp: smtp, signer: signer, baseURL: baseURL, logger: logger}
}

// WithAudit records admin settings changes.
func (h *DigestHandler) WithAudit(a *audit.Logger) *DigestHandler {
	h.audit = a
	return h
}

// ── User opt-in ──────────────────────────────────────────────────────────────

type myDigestDTO struct {
	Frequency string  `json:"frequency"` // off | weekly | monthly
	LastSent  *string `json:"last_sent_at,omitempty"`
	// HasEmail is false when the account has no email address: the digest
	// can be chosen but won't be sent until one is added.
	HasEmail bool `json:"has_email"`
	// Available is false while the admin has the digest off or SMTP isn't
	// configured; the page says so.
	Available bool `json:"available"`
}

func (h *DigestHandler) myDigest(ctx context.Context, userID uuid.UUID) (myDigestDTO, error) {
	out := myDigestDTO{Frequency: digest.FrequencyOff}
	prefs, err := h.db.GetDigestPrefs(ctx, userID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return out, err
	default:
		out.Frequency = prefs.Frequency
		if prefs.LastSentAt.Valid {
			s := prefs.LastSentAt.Time.UTC().Format(time.RFC3339)
			out.LastSent = &s
		}
	}
	rec, err := h.db.GetDigestRecipient(ctx, userID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	out.HasEmail = strings.TrimSpace(rec.Email) != ""
	out.Available = h.settings.Digest(ctx).Enabled && h.smtp.Enabled(ctx)
	return out, nil
}

// GetMine handles GET /api/v1/users/me/digest.
func (h *DigestHandler) GetMine(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	out, err := h.myDigest(r.Context(), claims.UserID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "digest: get prefs", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, out)
}

// SetMine handles PUT /api/v1/users/me/digest {"frequency":"off|weekly|monthly"}.
func (h *DigestHandler) SetMine(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	var body struct {
		Frequency string `json:"frequency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !digest.ValidFrequency(body.Frequency) {
		respond.BadRequest(w, r, "frequency must be off, weekly or monthly")
		return
	}
	if _, err := h.db.SetDigestFrequency(r.Context(), gen.SetDigestFrequencyParams{
		UserID: claims.UserID, Frequency: body.Frequency,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "digest: set prefs", "err", err)
		respond.InternalError(w, r)
		return
	}
	out, err := h.myDigest(r.Context(), claims.UserID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "digest: get prefs", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, out)
}

// ── Admin settings ───────────────────────────────────────────────────────────

type digestSettingsDTO struct {
	Enabled                 bool `json:"enabled"`
	Weekday                 int  `json:"weekday"` // 0 = Sunday .. 6 = Saturday, server time
	Hour                    int  `json:"hour"`    // 0-23, server time
	IncludeContinueWatching bool `json:"include_continue_watching"`
	IncludeStats            bool `json:"include_stats"`

	// Read-only context for the settings page (ignored on PUT).
	SMTPConfigured bool   `json:"smtp_configured"`
	BaseURL        string `json:"base_url"`        // Settings ▸ General; "" = not set
	ServerTimezone string `json:"server_timezone"` // the zone weekday/hour are in
}

func (h *DigestHandler) settingsDTO(ctx context.Context) digestSettingsDTO {
	cfg := h.settings.Digest(ctx)
	base := ""
	if h.baseURL != nil {
		base = h.baseURL(ctx)
	}
	zone, _ := time.Now().Zone()
	if name := time.Local.String(); name != "Local" {
		zone = name
	}
	return digestSettingsDTO{
		Enabled:                 cfg.Enabled,
		Weekday:                 cfg.Weekday,
		Hour:                    cfg.Hour,
		IncludeContinueWatching: cfg.IncludeContinueWatching,
		IncludeStats:            cfg.IncludeStats,
		SMTPConfigured:          h.smtp.Enabled(ctx),
		BaseURL:                 base,
		ServerTimezone:          zone,
	}
}

// GetSettings handles GET /api/v1/settings/digest (admin).
func (h *DigestHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	respond.Success(w, r, h.settingsDTO(r.Context()))
}

// UpdateSettings handles PUT /api/v1/settings/digest (admin).
func (h *DigestHandler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	var dto digestSettingsDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	cfg := settings.DigestConfig{
		Enabled:                 dto.Enabled,
		Weekday:                 dto.Weekday,
		Hour:                    dto.Hour,
		IncludeContinueWatching: dto.IncludeContinueWatching,
		IncludeStats:            dto.IncludeStats,
	}
	ctx := r.Context()
	if err := h.settings.SetDigest(ctx, cfg); err != nil {
		if errors.Is(err, settings.ErrInvalidDigestConfig) {
			respond.BadRequest(w, r, err.Error())
			return
		}
		h.logger.ErrorContext(ctx, "update digest config", "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(ctx); claims != nil {
			h.audit.Log(ctx, &claims.UserID, audit.ActionSettingsUpdate, "", map[string]any{
				"digest_enabled": cfg.Enabled,
				"digest_weekday": cfg.Weekday,
				"digest_hour":    cfg.Hour,
			}, audit.ClientIP(r))
		}
	}
	respond.Success(w, r, h.settingsDTO(ctx))
}

// SendTest handles POST /api/v1/settings/digest/test (admin): sends the
// calling admin their own digest of the current period now.
func (h *DigestHandler) SendTest(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	to, err := h.sender.SendTest(r.Context(), claims.UserID)
	switch {
	case errors.Is(err, digest.ErrSMTPOff):
		respond.BadRequest(w, r, "SMTP is not configured")
		return
	case errors.Is(err, digest.ErrNoEmail):
		respond.BadRequest(w, r, "Your account has no email address")
		return
	case err != nil:
		h.logger.ErrorContext(r.Context(), "digest: test send", "err", err)
		respond.BadRequest(w, r, "Failed to send: "+err.Error())
		return
	}
	respond.Success(w, r, map[string]string{"message": "Test digest sent to " + to})
}

// ── Unsubscribe (no sign-in) ─────────────────────────────────────────────────

// Unsubscribe handles GET and POST /api/v1/digest/unsubscribe?token=….
//
// GET shows a confirmation page with a button; only POST turns the digest
// off. Mail scanners and link previewers fetch every link in a message, so a
// GET that unsubscribed would turn people's digests off behind their backs.
// POST is also what a mail client sends for the List-Unsubscribe-Post
// one-click header (RFC 8058). The token is the HMAC-signed user id from the
// email; it can do nothing but this.
func (h *DigestHandler) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	userID, ok := h.signer.Verify(token)
	if !ok {
		unsubscribePage(w, http.StatusBadRequest, "This unsubscribe link isn't valid.",
			"It may have been cut short by your mail app. You can turn the digest off on your Account page instead.", "")
		return
	}
	if r.Method == http.MethodGet {
		unsubscribePage(w, http.StatusOK, "Unsubscribe from the OnScreen digest?",
			"You'll stop getting the digest email. You can turn it back on any time on your Account page.",
			"/api/v1/digest/unsubscribe?token="+token)
		return
	}
	if _, err := h.db.UnsubscribeDigest(r.Context(), userID); err != nil {
		h.logger.ErrorContext(r.Context(), "digest: unsubscribe", "err", err)
		unsubscribePage(w, http.StatusInternalServerError, "Something went wrong.", "Please try again in a moment.", "")
		return
	}
	h.logger.InfoContext(r.Context(), "digest: unsubscribed", "user_id", userID)
	unsubscribePage(w, http.StatusOK, "You're unsubscribed.",
		"You won't get the OnScreen digest any more. You can turn it back on any time on your Account page.", "")
}

// unsubscribePage writes a small standalone page. action, when set, renders
// the confirm button (a form POST to it). Every string is escaped.
func unsubscribePage(w http.ResponseWriter, status int, heading, body, action string) {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>OnScreen digest</title></head>`)
	b.WriteString(`<body style="margin:0;background:#07070d;color:#eeeef8;font-family:system-ui,-apple-system,sans-serif"><main style="max-width:420px;margin:12vh auto;padding:32px;background:#0e0e18;border:1px solid rgba(255,255,255,0.06);border-radius:16px">`)
	b.WriteString(`<p style="font-size:20px;font-weight:700;margin:0 0 20px">OnScreen</p>`)
	b.WriteString(`<h1 style="font-size:18px;font-weight:600;margin:0 0 10px">` + e(heading) + `</h1>`)
	b.WriteString(`<p style="font-size:14px;line-height:1.6;color:#8888a0;margin:0">` + e(body) + `</p>`)
	if action != "" {
		b.WriteString(`<form method="post" action="` + e(action) + `" style="margin-top:24px"><button type="submit" style="padding:12px 28px;background:#7c6af7;color:#fff;border:0;border-radius:8px;font-weight:600;font-size:15px;cursor:pointer">Unsubscribe</button></form>`)
	}
	b.WriteString(`</main></body></html>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(b.String()))
}
