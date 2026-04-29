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
	UpdateUserQualityProfile(ctx context.Context, arg gen.UpdateUserQualityProfileParams) error
	UpdateUserContentRating(ctx context.Context, arg gen.UpdateUserContentRatingParams) error
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
		UserID:           result.UserID,
		Username:         result.Username,
		IsAdmin:          result.IsAdmin,
		MaxContentRating: result.MaxContentRating,
		SessionEpoch:     result.SessionEpoch,
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
	respond.Success(w, r, preferencesResponse{
		PreferredAudioLang:    row.PreferredAudioLang,
		PreferredSubtitleLang: row.PreferredSubtitleLang,
		MaxContentRating:      row.MaxContentRating,
		MaxVideoBitrateKbps:   row.MaxVideoBitrateKbps,
		MaxAudioBitrateKbps:   row.MaxAudioBitrateKbps,
		MaxVideoHeight:        row.MaxVideoHeight,
		PreferredVideoCodec:   row.PreferredVideoCodec,
		ForcedSubtitlesOnly:   row.ForcedSubtitlesOnly,
		EpisodeUseShowPoster:  row.EpisodeUseShowPoster,
	})
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if err := h.db.UpdateUserContentRating(r.Context(), gen.UpdateUserContentRatingParams{
		ID:               targetID,
		MaxContentRating: body.MaxContentRating,
	}); err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}
