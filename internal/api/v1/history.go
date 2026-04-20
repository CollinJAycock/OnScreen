package v1

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// HistoryDB defines the database queries the history handler needs.
type HistoryDB interface {
	ListWatchHistory(ctx context.Context, arg gen.ListWatchHistoryParams) ([]gen.ListWatchHistoryRow, error)
}

// HistoryHandler serves watch history data.
type HistoryHandler struct {
	db     HistoryDB
	access LibraryAccessChecker
	logger *slog.Logger
}

// NewHistoryHandler creates a HistoryHandler.
func NewHistoryHandler(db HistoryDB, logger *slog.Logger) *HistoryHandler {
	return &HistoryHandler{db: db, logger: logger}
}

// WithLibraryAccess enables per-user library filtering on history rows.
func (h *HistoryHandler) WithLibraryAccess(a LibraryAccessChecker) *HistoryHandler {
	h.access = a
	return h
}

// WatchHistoryItem is the JSON response type for a single watch history entry.
type WatchHistoryItem struct {
	ID         string  `json:"id"`
	MediaID    string  `json:"media_id"`
	Title      string  `json:"title"`
	Type       string  `json:"type"`
	Year       *int    `json:"year,omitempty"`
	ThumbPath  *string `json:"thumb_path,omitempty"`
	ClientName *string `json:"client_name,omitempty"`
	DurationMS *int64  `json:"duration_ms,omitempty"`
	OccurredAt string  `json:"occurred_at"`
}

// List handles GET /api/v1/history?limit=50&offset=0.
func (h *HistoryHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}

	const defaultLimit = 50
	limit := int32(defaultLimit)
	offset := int32(0)
	if v, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 32); err == nil && v > 0 {
		limit = int32(v)
	}
	if v, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 32); err == nil && v >= 0 {
		offset = int32(v)
	}

	var allowed map[uuid.UUID]struct{}
	if h.access != nil {
		var aerr error
		allowed, aerr = h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
		if aerr != nil {
			h.logger.ErrorContext(r.Context(), "history: allowed libraries", "err", aerr)
			respond.InternalError(w, r)
			return
		}
	}
	libAllowed := func(id uuid.UUID) bool {
		if allowed == nil {
			return true
		}
		_, ok := allowed[id]
		return ok
	}

	rows, err := h.db.ListWatchHistory(r.Context(), gen.ListWatchHistoryParams{
		UserID: claims.UserID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "history: list", "err", err)
		respond.InternalError(w, r)
		return
	}

	items := make([]WatchHistoryItem, 0, len(rows))
	for _, row := range rows {
		if !libAllowed(row.LibraryID) {
			continue
		}
		item := WatchHistoryItem{
			ID:      row.ID.String(),
			MediaID: row.MediaID.String(),
			Title:   row.MediaTitle,
			Type:    row.MediaType,
		}

		if row.MediaYear != nil {
			y := int(*row.MediaYear)
			item.Year = &y
		}
		item.ThumbPath = row.MediaThumb
		item.ClientName = row.ClientName
		item.DurationMS = row.DurationMs

		if row.OccurredAt.Valid {
			item.OccurredAt = row.OccurredAt.Time.Format("2006-01-02T15:04:05Z")
		}

		items = append(items, item)
	}

	// The ListWatchHistory query does not return a total count.
	// Use -1 to indicate unknown total; the frontend uses "load more" pagination.
	respond.List(w, r, items, -1, "")
}
