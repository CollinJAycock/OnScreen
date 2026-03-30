package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// Sentinel errors returned by UserService.
var (
	ErrBadPIN             = errors.New("PIN must be exactly 4 digits")
	ErrInvalidCredentials = errors.New("invalid credentials")
)

// SwitchableUser is a public-safe user representation for the user picker.
type SwitchableUser struct {
	ID       uuid.UUID `json:"id"`
	Username string    `json:"username"`
	IsAdmin  bool      `json:"is_admin"`
	HasPin   bool      `json:"has_pin"`
}

// PINSwitchResult is returned by VerifyPIN on success.
type PINSwitchResult struct {
	UserID   uuid.UUID
	Username string
	IsAdmin  bool
}

// UserService manages user profile operations.
type UserService interface {
	SetPIN(ctx context.Context, userID uuid.UUID, rawPIN, password string) error
	ClearPIN(ctx context.Context, userID uuid.UUID, password string) error
	ListSwitchable(ctx context.Context) ([]SwitchableUser, error)
	VerifyPIN(ctx context.Context, userID uuid.UUID, rawPIN string) (*PINSwitchResult, error)
}

// UserDB defines the database interface for user admin operations.
type UserDB interface {
	ListUsers(ctx context.Context) ([]gen.ListUsersRow, error)
	DeleteUser(ctx context.Context, id uuid.UUID) error
	SetUserAdmin(ctx context.Context, arg gen.SetUserAdminParams) error
	CountAdmins(ctx context.Context) (int64, error)
	UpdateUserPassword(ctx context.Context, arg gen.UpdateUserPasswordParams) error
}

// UserHandler handles /api/v1/users endpoints.
type UserHandler struct {
	users  UserService
	db     UserDB
	tokens *auth.TokenMaker
	logger *slog.Logger
	audit  *audit.Logger
}

// WithAudit attaches an audit logger. Returns the handler for chaining.
func (h *UserHandler) WithAudit(a *audit.Logger) *UserHandler {
	h.audit = a
	return h
}

// NewUserHandler creates a UserHandler.
func NewUserHandler(users UserService) *UserHandler {
	return &UserHandler{users: users}
}

// WithDB attaches the admin DB queries. Returns the handler for chaining.
func (h *UserHandler) WithDB(db UserDB) *UserHandler {
	h.db = db
	return h
}

// WithTokenMaker attaches the token maker for PIN switch.
func (h *UserHandler) WithTokenMaker(tokens *auth.TokenMaker, logger *slog.Logger) *UserHandler {
	h.tokens = tokens
	h.logger = logger
	return h
}

// ── PIN management (existing) ─────────────────────────────────────────────────

// SetPIN handles PUT /api/v1/users/me/pin.
// Body: {"pin":"1234","password":"currentPassword"}
func (h *UserHandler) SetPIN(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}

	var body struct {
		PIN      string `json:"pin"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}

	if err := h.users.SetPIN(r.Context(), claims.UserID, body.PIN, body.Password); err != nil {
		switch {
		case errors.Is(err, ErrBadPIN):
			respond.BadRequest(w, r, err.Error())
		case errors.Is(err, ErrInvalidCredentials):
			respond.Forbidden(w, r)
		default:
			respond.InternalError(w, r)
		}
		return
	}
	respond.NoContent(w)
}

// ClearPIN handles DELETE /api/v1/users/me/pin.
// Body: {"password":"currentPassword"}
func (h *UserHandler) ClearPIN(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}

	if err := h.users.ClearPIN(r.Context(), claims.UserID, body.Password); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			respond.Forbidden(w, r)
		} else {
			respond.InternalError(w, r)
		}
		return
	}
	respond.NoContent(w)
}

// ── Admin user management ─────────────────────────────────────────────────────

type userListEntry struct {
	ID        uuid.UUID `json:"id"`
	Username  string    `json:"username"`
	IsAdmin   bool      `json:"is_admin"`
	CreatedAt time.Time `json:"created_at"`
}

func tsToTime(ts pgtype.Timestamptz) time.Time {
	if ts.Valid {
		return ts.Time
	}
	return time.Time{}
}

// ListUsers handles GET /api/v1/users — returns all users (admin only).
func (h *UserHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}

	rows, err := h.db.ListUsers(r.Context())
	if err != nil {
		respond.InternalError(w, r)
		return
	}

	users := make([]userListEntry, len(rows))
	for i, row := range rows {
		users[i] = userListEntry{
			ID:        row.ID,
			Username:  row.Username,
			IsAdmin:   row.IsAdmin,
			CreatedAt: tsToTime(row.CreatedAt),
		}
	}
	respond.List(w, r, users, int64(len(users)), "")
}

// DeleteUser handles DELETE /api/v1/users/{id} — deletes a user (admin only).
// Prevents self-deletion.
func (h *UserHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}

	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}

	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid user id")
		return
	}

	if targetID == claims.UserID {
		respond.BadRequest(w, r, "cannot delete yourself")
		return
	}

	if err := h.db.DeleteUser(r.Context(), targetID); err != nil {
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionUserDelete, targetID.String(), nil, audit.ClientIP(r))
	}
	respond.NoContent(w)
}

// SetAdmin handles PATCH /api/v1/users/{id} — sets admin status (admin only).
// Prevents demoting the last admin.
func (h *UserHandler) SetAdmin(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}

	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}

	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid user id")
		return
	}

	var body struct {
		IsAdmin *bool `json:"is_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IsAdmin == nil {
		respond.BadRequest(w, r, "is_admin field is required")
		return
	}

	// Prevent demoting the last admin.
	if !*body.IsAdmin {
		count, err := h.db.CountAdmins(r.Context())
		if err != nil {
			respond.InternalError(w, r)
			return
		}
		if count <= 1 {
			respond.BadRequest(w, r, "cannot remove the last admin")
			return
		}
	}

	if err := h.db.SetUserAdmin(r.Context(), gen.SetUserAdminParams{
		ID:      targetID,
		IsAdmin: *body.IsAdmin,
	}); err != nil {
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionUserRoleChange, targetID.String(), map[string]any{"is_admin": *body.IsAdmin}, audit.ClientIP(r))
	}
	respond.NoContent(w)
}

