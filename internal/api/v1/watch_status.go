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
	"github.com/onscreen/onscreen/internal/db/gen"
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
	// Optional item gate (see WithItemGate). nil leaves the historical
	// behaviour of accepting any item id.
	items  watchStatusItemGetter
	access LibraryAccessChecker
}

// watchStatusItemGetter loads an item for the visibility gate.
type watchStatusItemGetter interface {
	GetMediaItem(ctx context.Context, id uuid.UUID) (gen.GetMediaItemRow, error)
}

// NewWatchStatusHandler constructs the handler.
func NewWatchStatusHandler(svc WatchStatusService, logger *slog.Logger) *WatchStatusHandler {
	return &WatchStatusHandler{svc: svc, logger: logger}
}

// WithItemGate makes every watch-status read/write first check that the caller
// can see the item (library ACL + content-rating ceiling), 404ing otherwise —
// the same gate lists and favorites apply. Without it a user, including a
// restricted profile, could attach statuses to items outside their libraries
// and use the responses as an item-existence oracle.
func (h *WatchStatusHandler) WithItemGate(items watchStatusItemGetter, access LibraryAccessChecker) *WatchStatusHandler {
	h.items = items
	h.access = access
	return h
}

// itemVisible applies the item gate; true means proceed.
func (h *WatchStatusHandler) itemVisible(w http.ResponseWriter, r *http.Request, itemID uuid.UUID) bool {
	if h.items == nil {
		return true
	}
	item, err := h.items.GetMediaItem(r.Context(), itemID)
	if err != nil {
		respond.NotFound(w, r)
		return false
	}
	return itemAddAllowed(w, r, h.access, h.logger, item)
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

// Get returns the per-user status for an item. The "no row yet" case
// returns 200 with status="" rather than 404: the resource here is the
// user's relationship to the item, which has a meaningful default state
// (no classification) — that's semantically a successful read, not a
// missing resource. Returning 404 made the browser's resource-loading
// layer emit a "Failed to load resource: 404" console message on every
// /watch page visit (no JS catch can suppress that, it's pre-JS), which
// surfaced in Playwright as a spurious test failure. Status="" is the
// same value the frontend's 404-catch path was assigning anyway, so the
// UI is unchanged.
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
	if !h.itemVisible(w, r, itemID) {
		return
	}
	st, err := h.svc.Get(r.Context(), claims.UserID, itemID)
	if err != nil {
		if errors.Is(err, watchstatus.ErrNotFound) {
			respond.JSON(w, r, http.StatusOK, WatchStatusResponse{})
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
	if !h.itemVisible(w, r, itemID) {
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
	if !h.itemVisible(w, r, itemID) {
		return
	}
	if err := h.svc.Clear(r.Context(), claims.UserID, itemID); err != nil {
		h.logger.ErrorContext(r.Context(), "clear watch status", "user_id", claims.UserID, "item_id", itemID, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}
