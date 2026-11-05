package v1

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ItemBulkAdminDB exposes the narrow database queries the library-wide
// admin lists need. Defined here (not as a method on the larger
// ItemMediaService) so the day-to-day service interface stays small.
type ItemBulkAdminDB interface {
	ListUnmatchedTopLevelItems(ctx context.Context, arg gen.ListUnmatchedTopLevelItemsParams) ([]gen.ListUnmatchedTopLevelItemsRow, error)
	ListMediaItemsMissingArt(ctx context.Context, limit int32) ([]gen.ListMediaItemsMissingArtRow, error)
}

// ItemBulkAdminHandler serves the admin lists behind Settings > Unmatched
// (GET /api/v1/admin/items/unmatched) and Settings > Missing art
// (GET /api/v1/admin/items/missing-art). Fixing an item is per item (Fix
// Match). v3 removed the bulk POST /admin/items/re-enrich-unmatched, which
// no client called (docs/v3-api-audit.md).
type ItemBulkAdminHandler struct {
	db     ItemBulkAdminDB
	logger *slog.Logger
}

// NewItemBulkAdminHandler constructs the handler.
func NewItemBulkAdminHandler(db ItemBulkAdminDB, logger *slog.Logger) *ItemBulkAdminHandler {
	return &ItemBulkAdminHandler{db: db, logger: logger}
}

// unmatchedItem is the per-row shape for GET /admin/items/unmatched.
// Mirrors the fields the frontend needs to render the "Fix Match" tray:
// ID for the per-item match call, library_id for scoping, type +
// title + year for display.
type unmatchedItem struct {
	ID        string `json:"id"`
	LibraryID string `json:"library_id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Year      *int32 `json:"year,omitempty"`
}

type unmatchedListResponse struct {
	Items []unmatchedItem `json:"items"`
	Total int             `json:"total"`
}

// missingArtItem is the per-row shape for GET /admin/items/missing-art.
// Mirrors unmatchedItem but adds tmdb_id so the missing-art tray can
// kick the existing TMDB poster picker (POST /items/{id}/posters
// needs a tmdb id) without an extra round-trip per row.
type missingArtItem struct {
	ID        string `json:"id"`
	LibraryID string `json:"library_id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Year      *int32 `json:"year,omitempty"`
	TMDBID    *int   `json:"tmdb_id,omitempty"`
}

type missingArtListResponse struct {
	Items []missingArtItem `json:"items"`
	Total int              `json:"total"`
}

// ListMissingArt handles GET /api/v1/admin/items/missing-art. Returns
// every top-level movie/show with no poster_path. Used by the admin
// "Set poster" tray so the operator can pick a TMDB poster variant
// or paste a URL for items where the auto-fetched poster came back
// empty. List-only — mutations go through the existing /items/{id}/
// posters and /items/{id}/poster endpoints.
func (h *ItemBulkAdminHandler) ListMissingArt(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}

	limit := respond.ParseLimit(r, 200, 1000)
	rows, err := h.db.ListMediaItemsMissingArt(ctx, limit)
	if err != nil {
		h.logger.ErrorContext(ctx, "list items missing art", "err", err)
		respond.InternalError(w, r)
		return
	}

	items := make([]missingArtItem, 0, len(rows))
	for _, row := range rows {
		entry := missingArtItem{
			ID:        row.ID.String(),
			LibraryID: row.LibraryID.String(),
			Type:      row.Type,
			Title:     row.Title,
		}
		if row.Year != nil {
			y := *row.Year
			entry.Year = &y
		}
		if row.TmdbID != nil {
			v := int(*row.TmdbID)
			entry.TMDBID = &v
		}
		items = append(items, entry)
	}

	respond.Success(w, r, missingArtListResponse{
		Items: items,
		Total: len(items),
	})
}

// ListUnmatched handles GET /api/v1/admin/items/unmatched. Returns
// every top-level movie/show with no provider IDs (tmdb / tvdb /
// imdb). Used by the admin Fix Match tray as the data feed; the
// per-item search + apply path uses the existing
// /api/v1/items/{id}/match/search and /api/v1/items/{id}/match
// endpoints (matchShow / matchMovie internally), so this endpoint
// only does the list — no mutation.
func (h *ItemBulkAdminHandler) ListUnmatched(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}

	limit := respond.ParseLimit(r, 200, 1000)

	params := gen.ListUnmatchedTopLevelItemsParams{
		ResultLimit: limit,
	}
	if raw := r.URL.Query().Get("library_id"); raw != "" {
		libID, err := uuid.Parse(raw)
		if err != nil {
			respond.BadRequest(w, r, "invalid library_id")
			return
		}
		params.LibraryID = pgtype.UUID{Bytes: libID, Valid: true}
	}

	rows, err := h.db.ListUnmatchedTopLevelItems(ctx, params)
	if err != nil {
		h.logger.ErrorContext(ctx, "list unmatched items", "err", err)
		respond.InternalError(w, r)
		return
	}

	items := make([]unmatchedItem, 0, len(rows))
	for _, row := range rows {
		entry := unmatchedItem{
			ID:        row.ID.String(),
			LibraryID: row.LibraryID.String(),
			Type:      row.Type,
			Title:     row.Title,
		}
		if row.Year != nil {
			y := *row.Year
			entry.Year = &y
		}
		items = append(items, entry)
	}

	respond.Success(w, r, unmatchedListResponse{
		Items: items,
		Total: len(items),
	})
}
