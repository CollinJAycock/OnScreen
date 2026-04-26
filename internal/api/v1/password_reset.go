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

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/email"
)

// PasswordResetDB is the database interface for the password reset flow.
type PasswordResetDB interface {
	GetUserByEmail(ctx context.Context, email *string) (PRUser, error)
	CreateResetToken(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error
	GetResetToken(ctx context.Context, tokenHash string) (PRToken, error)
	MarkResetTokenUsed(ctx context.Context, id uuid.UUID) error
	UpdatePassword(ctx context.Context, userID uuid.UUID, passwordHash string) error
	// BumpSessionEpoch + DeleteSessionsForUser together revoke all
	// outstanding credentials for the user — see ResetPassword for why
	// we call them after a successful password update.
	BumpSessionEpoch(ctx context.Context, userID uuid.UUID) error
	DeleteSessionsForUser(ctx context.Context, userID uuid.UUID) error
}

// PRUser is the minimal user info needed for password reset.
type PRUser struct {
	ID       uuid.UUID
	Username string
	Email    *string
}

// PRToken represents a password reset token row.
type PRToken struct {
	ID     uuid.UUID
	UserID uuid.UUID
}

// PasswordResetHandler handles forgot password / reset password flows.
type PasswordResetHandler struct {
	db      PasswordResetDB
	sender  *email.Sender // always non-nil; live SMTP state via sender.Enabled(ctx)
	baseURL string
	logger  *slog.Logger
}

// NewPasswordResetHandler creates a PasswordResetHandler.
func NewPasswordResetHandler(db PasswordResetDB, sender *email.Sender, baseURL string, logger *slog.Logger) *PasswordResetHandler {
	return &PasswordResetHandler{db: db, sender: sender, baseURL: baseURL, logger: logger}
}

// Enabled returns whether the forgot password flow is available.
func (h *PasswordResetHandler) Enabled(w http.ResponseWriter, r *http.Request) {
	respond.Success(w, r, map[string]bool{"enabled": h.sender.Enabled(r.Context())})
}

// ForgotPassword handles POST /api/v1/auth/forgot-password.
// Sends a password reset email if the email exists. Always returns 200
// to prevent email enumeration.
func (h *PasswordResetHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	if !h.sender.Enabled(r.Context()) {
		respond.BadRequest(w, r, "Email is not configured on this server")
		return
	}

	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		respond.BadRequest(w, r, "email is required")
		return
	}

	// Always respond success to prevent email enumeration.
	defer respond.Success(w, r, map[string]string{"message": "If an account with that email exists, a password reset link has been sent."})

	user, err := h.db.GetUserByEmail(r.Context(), &body.Email)
	if err != nil {
		return // user not found — silently succeed
	}

	// Generate a secure random token.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: generate token", "err", err)
		return
	}
	rawToken := hex.EncodeToString(tokenBytes)

	// Store the hash (not the raw token) in the DB.
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])

	if err := h.db.CreateResetToken(r.Context(), user.ID, tokenHash, time.Now().Add(time.Hour)); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: store token", "err", err)
		return
	}

	// Send the email with the raw token (user clicks link, we hash and look up).
	resetURL := h.baseURL + "/reset-password?token=" + rawToken
	subject, htmlBody := email.PasswordResetEmail(user.Username, resetURL)
	if err := h.sender.Send(r.Context(), []string{body.Email}, subject, htmlBody); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: send email", "to", body.Email, "err", err)
	}
}

// ResetPassword handles POST /api/v1/auth/reset-password.
// Validates the token and sets the new password.
func (h *PasswordResetHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if body.Token == "" || body.Password == "" {
		respond.BadRequest(w, r, "token and password are required")
		return
	}
	if err := ValidatePassword(body.Password); err != nil {
		respond.BadRequest(w, r, err.Error())
		return
	}

	// Hash the token to look up in DB.
	hash := sha256.Sum256([]byte(body.Token))
	tokenHash := hex.EncodeToString(hash[:])

	token, err := h.db.GetResetToken(r.Context(), tokenHash)
	if err != nil {
		respond.BadRequest(w, r, "Invalid or expired reset link")
		return
	}

	// Hash the new password with bcrypt.
	pwHash, err := bcrypt.GenerateFromPassword([]byte(body.Password), 12)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: hash password", "err", err)
		respond.InternalError(w, r)
		return
	}

	if err := h.db.UpdatePassword(r.Context(), token.UserID, string(pwHash)); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: update password", "err", err)
		respond.InternalError(w, r)
		return
	}

	// Cut every existing credential for the user. The whole point of
	// "forgot password" is recovery from compromise — leaving the old
	// PASETO access tokens (1h TTL) and refresh tokens (30d) live would
	// hand the attacker a continued session even after the legitimate
	// owner reset. Bump the epoch (revokes access tokens) AND wipe the
	// sessions table (revokes the refresh path).
	if err := h.db.BumpSessionEpoch(r.Context(), token.UserID); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: bump session epoch", "err", err)
		respond.InternalError(w, r)
		return
	}
	if err := h.db.DeleteSessionsForUser(r.Context(), token.UserID); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: delete sessions", "err", err)
		respond.InternalError(w, r)
		return
	}

	if err := h.db.MarkResetTokenUsed(r.Context(), token.ID); err != nil {
		h.logger.ErrorContext(r.Context(), "password reset: mark used", "err", err)
	}

	respond.Success(w, r, map[string]string{"message": "Password has been reset. You can now sign in."})
}
