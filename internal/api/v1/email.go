package v1

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/email"
)

// EmailHandler handles email-related API endpoints.
type EmailHandler struct {
	sender *email.Sender // nil if SMTP not configured
	logger *slog.Logger
}

// NewEmailHandler creates an EmailHandler. sender may be nil.
func NewEmailHandler(sender *email.Sender, logger *slog.Logger) *EmailHandler {
	return &EmailHandler{sender: sender, logger: logger}
}

// Enabled handles GET /api/v1/email/enabled.
func (h *EmailHandler) Enabled(w http.ResponseWriter, r *http.Request) {
	respond.Success(w, r, map[string]bool{"enabled": h.sender != nil})
}

// SendTest handles POST /api/v1/email/test — sends a test email (admin only).
func (h *EmailHandler) SendTest(w http.ResponseWriter, r *http.Request) {
	if h.sender == nil {
		respond.BadRequest(w, r, "SMTP is not configured")
		return
	}

	var body struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.To == "" {
		respond.BadRequest(w, r, "\"to\" email address is required")
		return
	}

	subject, htmlBody := email.TestEmail()
	if err := h.sender.Send([]string{body.To}, subject, htmlBody); err != nil {
		h.logger.ErrorContext(r.Context(), "test email failed", "to", body.To, "err", err)
		respond.BadRequest(w, r, "Failed to send: "+err.Error())
		return
	}

	h.logger.InfoContext(r.Context(), "test email sent", "to", body.To)
	respond.Success(w, r, map[string]string{"message": "Test email sent"})
}
