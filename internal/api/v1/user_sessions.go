package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// UserSessionsDB is the sessions-table subset the signed-in devices endpoints
// need. Satisfied by *gen.Queries — on the PRIMARY pool, so a revocation is
// visible to the auth middleware's next session check immediately.
type UserSessionsDB interface {
	ListActiveSessionsForUser(ctx context.Context, userID uuid.UUID) ([]gen.ListActiveSessionsForUserRow, error)
	DeleteUserSession(ctx context.Context, arg gen.DeleteUserSessionParams) (gen.DeleteUserSessionRow, error)
	DeleteOtherSessionsForUser(ctx context.Context, arg gen.DeleteOtherSessionsForUserParams) (int64, error)
	DeleteSessionsForUser(ctx context.Context, userID uuid.UUID) error
	BumpSessionEpoch(ctx context.Context, id uuid.UUID) error
}

// UserSessionsHandler lists a user's sign-ins and revokes them one at a time —
// the "Signed-in devices" list in Settings ▸ Security and on the admin user
// page.
//
// Revoking one sign-in deletes its sessions row. That alone ends it
// everywhere it matters: the refresh token can no longer refresh, and the
// auth middleware rejects the access / asset / stream tokens minted under it
// (they carry its ID as the sid claim — see middleware.SessionStateReader) on
// their next request. No epoch bump, so the user's other devices — including
// native players mid-stream on a 24 h stream token — are untouched. Two
// exceptions lapse on their own instead: tokens minted before the sid claim
// existed (at most 24 h after upgrading), and HLS segment tokens, which are
// per user rather than per sign-in and expire when the stream goes idle.
type UserSessionsHandler struct {
	db        UserSessionsDB
	segTokens SegmentTokenRevoker
	audit     *audit.Logger
	logger    *slog.Logger
}

// NewUserSessionsHandler creates a UserSessionsHandler.
func NewUserSessionsHandler(db UserSessionsDB, logger *slog.Logger) *UserSessionsHandler {
	return &UserSessionsHandler{db: db, logger: logger}
}

// WithAudit attaches an audit logger. Returns the handler for chaining.
func (h *UserSessionsHandler) WithAudit(a *audit.Logger) *UserSessionsHandler {
	h.audit = a
	return h
}

// WithSegmentTokenRevoker wires the HLS segment-token wipe used by the admin
// "sign out everywhere". Optional — nil leaves segment tokens to their TTL.
func (h *UserSessionsHandler) WithSegmentTokenRevoker(r SegmentTokenRevoker) *UserSessionsHandler {
	h.segTokens = r
	return h
}

