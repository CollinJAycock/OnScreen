package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/domain/watchstatus"
)

// WatchStatusService is the narrow domain interface this handler
// depends on. Mirrors watchstatus.Service so tests can swap in a
// stub without pulling the full service.
type WatchStatusService interface {
	Get(ctx context.Context, userID, mediaItemID uuid.UUID) (watchstatus.Status, error)
	Set(ctx context.Context, userID, mediaItemID uuid.UUID, status string) (watchstatus.Status, error)
	Clear(ctx context.Context, userID, mediaItemID uuid.UUID) error
}

// WatchStatusHandler implements GET / PUT / DELETE
// /api/v1/items/{id}/watch-status. Always per-authenticated-user;
// admin doesn't get to manage other users' lists from this endpoint.
type WatchStatusHandler struct {
	svc    WatchStatusService
	logger *slog.Logger
}

// NewWatchStatusHandler constructs the handler.
func NewWatchStatusHandler(svc WatchStatusService, logger *slog.Logger) *WatchStatusHandler {
	return &WatchStatusHandler{svc: svc, logger: logger}
}

// WatchStatusResponse is the JSON shape for a single (user, item)
// status row. Status is one of `plan_to_watch`, `watching`,
// `completed`, `on_hold`, `dropped`. The two timestamps anchor
// "first added on …" + "last status change on …" displays.
type WatchStatusResponse struct {
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Get returns the per-user status for an item. 404 when nothing has
// been set — distinct from 200 with an empty status, since "no row"
// means "neither queued nor classified" which is a meaningful state.
func (h *WatchStatusHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	itemID, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	st, err := h.svc.Get(r.Context(), claims.UserID, itemID)
	if err != nil {
		if errors.Is(err, watchstatus.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get watch status", "user_id", claims.UserID, "item_id", itemID, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.JSON(w, r, http.StatusOK, WatchStatusResponse{
		Status:    st.Status,
		CreatedAt: st.CreatedAt,
		UpdatedAt: st.UpdatedAt,
	})
}

type setWatchStatusRequest struct {
	Status string `json:"status"`
}

// Put creates or updates the per-user status for an item. Body:
// `{"status":"watching"}` where status is one of the five recognised
// values. Validation happens both at the handler (clean error) and
// the DB CHECK constraint (defence in depth).
func (h *WatchStatusHandler) Put(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	itemID, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	var body setWatchStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if !watchstatus.IsValidStatus(body.Status) {
		respond.BadRequest(w, r, "status must be one of plan_to_watch, watching, on_hold, completed, dropped")
		return
	}
	st, err := h.svc.Set(r.Context(), claims.UserID, itemID, body.Status)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "set watch status", "user_id", claims.UserID, "item_id", itemID, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.JSON(w, r, http.StatusOK, WatchStatusResponse{
		Status:    st.Status,
		CreatedAt: st.CreatedAt,
		UpdatedAt: st.UpdatedAt,
	})
}

// Delete clears the per-user status for an item. 204 regardless of
// whether a row existed — DELETE is idempotent.
func (h *WatchStatusHandler) Delete(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	itemID, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	if err := h.svc.Clear(r.Context(), claims.UserID, itemID); err != nil {
		h.logger.ErrorContext(r.Context(), "clear watch status", "user_id", claims.UserID, "item_id", itemID, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}
