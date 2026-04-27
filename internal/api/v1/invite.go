package v1

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/email"
)

// InviteDB is the database interface for the invite flow.
type InviteDB interface {
	CreateInviteToken(ctx context.Context, createdBy uuid.UUID, tokenHash string, email *string, expiresAt time.Time) (uuid.UUID, error)
	GetInviteToken(ctx context.Context, tokenHash string) (InviteTokenRow, error)
	MarkInviteTokenUsed(ctx context.Context, id uuid.UUID, usedBy uuid.UUID) error
	ListInviteTokens(ctx context.Context) ([]InviteTokenSummaryRow, error)
	DeleteInviteToken(ctx context.Context, id uuid.UUID) error
	CreateUser(ctx context.Context, username string, email *string, passwordHash string) (uuid.UUID, error)
	// GrantAutoLibrariesToUser inserts library_access rows for every
	// library flagged auto_grant_new_users. Called after CreateUser
	// so accepted invites default into the admin-chosen library set.
	GrantAutoLibrariesToUser(ctx context.Context, userID uuid.UUID) error
}

// InviteTokenRow is the data returned when looking up an invite token.
type InviteTokenRow struct {
	ID        uuid.UUID
	CreatedBy uuid.UUID
	Email     *string
}

// InviteTokenSummaryRow is a summary row for listing invites.
type InviteTokenSummaryRow struct {
	ID        uuid.UUID
	CreatedBy uuid.UUID
	Email     *string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// InviteHandler handles user invitation endpoints.
type InviteHandler struct {
	db      InviteDB
	sender  *email.Sender // always non-nil; live SMTP state via sender.Enabled(ctx)
	baseURL string
	logger  *slog.Logger
}

// NewInviteHandler creates an InviteHandler.
func NewInviteHandler(db InviteDB, sender *email.Sender, baseURL string, logger *slog.Logger) *InviteHandler {
	return &InviteHandler{db: db, sender: sender, baseURL: baseURL, logger: logger}
}

// Create handles POST /api/v1/invites (admin only).
// Generates a random invite token, stores the hash, optionally sends an email,
// and returns the invite URL.
func (h *InviteHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.BadRequest(w, r, "unauthorized")
		return
	}

	var body struct {
		Email *string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		// Body is optional — email may be nil.
		body.Email = nil
	}

	// Generate a secure random token.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		h.logger.ErrorContext(r.Context(), "invite: generate token", "err", err)
		respond.InternalError(w, r)
		return
	}
	rawToken := hex.EncodeToString(tokenBytes)

	// Store the SHA-256 hash in the DB (never the raw token).
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	expiresAt := time.Now().Add(7 * 24 * time.Hour)

	id, err := h.db.CreateInviteToken(r.Context(), claims.UserID, tokenHash, body.Email, expiresAt)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "invite: store token", "err", err)
		respond.InternalError(w, r)
		return
	}

	inviteURL := h.baseURL + "/invite?token=" + rawToken

	// Send email if SMTP is configured and an email address was provided.
	if body.Email != nil && *body.Email != "" && h.sender.Enabled(r.Context()) {
		subject, htmlBody := email.InviteEmail(claims.Username, inviteURL)
		if err := h.sender.Send(r.Context(), []string{*body.Email}, subject, htmlBody); err != nil {
			h.logger.ErrorContext(r.Context(), "invite: send email", "to", *body.Email, "err", err)
			// Non-fatal — the admin can still share the URL manually.
		}
	}

	respond.Success(w, r, map[string]string{
		"id":         id.String(),
		"invite_url": inviteURL,
	})
}

// Accept handles POST /api/v1/invites/accept (public, rate-limited).
// Validates the invite token and creates a new user account.
func (h *InviteHandler) Accept(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if body.Token == "" || body.Username == "" || body.Password == "" {
		respond.BadRequest(w, r, "token, username, and password are required")
		return
	}
	if err := ValidatePassword(body.Password); err != nil {
		respond.BadRequest(w, r, err.Error())
		return
	}

	// Hash the raw token to look up the DB row.
	hash := sha256.Sum256([]byte(body.Token))
	tokenHash := hex.EncodeToString(hash[:])

	token, err := h.db.GetInviteToken(r.Context(), tokenHash)
	if err != nil {
		respond.BadRequest(w, r, "Invalid or expired invite link")
		return
	}

	// Hash the password with bcrypt.
	pwHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), 12)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "invite: hash password", "err", err)
		respond.InternalError(w, r)
		return
	}

	userID, err := h.db.CreateUser(r.Context(), body.Username, token.Email, string(pwHash))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "invite: create user", "err", err)
		respond.BadRequest(w, r, "Could not create account — username may already be taken")
		return
	}

	if err := h.db.GrantAutoLibrariesToUser(r.Context(), userID); err != nil {
		// Non-fatal — user can still see public libraries; admin can grant manually.
		h.logger.WarnContext(r.Context(), "invite: auto-grant libraries", "user_id", userID, "err", err)
	}

	if err := h.db.MarkInviteTokenUsed(r.Context(), token.ID, userID); err != nil {
		h.logger.ErrorContext(r.Context(), "invite: mark used", "err", err)
	}

	respond.Success(w, r, map[string]string{"message": "Account created. You can now sign in."})
}

// List handles GET /api/v1/invites (admin only).
func (h *InviteHandler) List(w http.ResponseWriter, r *http.Request) {
	tokens, err := h.db.ListInviteTokens(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "invite: list", "err", err)
		respond.InternalError(w, r)
		return
	}

	type inviteJSON struct {
		ID        string  `json:"id"`
		Email     *string `json:"email"`
		ExpiresAt string  `json:"expires_at"`
		UsedAt    *string `json:"used_at"`
		CreatedAt string  `json:"created_at"`
	}

	out := make([]inviteJSON, 0, len(tokens))
	for _, t := range tokens {
		item := inviteJSON{
			ID:        t.ID.String(),
			Email:     t.Email,
			ExpiresAt: t.ExpiresAt.Format(time.RFC3339),
			CreatedAt: t.CreatedAt.Format(time.RFC3339),
		}
		if t.UsedAt != nil {
			s := t.UsedAt.Format(time.RFC3339)
			item.UsedAt = &s
		}
		out = append(out, item)
	}

	respond.Success(w, r, out)
}

// Delete handles DELETE /api/v1/invites/{id} (admin only).
func (h *InviteHandler) Delete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		respond.BadRequest(w, r, "invalid invite id")
		return
	}

	if err := h.db.DeleteInviteToken(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "invite: delete", "err", err)
		respond.InternalError(w, r)
		return
	}

	respond.Success(w, r, map[string]string{"message": "Invite deleted"})
}