// signedInSession is one row of the signed-in devices list. last_seen_at is
// approximate: it moves on sign-in and on each token refresh (about hourly
// while a device is in use), never per request.
type signedInSession struct {
	ID         string    `json:"id"`
	ClientName string    `json:"client_name"`
	Platform   string    `json:"platform,omitempty"`
	IPAddress  string    `json:"ip_address,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	// Current marks the sign-in the caller's own token belongs to.
	Current bool `json:"current"`
}

// ListMine handles GET /api/v1/users/me/sessions.
func (h *UserSessionsHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	h.list(w, r, claims.UserID, claims.SessionID)
}

// ListForUser handles GET /api/v1/users/{id}/sessions (admin).
func (h *UserSessionsHandler) ListForUser(w http.ResponseWriter, r *http.Request) {
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
	// The admin's own session is marked current when they look at themselves.
	current := uuid.Nil
	if targetID == claims.UserID {
		current = claims.SessionID
	}
	h.list(w, r, targetID, current)
}

func (h *UserSessionsHandler) list(w http.ResponseWriter, r *http.Request, userID, current uuid.UUID) {
	rows, err := h.db.ListActiveSessionsForUser(r.Context(), userID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list sessions", "user_id", userID, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]signedInSession, 0, len(rows))
	for _, row := range rows {
		s := signedInSession{
			ID:         row.ID.String(),
			ClientName: derefStr(row.ClientName),
			Platform:   derefStr(row.Platform),
			CreatedAt:  row.CreatedAt.Time,
			LastSeenAt: row.LastSeen.Time,
			Current:    current != uuid.Nil && row.ID == current,
		}
		if row.IpAddr != nil {
			s.IPAddress = row.IpAddr.String()
		}
		out = append(out, s)
	}
	respond.Success(w, r, out)
}

// RevokeMine handles DELETE /api/v1/users/me/sessions/{id}: signs one of the
// caller's own devices out. Targeting the caller's current session is a
// sign-out — the auth cookies are cleared in the same response.
func (h *UserSessionsHandler) RevokeMine(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	sid, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid session id")
		return
	}
	h.revoke(w, r, claims.UserID, claims.UserID, sid, claims.SessionID, false)
}

// RevokeForUser handles DELETE /api/v1/users/{id}/sessions/{sid} (admin).
func (h *UserSessionsHandler) RevokeForUser(w http.ResponseWriter, r *http.Request) {
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
	sid, err := uuid.Parse(chi.URLParam(r, "sid"))
	if err != nil {
		respond.BadRequest(w, r, "invalid session id")
		return
	}
	current := uuid.Nil
	if targetID == claims.UserID {
		current = claims.SessionID
	}
	h.revoke(w, r, claims.UserID, targetID, sid, current, true)
}

func (h *UserSessionsHandler) revoke(w http.ResponseWriter, r *http.Request, actor, owner, sid, current uuid.UUID, byAdmin bool) {
	ctx := r.Context()
	// Scoped by owner in SQL: a session ID belonging to anyone else matches
	// no row and reads as 404, so IDs can't be probed across users.
	row, err := h.db.DeleteUserSession(ctx, gen.DeleteUserSessionParams{ID: sid, UserID: owner})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(ctx, "revoke session", "user_id", owner, "session_id", sid, "err", err)
		respond.InternalError(w, r)
		return
	}
	isCurrent := current != uuid.Nil && sid == current
	if h.audit != nil {
		detail := map[string]any{
			"session_id": sid.String(),
			"by_admin":   byAdmin,
			"current":    isCurrent,
		}
		if row.ClientName != nil {
			detail["client_name"] = truncate(*row.ClientName, 200)
		}
		if row.Platform != nil {
			detail["platform"] = *row.Platform
		}
		h.audit.Log(ctx, &actor, audit.ActionSessionRevoked, owner.String(), detail, audit.ClientIP(r))
	}
	if isCurrent {
		clearAuthCookies(w, r)
	}
	respond.NoContent(w)
}

// RevokeOthers handles DELETE /api/v1/users/me/sessions: signs the caller out
// everywhere except the device making the request. Needs to know which
// session that is, so a token without the sid claim (minted before it
// existed, or a PIN-switch token with no session of its own) gets 409 and the
// client should refresh, which mints one that has it.
func (h *UserSessionsHandler) RevokeOthers(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	if claims.SessionID == uuid.Nil {
		respond.Error(w, r, http.StatusConflict, "CURRENT_SESSION_UNKNOWN",
			"this sign-in can't be told apart from your others; sign in again and retry")
		return
	}
	ctx := r.Context()
	n, err := h.db.DeleteOtherSessionsForUser(ctx, gen.DeleteOtherSessionsForUserParams{
		UserID: claims.UserID, ID: claims.SessionID,
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "revoke other sessions", "user_id", claims.UserID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		h.audit.Log(ctx, &claims.UserID, audit.ActionSessionsRevoked, claims.UserID.String(), map[string]any{
			"reason":           "user_signed_out_others",
			"sessions_deleted": n,
		}, audit.ClientIP(r))
	}
	respond.Success(w, r, map[string]any{"revoked": n})
}

// RevokeAllForUser handles DELETE /api/v1/users/{id}/sessions (admin): signs
// the user out on every device. Unlike the per-device revoke this is the full
// family wipe password reset uses — sessions deleted, epoch bumped (so tokens
// minted before the sid claim die too), HLS segment tokens revoked.
func (h *UserSessionsHandler) RevokeAllForUser(w http.ResponseWriter, r *http.Request) {
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
	ctx := r.Context()
	derr := h.db.DeleteSessionsForUser(ctx, targetID)
	if derr != nil {
		h.logger.ErrorContext(ctx, "admin sign-out everywhere: delete sessions", "user_id", targetID, "err", derr)
	}
	berr := h.db.BumpSessionEpoch(ctx, targetID)
	if berr != nil {
		h.logger.ErrorContext(ctx, "admin sign-out everywhere: bump epoch", "user_id", targetID, "err", berr)
	}
	if h.segTokens != nil {
		if err := h.segTokens.RevokeAllForUser(ctx, targetID); err != nil {
			h.logger.WarnContext(ctx, "admin sign-out everywhere: revoke segment tokens", "user_id", targetID, "err", err)
		}
	}
	if h.audit != nil {
		h.audit.Log(ctx, &claims.UserID, audit.ActionSessionsRevoked, targetID.String(), map[string]any{
			"reason":           "admin_signed_out_all",
			"sessions_deleted": derr == nil,
			"epoch_bumped":     berr == nil,
		}, audit.ClientIP(r))
	}
	if derr != nil || berr != nil {
		respond.InternalError(w, r)
		return
	}
	if targetID == claims.UserID {
		clearAuthCookies(w, r)
	}
	respond.NoContent(w)
}
