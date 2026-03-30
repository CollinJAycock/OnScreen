package v1

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// HubDB defines the database queries the hub handler needs.
type HubDB interface {
	ListContinueWatching(ctx context.Context, arg gen.ListContinueWatchingParams) ([]gen.ListContinueWatchingRow, error)
	ListRecentlyAdded(ctx context.Context, arg gen.ListRecentlyAddedParams) ([]gen.ListRecentlyAddedRow, error)
}

// HubHandler serves the home page hub data.
type HubHandler struct {
	db     HubDB
	logger *slog.Logger
}

// NewHubHandler creates a HubHandler.
func NewHubHandler(db HubDB, logger *slog.Logger) *HubHandler {
	return &HubHandler{db: db, logger: logger}
}

// HubResponse is the combined home page data.
type HubResponse struct {
	ContinueWatching []HubItem `json:"continue_watching"`
	RecentlyAdded    []HubItem `json:"recently_added"`
}

// HubItem is a compact item for hub display.
type HubItem struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Type         string  `json:"type"`
	Year         *int    `json:"year,omitempty"`
	PosterPath   *string `json:"poster_path,omitempty"`
	FanartPath   *string `json:"fanart_path,omitempty"`
	ThumbPath    *string `json:"thumb_path,omitempty"`
	ViewOffsetMS *int64  `json:"view_offset_ms,omitempty"`
	DurationMS   *int64  `json:"duration_ms,omitempty"`
	UpdatedAt    int64   `json:"updated_at"`
}

// Get handles GET /api/v1/hub.
func (h *HubHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}

	out := HubResponse{
		ContinueWatching: []HubItem{},
		RecentlyAdded:    []HubItem{},
	}

	// Continue watching — items the user has in progress.
	cwRows, err := h.db.ListContinueWatching(r.Context(), gen.ListContinueWatchingParams{
		UserID: claims.UserID,
		Limit:  20,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "hub: continue watching", "err", err)
	} else {
		for _, row := range cwRows {
			year := intPtrFrom32(row.Year)
			offset := row.ViewOffset
			out.ContinueWatching = append(out.ContinueWatching, HubItem{
				ID:           row.ID.String(),
				Title:        row.Title,
				Type:         row.Type,
				Year:         year,
				PosterPath:   row.FallbackPoster,
				FanartPath:   row.FanartPath,
				ThumbPath:    row.ThumbPath,
				ViewOffsetMS: &offset,
				DurationMS:   row.DurationMs,
				UpdatedAt:    timestamptzToMilli(row.UpdatedAt),
			})
		}
	}

	// Recently added — newest items across all libraries.
	// Fetch extra rows so we still have ≥20 after deduplication.
	raRows, err := h.db.ListRecentlyAdded(r.Context(), gen.ListRecentlyAddedParams{
		Limit: 40,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "hub: recently added", "err", err)
	} else {
		seen := make(map[string]bool)
		for _, row := range raRows {
			// Deduplicate by title+type (handles duplicate media_items rows).
			key := row.Type + "|" + row.Title
			if seen[key] {
				continue
			}
			seen[key] = true
			year := intPtrFrom32(row.Year)
			out.RecentlyAdded = append(out.RecentlyAdded, HubItem{
				ID:         row.ID.String(),
				Title:      row.Title,
				Type:       row.Type,
				Year:       year,
				PosterPath: row.PosterPath,
				FanartPath: row.FanartPath,
				DurationMS: row.DurationMs,
				UpdatedAt:  timestamptzToMilli(row.UpdatedAt),
			})
			if len(out.RecentlyAdded) >= 20 {
				break
			}
		}
	}

	respond.Success(w, r, out)
}

func intPtrFrom32(v *int32) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func timestamptzToMilli(ts pgtype.Timestamptz) int64 {
	if !ts.Valid {
		return 0
	}
	return ts.Time.UnixMilli()
}
