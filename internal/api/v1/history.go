package v1

import (
	"context"
	"log/slog"
	"net/http"

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
	epDB   EpisodePosterDB
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

// WithEpisodePoster wires the episode → show poster substitution.
// Honours the per-user episode_use_show_poster preference.
func (h *HistoryHandler) WithEpisodePoster(db EpisodePosterDB) *HistoryHandler {
	h.epDB = db
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

	page := respond.ParsePagination(r, 50, 200)
	limit, offset := page.Limit, page.Offset

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
		UserID:        claims.UserID,
		Lim:           limit,
		Off:           offset,
		MaxRatingRank: maxRatingRankFromClaims(claims.MaxContentRating),
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

	// Episode-poster substitution. History references the underlying
	// media via media_id, so we collect those IDs (not the history-row
	// IDs) for the lookup.
	if h.epDB != nil {
		var epIDs []uuid.UUID
		for _, it := range items {
			if it.Type == "episode" {
				if id, err := uuid.Parse(it.MediaID); err == nil {
					epIDs = append(epIDs, id)
				}
			}
		}
		if posters := resolveEpisodeShowPosters(r.Context(), h.epDB, claims.UserID, epIDs); len(posters) > 0 {
			for i := range items {
				if items[i].Type != "episode" {
					continue
				}
				if id, err := uuid.Parse(items[i].MediaID); err == nil {
					if p, ok := posters[id]; ok {
						pp := p
						items[i].ThumbPath = &pp
					}
				}
			}
		}
	}

	// The ListWatchHistory query does not return a total count.
	// Use -1 to indicate unknown total; the frontend uses "load more" pagination.
	respond.List(w, r, items, -1, "")
}
