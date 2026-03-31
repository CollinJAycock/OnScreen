package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/notification"
)

// NotificationDB defines the database operations the notification handler needs.
type NotificationDB interface {
	ListNotifications(ctx context.Context, arg gen.ListNotificationsParams) ([]gen.Notification, error)
	CountUnreadNotifications(ctx context.Context, userID uuid.UUID) (int64, error)
	MarkNotificationRead(ctx context.Context, arg gen.MarkNotificationReadParams) error
	MarkAllNotificationsRead(ctx context.Context, userID uuid.UUID) error
}

// NotificationHandler serves notification endpoints.
type NotificationHandler struct {
	db     NotificationDB
	broker *notification.Broker
	logger *slog.Logger
}

// NewNotificationHandler creates a NotificationHandler.
func NewNotificationHandler(db NotificationDB, broker *notification.Broker, logger *slog.Logger) *NotificationHandler {
	return &NotificationHandler{db: db, broker: broker, logger: logger}
}

type notificationResponse struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	ItemID    *string `json:"item_id,omitempty"`
	Read      bool    `json:"read"`
	CreatedAt int64   `json:"created_at"`
}

// List handles GET /api/v1/notifications?limit=20&offset=0.
func (h *NotificationHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}

	limit := int32(20)
	offset := int32(0)
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = int32(n)
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = int32(n)
		}
	}

	rows, err := h.db.ListNotifications(r.Context(), gen.ListNotificationsParams{
		UserID: claims.UserID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list notifications", "err", err)
		respond.InternalError(w, r)
		return
	}

	out := make([]notificationResponse, len(rows))
	for i, n := range rows {
		var itemID *string
		if n.ItemID.Valid {
			id := uuid.UUID(n.ItemID.Bytes).String()
			itemID = &id
		}
		out[i] = notificationResponse{
			ID:        n.ID.String(),
			Type:      n.Type,
			Title:     n.Title,
			Body:      n.Body,
			ItemID:    itemID,
			Read:      n.Read,
			CreatedAt: n.CreatedAt.Time.UnixMilli(),
		}
	}
	respond.Success(w, r, out)
}

// UnreadCount handles GET /api/v1/notifications/unread-count.
func (h *NotificationHandler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	count, err := h.db.CountUnreadNotifications(r.Context(), claims.UserID)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, map[string]int64{"count": count})
}

// MarkRead handles POST /api/v1/notifications/{id}/read.
func (h *NotificationHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid notification id")
		return
	}
	if err := h.db.MarkNotificationRead(r.Context(), gen.MarkNotificationReadParams{
		ID:     id,
		UserID: claims.UserID,
	}); err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// MarkAllRead handles POST /api/v1/notifications/read-all.
func (h *NotificationHandler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	if err := h.db.MarkAllNotificationsRead(r.Context(), claims.UserID); err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// Stream handles GET /api/v1/notifications/stream (SSE).
func (h *NotificationHandler) Stream(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		respond.InternalError(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := h.broker.Subscribe(claims.UserID)
	defer h.broker.Unsubscribe(claims.UserID, ch)

	// Send initial keepalive.
	fmt.Fprintf(w, ": keepalive\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
