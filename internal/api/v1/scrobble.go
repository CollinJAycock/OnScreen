package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/scrobble"
)

// ScrobbleStore is the per-user scrobble-config surface the handler needs.
// Satisfied by *scrobble.Store.
type ScrobbleStore interface {
	SetListenBrainz(ctx context.Context, userID uuid.UUID, token string, enabled bool) error
	Status(ctx context.Context, userID uuid.UUID) (scrobble.Status, error)
}

// ScrobbleHandler serves the per-user scrobble account-link endpoints.
type ScrobbleHandler struct {
	store ScrobbleStore
}

// NewScrobbleHandler constructs a ScrobbleHandler.
func NewScrobbleHandler(store ScrobbleStore) *ScrobbleHandler {
	return &ScrobbleHandler{store: store}
}

// GetStatus handles GET /api/v1/users/me/scrobble — reports whether the caller
// has a ListenBrainz token linked and whether export is enabled. The token
// itself is never returned.
func (h *ScrobbleHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	st, err := h.store.Status(r.Context(), claims.UserID)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, map[string]any{
		"listenbrainz_linked":  st.ListenBrainzLinked,
		"listenbrainz_enabled": st.ListenBrainzEnabled,
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
	if err := h.store.SetListenBrainz(r.Context(), claims.UserID, strings.TrimSpace(body.Token), body.Enabled); err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}
