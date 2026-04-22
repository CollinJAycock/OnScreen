package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// FavoritesDB defines the database operations the favorites handler needs.
type FavoritesDB interface {
	AddFavorite(ctx context.Context, arg gen.AddFavoriteParams) error
	RemoveFavorite(ctx context.Context, arg gen.RemoveFavoriteParams) error
	IsFavorite(ctx context.Context, arg gen.IsFavoriteParams) (bool, error)
	ListFavorites(ctx context.Context, arg gen.ListFavoritesParams) ([]gen.ListFavoritesRow, error)
	CountFavorites(ctx context.Context, userID uuid.UUID) (int64, error)
}

// FavoritesHandler serves favorites endpoints.
type FavoritesHandler struct {
	db     FavoritesDB
	access LibraryAccessChecker
	logger *slog.Logger
}

// NewFavoritesHandler creates a FavoritesHandler.
func NewFavoritesHandler(db FavoritesDB, logger *slog.Logger) *FavoritesHandler {
	return &FavoritesHandler{db: db, logger: logger}
}

// WithLibraryAccess enables per-user library filtering on favorites.
func (h *FavoritesHandler) WithLibraryAccess(a LibraryAccessChecker) *FavoritesHandler {
	h.access = a
	return h
}

// FavoriteItemResponse is the JSON shape for a single favorited item.
type FavoriteItemResponse struct {
	ID          string  `json:"id"`
	LibraryID   string  `json:"library_id"`
	Type        string  `json:"type"`
	Title       string  `json:"title"`
	Year        *int32  `json:"year,omitempty"`
	Summary     *string `json:"summary,omitempty"`
	PosterPath  *string `json:"poster_path,omitempty"`
	ThumbPath   *string `json:"thumb_path,omitempty"`
	DurationMS  *int64  `json:"duration_ms,omitempty"`
	FavoritedAt int64   `json:"favorited_at"`
}

// List handles GET /api/v1/favorites.
func (h *FavoritesHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}

	page := respond.ParsePagination(r, 50, 200)
	limit, offset := page.Limit, page.Offset

	var maxRank *int32
	if claims.MaxContentRating != "" {
		rank := int32(contentrating.Rank(claims.MaxContentRating))
		maxRank = &rank
	}

	var allowed map[uuid.UUID]struct{}
	if h.access != nil {
		var aerr error
		allowed, aerr = h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
		if aerr != nil {
			h.logger.ErrorContext(r.Context(), "favorites: allowed libraries", "err", aerr)
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

	rows, err := h.db.ListFavorites(r.Context(), gen.ListFavoritesParams{
		UserID:        claims.UserID,
		Limit:         limit,
		Offset:        offset,
		MaxRatingRank: maxRank,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list favorites", "err", err)
		respond.InternalError(w, r)
		return
	}

	out := make([]FavoriteItemResponse, 0, len(rows))
	for _, row := range rows {
		if !libAllowed(row.LibraryID) {
			continue
		}
		var favAt int64
		if row.FavoritedAt.Valid {
			favAt = row.FavoritedAt.Time.UnixMilli()
		}
		out = append(out, FavoriteItemResponse{
			ID:          row.ID.String(),
			LibraryID:   row.LibraryID.String(),
			Type:        row.Type,
			Title:       row.Title,
			Year:        row.Year,
			Summary:     row.Summary,
			PosterPath:  row.PosterPath,
			ThumbPath:   row.ThumbPath,
			DurationMS:  row.DurationMs,
			FavoritedAt: favAt,
		})
	}

	total, _ := h.db.CountFavorites(r.Context(), claims.UserID)
	respond.List(w, r, out, total, "")
}

// Add handles POST /api/v1/items/{id}/favorite.
func (h *FavoritesHandler) Add(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	if err := h.db.AddFavorite(r.Context(), gen.AddFavoriteParams{
		UserID:  claims.UserID,
		MediaID: id,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "add favorite", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// Remove handles DELETE /api/v1/items/{id}/favorite.
func (h *FavoritesHandler) Remove(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	if err := h.db.RemoveFavorite(r.Context(), gen.RemoveFavoriteParams{
		UserID:  claims.UserID,
		MediaID: id,
	}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		h.logger.ErrorContext(r.Context(), "remove favorite", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}