// ResetPassword handles PUT /api/v1/users/{id}/password — admin resets a user's password.
func (h *UserHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}

	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid user id")
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if len(body.Password) < 8 {
		respond.BadRequest(w, r, "password must be at least 8 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), 12)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	hashStr := string(hash)
	if err := h.db.UpdateUserPassword(r.Context(), gen.UpdateUserPasswordParams{
		ID:           targetID,
		PasswordHash: &hashStr,
	}); err != nil {
		respond.InternalError(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if h.audit != nil && claims != nil {
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionPasswordReset, targetID.String(), nil, audit.ClientIP(r))
	}
	respond.NoContent(w)
}

// ── PIN-based user switching ──────────────────────────────────────────────────

// ListSwitchable handles GET /api/v1/users/switchable.
// Returns all users with id, username, is_admin, has_pin (never exposes the hash).
func (h *UserHandler) ListSwitchable(w http.ResponseWriter, r *http.Request) {
	users, err := h.users.ListSwitchable(r.Context())
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, users)
}

// PINSwitch handles POST /api/v1/auth/pin-switch.
// Body: {"user_id":"...","pin":"1234"}
// Verifies the PIN, then issues a new access token for the target user.
func (h *UserHandler) PINSwitch(w http.ResponseWriter, r *http.Request) {
	if h.tokens == nil {
		respond.InternalError(w, r)
		return
	}

	var body struct {
		UserID string `json:"user_id"`
		PIN    string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}

	targetID, err := uuid.Parse(body.UserID)
	if err != nil {
		respond.BadRequest(w, r, "invalid user_id")
		return
	}

	if body.PIN == "" {
		respond.BadRequest(w, r, "pin is required")
		return
	}

	result, err := h.users.VerifyPIN(r.Context(), targetID, body.PIN)
	if err != nil {
		if errors.Is(err, ErrBadPIN) || errors.Is(err, ErrInvalidCredentials) {
			respond.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "invalid PIN")
			return
		}
		if h.logger != nil {
			h.logger.ErrorContext(r.Context(), "pin switch", "err", err)
		}
		respond.InternalError(w, r)
		return
	}

	// Issue a new access token for the target user.
	accessToken, err := h.tokens.IssueAccessToken(auth.Claims{
		UserID:   result.UserID,
		Username: result.Username,
		IsAdmin:  result.IsAdmin,
	})
	if err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(r.Context(), "pin switch: issue access token", "err", err)
		}
		respond.InternalError(w, r)
		return
	}

	tokenPair := &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: "",
		ExpiresAt:    time.Now().Add(auth.AccessTokenTTL),
		UserID:       result.UserID,
		Username:     result.Username,
		IsAdmin:      result.IsAdmin,
	}
	setAuthCookies(w, r, tokenPair)
	respond.Success(w, r, tokenPair)
}
