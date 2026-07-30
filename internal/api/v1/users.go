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
	UserID           uuid.UUID
	Username         string
	IsAdmin          bool
	MaxContentRating string
	SessionEpoch     int64
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
	BumpSessionEpoch(ctx context.Context, id uuid.UUID) error
	DeleteSessionsForUser(ctx context.Context, userID uuid.UUID) error
	CountAdmins(ctx context.Context) (int64, error)
	UpdateUserPassword(ctx context.Context, arg gen.UpdateUserPasswordParams) error
	ListManagedProfiles(ctx context.Context, parentUserID pgtype.UUID) ([]gen.ListManagedProfilesRow, error)
	ListAllManagedProfiles(ctx context.Context) ([]gen.ListAllManagedProfilesRow, error)
	CreateManagedProfile(ctx context.Context, arg gen.CreateManagedProfileParams) (gen.CreateManagedProfileRow, error)
	UpdateManagedProfile(ctx context.Context, arg gen.UpdateManagedProfileParams) (gen.UpdateManagedProfileRow, error)
	UpdateManagedProfileAdmin(ctx context.Context, arg gen.UpdateManagedProfileAdminParams) (gen.UpdateManagedProfileAdminRow, error)
	DeleteManagedProfile(ctx context.Context, arg gen.DeleteManagedProfileParams) error
	DeleteManagedProfileAdmin(ctx context.Context, id uuid.UUID) error
	GetUserPreferences(ctx context.Context, id uuid.UUID) (gen.GetUserPreferencesRow, error)
	UpdateUserPreferences(ctx context.Context, arg gen.UpdateUserPreferencesParams) error
	UpdateUserHubLayout(ctx context.Context, arg gen.UpdateUserHubLayoutParams) error
	UpdateUserQualityProfile(ctx context.Context, arg gen.UpdateUserQualityProfileParams) error
	UpdateUserContentRating(ctx context.Context, arg gen.UpdateUserContentRatingParams) error
	UpdateUserStreamCaps(ctx context.Context, arg gen.UpdateUserStreamCapsParams) error
	GetUserStreamCaps(ctx context.Context, id uuid.UUID) (gen.GetUserStreamCapsRow, error)
	SetProfileInheritLibraryAccess(ctx context.Context, arg gen.SetProfileInheritLibraryAccessParams) (int64, error)
}

// UserLibraryAccessService is the subset of the library service needed to
// read/write per-user library grants. Kept small so the user handler doesn't
// depend on the whole library service API surface. The adapter is responsible
// for looking up the target user's is_admin flag to decide whether to report
// every library as enabled.
type UserLibraryAccessService interface {
	ListAccessForUser(ctx context.Context, userID uuid.UUID) ([]UserLibraryAccessEntry, error)
	ReplaceAccessForUser(ctx context.Context, userID uuid.UUID, libraryIDs []uuid.UUID) error
}

