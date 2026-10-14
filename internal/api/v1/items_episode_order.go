package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/observability"
)

// ItemEpisodeOrderSetter sets or (nil) clears a show's own episode order.
// Satisfied by *media.Service; ErrNotFound when the item is not a live
// show.
type ItemEpisodeOrderSetter interface {
	SetShowEpisodeOrder(ctx context.Context, id uuid.UUID, order *string) error
}

// WithEpisodeOrder enables PUT /items/{id}/episode-order.
func (h *ItemHandler) WithEpisodeOrder(s ItemEpisodeOrderSetter) *ItemHandler {
	h.episodeOrder = s
	return h
}

// episodeOrderRequest is the body of PUT /items/{id}/episode-order.
// EpisodeOrder is "aired", "dvd" or "absolute"; null (or "") clears the
// show's own order so it follows its library's default again.
type episodeOrderRequest struct {
	EpisodeOrder *string `json:"episode_order"`
}

// SetEpisodeOrder handles PUT /api/v1/items/{id}/episode-order.
//
// Admin-only: picks the order a show's episode files are numbered in —
// aired (the provider's broadcast order), DVD or absolute — overriding
// the library default. The show is then re-enriched in the background
// (the same plumbing as POST /items/{id}/enrich, children forced) so its
// episodes take their titles, summaries and thumbnails from the chosen
// order. A provider without that order for the show leaves the episodes
// on aired order, with a warning in the server log.
func (h *ItemHandler) SetEpisodeOrder(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	if h.episodeOrder == nil {
		respond.BadRequest(w, r, "episode order is not configured")
		return
	}

	var body episodeOrderRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid JSON body: "+err.Error())
		return
	}
	order := body.EpisodeOrder
	if order != nil && *order == "" {
		order = nil
	}
	if order != nil && !media.ValidEpisodeOrder(*order) {
		respond.BadRequest(w, r, "episode_order must be aired, dvd, absolute or null")
		return
	}

	item, err := h.media.GetItem(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get item for episode order", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if item.Type != "show" {
		respond.ValidationError(w, r, "episode order can only be set on a show")
		return
	}

	if err := h.episodeOrder.SetShowEpisodeOrder(r.Context(), id, order); err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "set show episode order", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	if h.audit != nil {
		actor := claims.UserID
		detail := map[string]any{"episode_order": nil}
		if order != nil {
			detail["episode_order"] = *order
		}
		h.audit.Log(r.Context(), &actor, audit.ActionItemEpisodeOrder, id.String(), detail, audit.ClientIP(r))
	}

	// Re-number the show's episodes under the new order. Only when the
	// order actually changed: re-sending the current value is a no-op.
	if h.enricher != nil && !sameEpisodeOrder(item.EpisodeOrder, order) {
		bgCtx := context.WithoutCancel(r.Context())
		observability.SafeGo(h.logger, "items:episode-order-reenrich", func() {
			if err := h.enricher.EnrichItem(bgCtx, id); err != nil {
				h.logger.WarnContext(bgCtx, "episode order re-enrich failed", "id", id, "err", err)
			}
		})
	}

	respond.Success(w, r, map[string]any{
		"id":            id.String(),
		"episode_order": order,
	})
}

// sameEpisodeOrder reports whether two optional episode orders are equal.
func sameEpisodeOrder(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
