package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/scrobble"
)

// Scrobbler is the per-user scrobble surface the handler needs. Satisfied by
// *scrobble.Service.
type Scrobbler interface {
	Status(ctx context.Context, userID uuid.UUID) (scrobble.Status, error)
	SetListenBrainz(ctx context.Context, userID uuid.UUID, token string, enabled bool) error
	StartLastFMLink(ctx context.Context, userID uuid.UUID) (scrobble.LastFMLinkStart, error)
	CompleteLastFMLink(ctx context.Context, userID uuid.UUID, pending string) (scrobble.LinkResult, error)
	UnlinkLastFM(ctx context.Context, userID uuid.UUID) error
	StartTraktLink(ctx context.Context, userID uuid.UUID) (scrobble.TraktLinkStart, error)
	CompleteTraktLink(ctx context.Context, userID uuid.UUID, pending string) (scrobble.LinkResult, error)
	UnlinkTrakt(ctx context.Context, userID uuid.UUID) error
}

// ScrobbleHandler serves the per-user scrobble account-link endpoints.
type ScrobbleHandler struct {
	svc    Scrobbler
	logger *slog.Logger
}

// NewScrobbleHandler constructs a ScrobbleHandler.
func NewScrobbleHandler(svc Scrobbler, logger *slog.Logger) *ScrobbleHandler {
	return &ScrobbleHandler{svc: svc, logger: logger}
}

// GetStatus handles GET /api/v1/users/me/scrobble — what the caller has
// linked, and whether the operator has set up Last.fm / Trakt at all
// (*_available). No credential is ever returned.
func (h *ScrobbleHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	st, err := h.svc.Status(r.Context(), claims.UserID)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, map[string]any{
		"listenbrainz_linked":  st.ListenBrainzLinked,
		"listenbrainz_enabled": st.ListenBrainzEnabled,
		"lastfm_available":     st.LastFMAvailable,
		"lastfm_linked":        st.LastFMLinked,
		"lastfm_username":      st.LastFMUsername,
		"trakt_available":      st.TraktAvailable,
		"trakt_linked":         st.TraktLinked,
		"trakt_username":       st.TraktUsername,
	})
}

// SetListenBrainz handles PUT /api/v1/users/me/scrobble/listenbrainz.
// Body: {"token":"...","enabled":true}. An empty token unlinks the account.
func (h *ScrobbleHandler) SetListenBrainz(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	var body struct {
		Token   string `json:"token"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if err := h.svc.SetListenBrainz(r.Context(), claims.UserID, strings.TrimSpace(body.Token), body.Enabled); err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// StartLastFMLink handles POST /api/v1/users/me/scrobble/lastfm/link. The
// client opens auth_url for the user to approve OnScreen, then polls
// .../lastfm/link/complete with pending.
func (h *ScrobbleHandler) StartLastFMLink(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	start, err := h.svc.StartLastFMLink(r.Context(), claims.UserID)
	if err != nil {
		h.linkError(w, r, "Last.fm", err)
		return
	}
	respond.Success(w, r, map[string]any{"auth_url": start.AuthURL, "pending": start.Pending})
}

// CompleteLastFMLink handles POST /api/v1/users/me/scrobble/lastfm/link/complete.
// Body: {"pending":"..."}. Returns {"status": pending|linked|expired, "username"}.
func (h *ScrobbleHandler) CompleteLastFMLink(w http.ResponseWriter, r *http.Request) {
	h.complete(w, r, "Last.fm", h.svc.CompleteLastFMLink)
}

// UnlinkLastFM handles DELETE /api/v1/users/me/scrobble/lastfm.
func (h *ScrobbleHandler) UnlinkLastFM(w http.ResponseWriter, r *http.Request) {
	h.unlink(w, r, h.svc.UnlinkLastFM)
}

// StartTraktLink handles POST /api/v1/users/me/scrobble/trakt/link. The user
// enters user_code at verification_url (on any device); the client polls
// .../trakt/link/complete with pending every interval seconds.
func (h *ScrobbleHandler) StartTraktLink(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	start, err := h.svc.StartTraktLink(r.Context(), claims.UserID)
	if err != nil {
		h.linkError(w, r, "Trakt", err)
		return
	}
	respond.Success(w, r, map[string]any{
		"user_code":        start.UserCode,
		"verification_url": start.VerificationURL,
		"expires_in":       start.ExpiresIn,
		"interval":         start.Interval,
		"pending":          start.Pending,
	})
}

// CompleteTraktLink handles POST /api/v1/users/me/scrobble/trakt/link/complete.
// Body: {"pending":"..."}. Returns {"status": pending|slow_down|linked|expired|denied, "username"}.
func (h *ScrobbleHandler) CompleteTraktLink(w http.ResponseWriter, r *http.Request) {
	h.complete(w, r, "Trakt", h.svc.CompleteTraktLink)
}

// UnlinkTrakt handles DELETE /api/v1/users/me/scrobble/trakt; the token is
// also revoked with Trakt.
func (h *ScrobbleHandler) UnlinkTrakt(w http.ResponseWriter, r *http.Request) {
	h.unlink(w, r, h.svc.UnlinkTrakt)
}

func (h *ScrobbleHandler) complete(w http.ResponseWriter, r *http.Request, service string,
	fn func(context.Context, uuid.UUID, string) (scrobble.LinkResult, error)) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	var body struct {
		Pending string `json:"pending"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Pending == "" {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	res, err := fn(r.Context(), claims.UserID, body.Pending)
	if err != nil {
		h.linkError(w, r, service, err)
		return
	}
	out := map[string]any{"status": res.Status}
	if res.Username != "" {
		out["username"] = res.Username
	}
	respond.Success(w, r, out)
}

func (h *ScrobbleHandler) unlink(w http.ResponseWriter, r *http.Request, fn func(context.Context, uuid.UUID) error) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	if err := fn(r.Context(), claims.UserID); err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// linkError maps a link-flow failure. Anything unrecognised is the service
// itself failing (unreachable, or an unexpected answer), reported as 502 so
// the client can say so rather than blame the user.
func (h *ScrobbleHandler) linkError(w http.ResponseWriter, r *http.Request, service string, err error) {
	switch {
	case errors.Is(err, scrobble.ErrNotConfigured):
		respond.Error(w, r, http.StatusConflict, "SCROBBLE_NOT_CONFIGURED",
			service+" isn't set up on this server; an admin adds its API credentials in Settings")
	case errors.Is(err, scrobble.ErrInvalidPending):
		respond.BadRequest(w, r, "invalid link request; start again")
	default:
		if h.logger != nil {
			h.logger.WarnContext(r.Context(), "scrobble link", "service", service, "err", err)
		}
		respond.Error(w, r, http.StatusBadGateway, "SCROBBLE_UPSTREAM", "couldn't reach "+service)
	}
}