// UserLibraryAccessEntry is a flat shape suitable for returning over JSON
// without pulling the full library domain type into this package.
type UserLibraryAccessEntry struct {
	LibraryID uuid.UUID `json:"library_id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Enabled   bool      `json:"enabled"`
}

// SegmentTokenRevoker wipes every outstanding HLS segment token for a
// user. Wired into credential-rotation paths (password reset, admin
// demote) so an active playback can't outlive the access-token
// revocation by up to 4h. Optional — nil means tokens age out via TTL.
type SegmentTokenRevoker interface {
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

// UserHandler handles /api/v1/users endpoints.
type UserHandler struct {
	users     UserService
	db        UserDB
	tokens    *auth.TokenMaker
	segTokens SegmentTokenRevoker
	logger    *slog.Logger
	audit     *audit.Logger
	libAccess UserLibraryAccessService
	throttle  FailureThrottle
	pinPolicy PinSwitchPolicy
}

// PinSwitchPolicy reports whether PIN-based profile switching is enabled
// server-wide. Satisfied by *settings.Service. nil = no gate (allowed),
// which keeps tests and minimal wirings working without a settings service.
type PinSwitchPolicy interface {
	PinSwitchEnabled(ctx context.Context) bool
}

// FailureThrottle is the brute-force counter the PIN-switch path uses to lock
// out repeated bad PINs against a target user. Satisfied by *valkey.RateLimiter;
// nil in tests that don't exercise throttling (the handler no-ops the gate).
type FailureThrottle interface {
	CheckFailures(ctx context.Context, key string, limit int) (bool, error)
	IncrFailure(ctx context.Context, key string, window time.Duration)
	ResetFailures(ctx context.Context, key string)
}

const (
	// pinSwitchMaxFailures / pinSwitchFailWindow bound PIN-switch guessing.
	// A 4-digit PIN has only 10⁴ values and mints a token carrying the
	// TARGET's privileges (including admin), so without a lockout it's
	// brute-forceable in minutes. 5 failures per 15 min per target makes
	// exhausting the space take days while leaving genuine "wrong PIN"
	// retries unaffected.
	pinSwitchMaxFailures = 5
	pinSwitchFailWindow  = 15 * time.Minute
)

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

// WithLibraryAccess attaches the library-access service used by the per-user
// library grant endpoints.
func (h *UserHandler) WithLibraryAccess(svc UserLibraryAccessService) *UserHandler {
	h.libAccess = svc
	return h
}

// WithSegmentTokenRevoker attaches the HLS segment-token revoker so password
// changes and admin demotes also wipe in-flight playback credentials.
func (h *UserHandler) WithSegmentTokenRevoker(r SegmentTokenRevoker) *UserHandler {
	h.segTokens = r
	return h
}

// WithPINThrottle attaches the brute-force throttle used by PINSwitch.
func (h *UserHandler) WithPINThrottle(t FailureThrottle) *UserHandler {
	h.throttle = t
	return h
}

// WithPinSwitchPolicy attaches the server-wide PIN-switch enable toggle.
// nil leaves switching unconditionally allowed.
func (h *UserHandler) WithPinSwitchPolicy(p PinSwitchPolicy) *UserHandler {
	h.pinPolicy = p
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

// DeleteSelf handles DELETE /api/v1/users/me — a user deletes their own
// account. Refuses if the caller is the last admin (deleting them would lock
// everyone out), audits the deletion, and clears the auth cookies. The access
// token dies on the next request regardless: the user row is gone, so the
// epoch check fails closed on ErrUserNotFound.
func (h *UserHandler) DeleteSelf(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	// A 4-digit PIN must not be able to destroy the account it switched into.
	if blockSwitchedSession(w, r, "delete your account") {
		return
	}
	if claims.IsAdmin {
		count, err := h.db.CountAdmins(r.Context())
		if err != nil {
			respond.InternalError(w, r)
			return
		}
		if count <= 1 {
			respond.BadRequest(w, r, "cannot delete the last admin account")
			return
		}
	}
	if err := h.db.DeleteUser(r.Context(), claims.UserID); err != nil {
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionUserDelete, claims.UserID.String(), nil, audit.ClientIP(r))
	}
	clearAuthCookies(w, r)
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
	// Invalidate the target's outstanding credentials. The DB update
	// above takes effect for new tokens via IssueAccessToken; the calls
	// below revoke already-issued tokens (PASETO via session_epoch,
	// refresh tokens via the sessions table) so a demoted admin can't
	// keep using their session for up to an hour while the access token
	// rides out its TTL. Fail the request on error — leaving the role
	// changed but the old tokens live is the worst possible state.
	if err := h.db.BumpSessionEpoch(r.Context(), targetID); err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(r.Context(), "bump session epoch after role change",
				"target_id", targetID, "err", err)
		}
		respond.InternalError(w, r)
		return
	}
	if err := h.db.DeleteSessionsForUser(r.Context(), targetID); err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(r.Context(), "delete sessions after role change",
				"target_id", targetID, "err", err)
		}
		respond.InternalError(w, r)
		return
	}
	// HLS segment tokens live in Valkey with their own 4h TTL and don't
	// participate in session_epoch checks (HLS.js can't carry headers, so
	// the carrier is a query-string capability). Revoke them here too —
	// a demoted admin shouldn't keep streaming for hours.
	if h.segTokens != nil {
		if err := h.segTokens.RevokeAllForUser(r.Context(), targetID); err != nil && h.logger != nil {
			h.logger.WarnContext(r.Context(), "revoke segment tokens after role change",
				"target_id", targetID, "err", err)
		}
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
	if err := ValidatePassword(body.Password); err != nil {
		respond.BadRequest(w, r, err.Error())
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
	// Invalidate every existing session for the target user. The password
	// change is the user's "I'm compromised, kick everyone out" lever — if
	// we leave the existing PASETO access tokens (1h TTL) and refresh
	// tokens (30d) in place, an attacker who already grabbed a session
	// keeps it. Bumping the epoch revokes outstanding access tokens
	// immediately; deleting refresh-token rows revokes the refresh path.
	if err := h.db.BumpSessionEpoch(r.Context(), targetID); err != nil {
		respond.InternalError(w, r)
		return
	}
	if err := h.db.DeleteSessionsForUser(r.Context(), targetID); err != nil {
		respond.InternalError(w, r)
		return
	}
	if h.segTokens != nil {
		if err := h.segTokens.RevokeAllForUser(r.Context(), targetID); err != nil && h.logger != nil {
			h.logger.WarnContext(r.Context(), "revoke segment tokens after admin password reset",
				"target_id", targetID, "err", err)
		}
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

	// Server-wide kill switch. Operators running OnScreen as isolated
	// per-account tenants (rather than a shared household) disable the
	// household profile-picker entirely.
	if h.pinPolicy != nil && !h.pinPolicy.PinSwitchEnabled(r.Context()) {
		respond.Error(w, r, http.StatusForbidden, "PIN_SWITCH_DISABLED",
			"profile switching is disabled on this server")
		return
	}

	// No chaining: a token already minted via PIN-switch cannot initiate
	// another switch. Otherwise profile A could PIN into B, then from B
	// into C, hopping across the whole household from a single login and
	// defeating the per-target brute-force lockout (each hop resets the
	// attacker's vantage point). The legitimate flow always starts from a
	// full credential login, whose token has Switched=false.
	if cur := middleware.ClaimsFromContext(r.Context()); cur != nil && cur.Switched {
		respond.Error(w, r, http.StatusForbidden, "PIN_SWITCH_CHAINED",
			"sign in with your account before switching profiles")
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

	// Brute-force lockout, keyed by the TARGET user. The 4-digit PIN mints a
	// token carrying the target's privileges, so an unthrottled guesser could
	// escalate to admin in minutes; this caps failures per target. CheckFailures
	// only reads — IncrFailure runs on a confirmed bad PIN, ResetFailures clears
	// the counter on success. Mirrors the per-username login throttle.
	failKey := "ratelimit:pinswitch:" + targetID.String()
	if h.throttle != nil {
		allowed, _ := h.throttle.CheckFailures(r.Context(), failKey, pinSwitchMaxFailures)
		if !allowed {
			if h.logger != nil {
				h.logger.WarnContext(r.Context(), "pin-switch throttle hit", "target_id", targetID)
			}
			respond.Error(w, r, http.StatusTooManyRequests, "RATE_LIMITED",
				"too many failed attempts; try again later")
			return
		}
	}

	result, err := h.users.VerifyPIN(r.Context(), targetID, body.PIN)
	if err != nil {
		if errors.Is(err, ErrBadPIN) || errors.Is(err, ErrInvalidCredentials) {
			if h.throttle != nil {
				h.throttle.IncrFailure(r.Context(), failKey, pinSwitchFailWindow)
			}
			respond.Error(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "invalid PIN")
			return
		}
		if h.logger != nil {
			h.logger.ErrorContext(r.Context(), "pin switch", "err", err)
		}
		respond.InternalError(w, r)
		return
	}
	if h.throttle != nil {
		h.throttle.ResetFailures(r.Context(), failKey)
	}

	// A 4-digit PIN must never grant admin. Admins authenticate with full
	// credentials; PIN-switch is for non-admin household profiles only.
	if result.IsAdmin {
		respond.Error(w, r, http.StatusForbidden, "ADMIN_PIN_SWITCH_DISALLOWED",
			"this profile requires full sign-in")
		return
	}

	// Issue a new access token for the target user. Switched=true brands it
	// so it can't be used to PIN-switch again (the no-chaining guard above).
	switchClaims := auth.Claims{
		UserID:           result.UserID,
		Username:         result.Username,
		IsAdmin:          result.IsAdmin,
		MaxContentRating: result.MaxContentRating,
		SessionEpoch:     result.SessionEpoch,
		Switched:         true,
	}
	accessToken, err := h.tokens.IssueAccessToken(switchClaims)
	if err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(r.Context(), "pin switch: issue access token", "err", err)
		}
		respond.InternalError(w, r)
		return
	}
	assetToken, err := h.tokens.IssueAssetToken(switchClaims)
	if err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(r.Context(), "pin switch: issue asset token", "err", err)
		}
		respond.InternalError(w, r)
		return
	}

	tokenPair := &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: "",
		AssetToken:   assetToken,
		ExpiresAt:    time.Now().Add(auth.AccessTokenTTL),
		UserID:       result.UserID,
		Username:     result.Username,
		IsAdmin:      result.IsAdmin,
	}
	setAuthCookies(w, r, tokenPair)
	respond.Success(w, r, tokenPair)
}

// ── Managed profiles ──────────────────────────────────────────────────────────

type profileResponse struct {
	ID               string  `json:"id"`
	Username         string  `json:"username"`
	AvatarURL        *string `json:"avatar_url,omitempty"`
	HasPIN           bool    `json:"has_pin"`
	CreatedAt        string  `json:"created_at"`
	MaxContentRating *string `json:"max_content_rating,omitempty"`
	// InheritLibraryAccess: when true, the profile sees the parent's
	// library grants (the safe default — admins generally create
	// profiles for their household). When false, the profile uses its
	// own library_access rows so admins can narrow per-profile (kid
	// sees Family Movies only, even though parent has 4K Movies too).
	InheritLibraryAccess bool    `json:"inherit_library_access"`
	OwnerID              *string `json:"owner_id,omitempty"`       // admin only
	OwnerUsername        *string `json:"owner_username,omitempty"` // admin only
}

// ListProfiles handles GET /api/v1/profiles.
// Admins receive all profiles across all users including owner metadata.
// Regular users receive only their own profiles.
func (h *UserHandler) ListProfiles(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}

	if claims.IsAdmin {
		rows, err := h.db.ListAllManagedProfiles(r.Context())
		if err != nil {
			respond.InternalError(w, r)
			return
		}
		out := make([]profileResponse, len(rows))
		for i, row := range rows {
			ownerID := row.OwnerID.String()
			out[i] = profileResponse{
				ID:                   row.ID.String(),
				Username:             row.Username,
				AvatarURL:            row.AvatarUrl,
				HasPIN:               row.HasPin == true,
				CreatedAt:            row.CreatedAt.Time.Format(time.RFC3339),
				MaxContentRating:     row.MaxContentRating,
				InheritLibraryAccess: row.InheritLibraryAccess,
				OwnerID:              &ownerID,
				OwnerUsername:        &row.OwnerUsername,
			}
		}
		respond.Success(w, r, out)
		return
	}

	parentPG := pgtype.UUID{Bytes: [16]byte(claims.UserID), Valid: true}
	rows, err := h.db.ListManagedProfiles(r.Context(), parentPG)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	out := make([]profileResponse, len(rows))
	for i, row := range rows {
		out[i] = profileResponse{
			ID:                   row.ID.String(),
			Username:             row.Username,
			AvatarURL:            row.AvatarUrl,
			HasPIN:               row.HasPin == true,
			CreatedAt:            row.CreatedAt.Time.Format(time.RFC3339),
			MaxContentRating:     row.MaxContentRating,
			InheritLibraryAccess: row.InheritLibraryAccess,
		}
	}
	respond.Success(w, r, out)
}

// CreateProfile handles POST /api/v1/profiles.
// Admins may pass owner_id to create a profile under any user.
func (h *UserHandler) CreateProfile(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	var body struct {
		Username  string  `json:"username"`
		AvatarURL *string `json:"avatar_url"`
		PIN       *string `json:"pin"`
		OwnerID   *string `json:"owner_id"` // admin only: create profile under another user
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
		respond.BadRequest(w, r, "username is required")
		return
	}

	// Determine the owning user: admin may specify any user; regular users always own the profile themselves.
	ownerID := claims.UserID
	if body.OwnerID != nil && *body.OwnerID != "" {
		if !claims.IsAdmin {
			respond.Forbidden(w, r)
			return
		}
		parsed, err := uuid.Parse(*body.OwnerID)
		if err != nil {
			respond.BadRequest(w, r, "invalid owner_id")
			return
		}
		ownerID = parsed
	}

	var pinHash *string
	if body.PIN != nil && *body.PIN != "" {
		if len(*body.PIN) != 4 {
			respond.BadRequest(w, r, "PIN must be exactly 4 digits")
			return
		}
		// cost 12 matches the password hasher; the old cost 10 was a
		// defense-in-depth gap on a short input (4-digit PIN is offline-
		// brute-forceable in seconds even at 12, but mismatched costs
		// were the real smell).
		h, err := bcrypt.GenerateFromPassword([]byte(*body.PIN), 12)
		if err != nil {
			respond.InternalError(w, r)
			return
		}
		s := string(h)
		pinHash = &s
	}
	parentPG := pgtype.UUID{Bytes: [16]byte(ownerID), Valid: true}
	row, err := h.db.CreateManagedProfile(r.Context(), gen.CreateManagedProfileParams{
		Username:     body.Username,
		ParentUserID: parentPG,
		AvatarUrl:    body.AvatarURL,
		Pin:          pinHash,
	})
	if err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(r.Context(), "create managed profile", "err", err)
		}
		respond.InternalError(w, r)
		return
	}
	respond.Created(w, r, profileResponse{
		ID:        row.ID.String(),
		Username:  row.Username,
		AvatarURL: row.AvatarUrl,
		HasPIN:    pinHash != nil,
		CreatedAt: row.CreatedAt.Time.Format(time.RFC3339),
	})
}

// UpdateProfile handles PATCH /api/v1/profiles/{id}.
// Admins can update any profile; regular users can only update their own.
func (h *UserHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	profileID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid profile id")
		return
	}
	var body struct {
		Username  string  `json:"username"`
		AvatarURL *string `json:"avatar_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username == "" {
		respond.BadRequest(w, r, "username is required")
		return
	}

	if claims.IsAdmin {
		row, err := h.db.UpdateManagedProfileAdmin(r.Context(), gen.UpdateManagedProfileAdminParams{
			ID:        profileID,
			Username:  body.Username,
			AvatarUrl: body.AvatarURL,
		})
		if err != nil {
			respond.NotFound(w, r)
			return
		}
		ownerID := row.ParentUserID.String()
		respond.Success(w, r, profileResponse{
			ID:        row.ID.String(),
			Username:  row.Username,
			AvatarURL: row.AvatarUrl,
			CreatedAt: row.CreatedAt.Time.Format(time.RFC3339),
			OwnerID:   &ownerID,
		})
		return
	}

	parentPG := pgtype.UUID{Bytes: [16]byte(claims.UserID), Valid: true}
	row, err := h.db.UpdateManagedProfile(r.Context(), gen.UpdateManagedProfileParams{
		ID:           profileID,
		Username:     body.Username,
		AvatarUrl:    body.AvatarURL,
		ParentUserID: parentPG,
	})
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	respond.Success(w, r, profileResponse{
		ID:        row.ID.String(),
		Username:  row.Username,
		AvatarURL: row.AvatarUrl,
		CreatedAt: row.CreatedAt.Time.Format(time.RFC3339),
	})
}

// DeleteProfile handles DELETE /api/v1/profiles/{id}.
// Admins can delete any profile; regular users can only delete their own.
func (h *UserHandler) DeleteProfile(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	profileID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid profile id")
		return
	}

	if claims.IsAdmin {
		if err := h.db.DeleteManagedProfileAdmin(r.Context(), profileID); err != nil {
			respond.NotFound(w, r)
			return
		}
		respond.NoContent(w)
		return
	}

	parentPG := pgtype.UUID{Bytes: [16]byte(claims.UserID), Valid: true}
	if err := h.db.DeleteManagedProfile(r.Context(), gen.DeleteManagedProfileParams{
		ID:           profileID,
		ParentUserID: parentPG,
	}); err != nil {
		respond.NotFound(w, r)
		return
	}
	respond.NoContent(w)
}

// ── Language preferences ─────────────────────────────────────────────────────

type preferencesResponse struct {
	PreferredAudioLang    *string `json:"preferred_audio_lang"`
	PreferredSubtitleLang *string `json:"preferred_subtitle_lang"`
	MaxContentRating      *string `json:"max_content_rating"`
	MaxVideoBitrateKbps   *int32  `json:"max_video_bitrate_kbps,omitempty"`
	MaxAudioBitrateKbps   *int32  `json:"max_audio_bitrate_kbps,omitempty"`
	MaxVideoHeight        *int32  `json:"max_video_height,omitempty"`
	PreferredVideoCodec   *string `json:"preferred_video_codec,omitempty"`
	ForcedSubtitlesOnly   bool    `json:"forced_subtitles_only"`
	EpisodeUseShowPoster  bool    `json:"episode_use_show_poster"`
	// HubLayout is the user's hub row customization (order + visibility).
	// Omitted entirely when the user has never customized — clients render
	// their default layout.
	HubLayout []hubRowPref `json:"hub_layout,omitempty"`
}

// hubRowPref is one entry of the per-user hub layout. Key is a row
// identifier shared across clients: "continue_tv", "continue_movies",
// "continue_other", "trending", or "library:<uuid>". Rows present in the
// hub data but absent from the layout render enabled, after the configured
// ones, in default order — so new libraries appear without the user having
// to re-save.
type hubRowPref struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

// GetPreferences handles GET /api/v1/users/me/preferences.
func (h *UserHandler) GetPreferences(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	row, err := h.db.GetUserPreferences(r.Context(), claims.UserID)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	resp := preferencesResponse{
		PreferredAudioLang:    row.PreferredAudioLang,
		PreferredSubtitleLang: row.PreferredSubtitleLang,
		MaxContentRating:      row.MaxContentRating,
		MaxVideoBitrateKbps:   row.MaxVideoBitrateKbps,
		MaxAudioBitrateKbps:   row.MaxAudioBitrateKbps,
		MaxVideoHeight:        row.MaxVideoHeight,
		PreferredVideoCodec:   row.PreferredVideoCodec,
		ForcedSubtitlesOnly:   row.ForcedSubtitlesOnly,
		EpisodeUseShowPoster:  row.EpisodeUseShowPoster,
	}
	if len(row.HubLayout) > 0 {
		// Tolerate a corrupt blob (manual DB edits) by omitting the field —
		// the client falls back to its default layout.
		_ = json.Unmarshal(row.HubLayout, &resp.HubLayout)
	}
	respond.Success(w, r, resp)
}

// SetHubLayout handles PUT /api/v1/users/me/hub-layout. Body is the full
// ordered layout: {"rows": [{"key": "trending", "enabled": false}, ...]}.
// An empty rows array resets to the default layout.
func (h *UserHandler) SetHubLayout(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	var body struct {
		Rows []hubRowPref `json:"rows"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if len(body.Rows) > 200 {
		respond.BadRequest(w, r, "too many rows")
		return
	}
	seen := make(map[string]bool, len(body.Rows))
	for _, row := range body.Rows {
		if row.Key == "" || len(row.Key) > 64 {
			respond.BadRequest(w, r, "invalid row key")
			return
		}
		if seen[row.Key] {
			respond.BadRequest(w, r, "duplicate row key: "+row.Key)
			return
		}
		seen[row.Key] = true
	}

	var blob []byte
	if len(body.Rows) > 0 {
		var err error
		if blob, err = json.Marshal(body.Rows); err != nil {
			respond.InternalError(w, r)
			return
		}
	}
	if err := h.db.UpdateUserHubLayout(r.Context(), gen.UpdateUserHubLayoutParams{
		ID:        claims.UserID,
		HubLayout: blob, // nil resets to default
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "set hub layout", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// SetPreferences handles PUT /api/v1/users/me/preferences.
func (h *UserHandler) SetPreferences(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	var body struct {
		PreferredAudioLang    *string `json:"preferred_audio_lang"`
		PreferredSubtitleLang *string `json:"preferred_subtitle_lang"`
		EpisodeUseShowPoster  *bool   `json:"episode_use_show_poster"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if err := h.db.UpdateUserPreferences(r.Context(), gen.UpdateUserPreferencesParams{
		ID:                    claims.UserID,
		PreferredAudioLang:    body.PreferredAudioLang,
		PreferredSubtitleLang: body.PreferredSubtitleLang,
		EpisodeUseShowPoster:  body.EpisodeUseShowPoster,
	}); err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// SetQualityProfile handles PUT /api/v1/users/me/quality-profile.
// Each field is a pointer so clients can leave any subset alone (send
// {"max_video_height": 1080} to cap resolution without touching bitrate
// caps). forced_subtitles_only is a plain bool — it's either on or off.
func (h *UserHandler) SetQualityProfile(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	var body struct {
		MaxVideoBitrateKbps *int32  `json:"max_video_bitrate_kbps"`
		MaxAudioBitrateKbps *int32  `json:"max_audio_bitrate_kbps"`
		MaxVideoHeight      *int32  `json:"max_video_height"`
		PreferredVideoCodec *string `json:"preferred_video_codec"`
		ForcedSubtitlesOnly bool    `json:"forced_subtitles_only"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	// Validate codec: empty/nil ok; otherwise must be a known name so we
	// don't store "h.264" garbage that mismatches the capabilities codec
	// list clients see.
	if body.PreferredVideoCodec != nil && *body.PreferredVideoCodec != "" {
		switch *body.PreferredVideoCodec {
		case "h264", "hevc":
		default:
			respond.BadRequest(w, r, "preferred_video_codec must be 'h264' or 'hevc'")
			return
		}
	}
	if err := h.db.UpdateUserQualityProfile(r.Context(), gen.UpdateUserQualityProfileParams{
		ID:                  claims.UserID,
		MaxVideoBitrateKbps: body.MaxVideoBitrateKbps,
		MaxAudioBitrateKbps: body.MaxAudioBitrateKbps,
		MaxVideoHeight:      body.MaxVideoHeight,
		PreferredVideoCodec: body.PreferredVideoCodec,
		ForcedSubtitlesOnly: body.ForcedSubtitlesOnly,
	}); err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// GetUserLibraries handles GET /api/v1/users/{id}/libraries — returns every
// library paired with whether the target user currently has access. Admins
// are reported as having access to everything.
func (h *UserHandler) GetUserLibraries(w http.ResponseWriter, r *http.Request) {
	if h.libAccess == nil {
		respond.InternalError(w, r)
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid user id")
		return
	}
	entries, err := h.libAccess.ListAccessForUser(r.Context(), targetID)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, entries)
}

// SetUserLibraries handles PUT /api/v1/users/{id}/libraries.
// Body: {"library_ids":["uuid","uuid",...]}
// Replaces the user's grants with exactly the given set.
func (h *UserHandler) SetUserLibraries(w http.ResponseWriter, r *http.Request) {
	if h.libAccess == nil {
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
		LibraryIDs []string `json:"library_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ids := make([]uuid.UUID, 0, len(body.LibraryIDs))
	for _, s := range body.LibraryIDs {
		id, err := uuid.Parse(s)
		if err != nil {
			respond.BadRequest(w, r, "invalid library id: "+s)
			return
		}
		ids = append(ids, id)
	}
	if err := h.libAccess.ReplaceAccessForUser(r.Context(), targetID, ids); err != nil {
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionUserRoleChange, targetID.String(),
			map[string]any{"library_ids": body.LibraryIDs}, audit.ClientIP(r))
	}
	respond.NoContent(w)
}

// SetProfileLibraryInherit handles PUT /api/v1/profiles/{id}/library-inherit.
// Toggles the inherit_library_access flag on a managed profile. The
// parent user (owner) and admins can both call it; for non-admins
// the SQL gates by parent ownership and returns 0 rows on a
// mismatch, which we surface as 404 — same shape as the rest of the
// profile endpoints.
func (h *UserHandler) SetProfileLibraryInherit(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		respond.InternalError(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	profileID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid profile id")
		return
	}
	var body struct {
		Inherit bool `json:"inherit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}

	// Admins bypass the ownership check; everyone else may only flip
	// their own profiles' flag (the SQL returns 0 rows on a parent
	// mismatch, which we map to 404).
	var ownerArg pgtype.UUID
	if !claims.IsAdmin {
		ownerArg = pgtype.UUID{Bytes: [16]byte(claims.UserID), Valid: true}
	}
	rows, err := h.db.SetProfileInheritLibraryAccess(r.Context(), gen.SetProfileInheritLibraryAccessParams{
		Inherit: body.Inherit,
		ID:      profileID,
		OwnerID: ownerArg,
	})
	if err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(r.Context(), "set profile library inherit", "profile_id", profileID, "err", err)
		}
		respond.InternalError(w, r)
		return
	}
	if rows == 0 {
		respond.NotFound(w, r)
		return
	}
	respond.NoContent(w)
}

// SetContentRating handles PUT /api/v1/users/{id}/content-rating.
// Only admins can set content ratings on any user.
func (h *UserHandler) SetContentRating(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid user id")
		return
	}
	var body struct {
		MaxContentRating *string `json:"max_content_rating"`
	}
	// Reject unknown fields. Without this, a client typo like
	// `content_rating` (instead of `max_content_rating`) silently
	// decodes into nothing, the handler updates the user with
	// MaxContentRating=nil (clearing the ceiling), and returns 204
	// — looking exactly like a successful set from the caller's
	// perspective. The wrong field stays silently broken until
	// someone notices the ceiling isn't enforced.
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body: "+err.Error())
		return
	}
	if err := h.db.UpdateUserContentRating(r.Context(), gen.UpdateUserContentRatingParams{
		ID:               targetID,
		MaxContentRating: body.MaxContentRating,
	}); err != nil {
		respond.InternalError(w, r)
		return
	}
	// Revoke the target's outstanding credentials, mirroring SetAdmin.
	//
	// The ceiling is baked into the PASETO claims at issue time, so the DB
	// update alone changes nothing for tokens already in the wild: the access
	// token rides out its TTL and the 24 h asset/stream tokens keep carrying
	// the OLD, looser ceiling for up to a day. An admin tightening a child's
	// parental control reasonably expects it to bite immediately, and had no
	// override available. Fail the request on error — leaving the ceiling
	// changed but the old tokens live is the worst state to be in.
	if err := h.db.BumpSessionEpoch(r.Context(), targetID); err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(r.Context(), "bump session epoch after content-rating change",
				"target_id", targetID, "err", err)
		}
		respond.InternalError(w, r)
		return
	}
	if err := h.db.DeleteSessionsForUser(r.Context(), targetID); err != nil {
		if h.logger != nil {
			h.logger.ErrorContext(r.Context(), "delete sessions after content-rating change",
				"target_id", targetID, "err", err)
		}
		respond.InternalError(w, r)
		return
	}
	// Segment tokens are a query-string capability with their own Valkey TTL
	// and no session_epoch participation, so they need an explicit revoke or a
	// restricted profile keeps streaming over-ceiling content for hours.
	if h.segTokens != nil {
		if err := h.segTokens.RevokeAllForUser(r.Context(), targetID); err != nil && h.logger != nil {
			h.logger.WarnContext(r.Context(), "revoke segment tokens after content-rating change",
				"target_id", targetID, "err", err)
		}
	}
	if h.audit != nil {
		rating := "none"
		if body.MaxContentRating != nil {
			rating = *body.MaxContentRating
		}
		h.audit.Log(r.Context(), &claims.UserID, audit.ActionUserRatingChange, targetID.String(),
			map[string]any{"max_content_rating": rating}, audit.ClientIP(r))
	}
	respond.NoContent(w)
}

// GetStreamingLimits returns a user's admin-set streaming caps (admin only).
// GET /api/v1/users/{id}/streaming-limits. null fields = no cap.
func (h *UserHandler) GetStreamingLimits(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid user id")
		return
	}
	row, err := h.db.GetUserStreamCaps(r.Context(), targetID)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, map[string]any{
		"max_concurrent_streams":  row.MaxConcurrentStreams,
		"max_stream_bitrate_kbps": row.MaxStreamBitrateKbps,
	})
}

// SetStreamingLimits sets a user's admin-enforced streaming caps (admin only).
// PUT /api/v1/users/{id}/streaming-limits with
// {"max_concurrent_streams": N|null, "max_stream_bitrate_kbps": N|null}.
// null = remove the cap. Enforced server-side at transcode start (read from the
// DB), so it takes effect on the user's next stream — no re-login needed.
func (h *UserHandler) SetStreamingLimits(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid user id")
		return
	}
	var body struct {
		MaxConcurrentStreams *int32 `json:"max_concurrent_streams"`
		MaxStreamBitrateKbps *int32 `json:"max_stream_bitrate_kbps"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body: "+err.Error())
		return
	}
	// Reject negative caps (0/null both mean "no cap").
	if (body.MaxConcurrentStreams != nil && *body.MaxConcurrentStreams < 0) ||
		(body.MaxStreamBitrateKbps != nil && *body.MaxStreamBitrateKbps < 0) {
		respond.BadRequest(w, r, "caps must be non-negative")
		return
	}
	if err := h.db.UpdateUserStreamCaps(r.Context(), gen.UpdateUserStreamCapsParams{
		ID:                   targetID,
		MaxConcurrentStreams: body.MaxConcurrentStreams,
		MaxStreamBitrateKbps: body.MaxStreamBitrateKbps,
	}); err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}
