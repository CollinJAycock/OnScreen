package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// CollectionDB defines the DB operations the collections handler needs.
type CollectionDB interface {
	ListCollections(ctx context.Context, userID pgtype.UUID) ([]gen.Collection, error)
	GetCollection(ctx context.Context, id uuid.UUID) (gen.Collection, error)
	CreateCollection(ctx context.Context, arg gen.CreateCollectionParams) (gen.Collection, error)
	UpdateCollection(ctx context.Context, arg gen.UpdateCollectionParams) (gen.Collection, error)
	DeleteCollection(ctx context.Context, id uuid.UUID) error
	ListCollectionItems(ctx context.Context, collectionID uuid.UUID) ([]gen.ListCollectionItemsRow, error)
	AddCollectionItem(ctx context.Context, arg gen.AddCollectionItemParams) (gen.CollectionItem, error)
	RemoveCollectionItem(ctx context.Context, arg gen.RemoveCollectionItemParams) error
	ListAutoGenreCollections(ctx context.Context) ([]gen.Collection, error)
	ListItemsByGenre(ctx context.Context, arg gen.ListItemsByGenreParams) ([]gen.ListItemsByGenreRow, error)
	CountItemsByGenre(ctx context.Context, genres []string) (int64, error)
	ListDistinctGenres(ctx context.Context, libraryID uuid.UUID) ([]string, error)
}

// CollectionHandler handles /api/v1/collections.
type CollectionHandler struct {
	db     CollectionDB
	access LibraryAccessChecker
	logger *slog.Logger
}

// NewCollectionHandler creates a CollectionHandler.
func NewCollectionHandler(db CollectionDB, logger *slog.Logger) *CollectionHandler {
	return &CollectionHandler{db: db, logger: logger}
}

// WithLibraryAccess enables per-user library filtering on collection items.
func (h *CollectionHandler) WithLibraryAccess(a LibraryAccessChecker) *CollectionHandler {
	h.access = a
	return h
}

type collectionResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	Type        string  `json:"type"`
	Genre       *string `json:"genre,omitempty"`
	PosterPath  *string `json:"poster_path,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

func toCollectionResponse(c gen.Collection) collectionResponse {
	return collectionResponse{
		ID:          c.ID.String(),
		Name:        c.Name,
		Description: c.Description,
		Type:        c.Type,
		Genre:       c.Genre,
		PosterPath:  c.PosterPath,
		CreatedAt:   c.CreatedAt.Time.Format(time.RFC3339),
	}
}

type collectionItemResponse struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Year       *int32   `json:"year,omitempty"`
	Rating     *float64 `json:"rating,omitempty"`
	PosterPath *string  `json:"poster_path,omitempty"`
	DurationMS *int64   `json:"duration_ms,omitempty"`
	Position   int32    `json:"position"`
}

// List handles GET /api/v1/collections.
func (h *CollectionHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	userPG := pgtype.UUID{Bytes: [16]byte(claims.UserID), Valid: true}

	cols, err := h.db.ListCollections(r.Context(), userPG)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list collections", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]collectionResponse, len(cols))
	for i, c := range cols {
		out[i] = toCollectionResponse(c)
	}
	respond.Success(w, r, out)
}

// Get handles GET /api/v1/collections/{id}.
func (h *CollectionHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid collection id")
		return
	}
	col, err := h.db.GetCollection(r.Context(), id)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	if !h.requireOwnerOrAdmin(w, r, col) {
		return
	}
	respond.Success(w, r, toCollectionResponse(col))
}

// requireOwnerOrAdmin returns true when the caller may mutate or read the
// given collection. Auto-generated collections (e.g. type=auto_genre) have
// no UserID and are treated as server-owned: any authenticated user may
// read them, but only admins may mutate. Per-user collections are gated to
// their owner, with admins permitted as a service-level override. On a
// denial, this writes the response (404 to avoid existence leaks) and
// returns false.
func (h *CollectionHandler) requireOwnerOrAdmin(w http.ResponseWriter, r *http.Request, col gen.Collection) bool {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return false
	}
	if claims.IsAdmin {
		return true
	}
	if !col.UserID.Valid {
		// Server-owned (auto-genre etc.) — non-admins may read but not mutate.
		// The mutating handlers wrap this helper so getting here from one of
		// them already implies admin via the IsAdmin branch above; for Get/
		// Items the read is allowed, so return true.
		return true
	}
	if uuid.UUID(col.UserID.Bytes) != claims.UserID {
		respond.NotFound(w, r)
		return false
	}
	return true
}

// requireOwnerOrAdminMutate is the mutate-only variant: it additionally
// rejects auto-generated (server-owned) collections for non-admin callers.
func (h *CollectionHandler) requireOwnerOrAdminMutate(w http.ResponseWriter, r *http.Request, col gen.Collection) bool {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return false
	}
	if claims.IsAdmin {
		return true
	}
	if !col.UserID.Valid || uuid.UUID(col.UserID.Bytes) != claims.UserID {
		respond.NotFound(w, r)
		return false
	}
	return true
}

// Create handles POST /api/v1/collections.
func (h *CollectionHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		respond.BadRequest(w, r, "name is required")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	userPG := pgtype.UUID{Bytes: [16]byte(claims.UserID), Valid: true}

	col, err := h.db.CreateCollection(r.Context(), gen.CreateCollectionParams{
		UserID:      userPG,
		Name:        body.Name,
		Description: body.Description,
		Type:        "playlist",
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create collection", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Created(w, r, toCollectionResponse(col))
}

// Update handles PATCH /api/v1/collections/{id}.
func (h *CollectionHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid collection id")
		return
	}
	existing, err := h.db.GetCollection(r.Context(), id)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	if !h.requireOwnerOrAdminMutate(w, r, existing) {
		return
	}
	var body struct {
		Name        string  `json:"name"`
		Description *string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid body")
		return
	}
	col, err := h.db.UpdateCollection(r.Context(), gen.UpdateCollectionParams{
		ID:          id,
		Name:        body.Name,
		Description: body.Description,
	})
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	respond.Success(w, r, toCollectionResponse(col))
}

// Delete handles DELETE /api/v1/collections/{id}.
func (h *CollectionHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid collection id")
		return
	}
	existing, err := h.db.GetCollection(r.Context(), id)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	if !h.requireOwnerOrAdminMutate(w, r, existing) {
		return
	}
	if err := h.db.DeleteCollection(r.Context(), id); err != nil {
		respond.NotFound(w, r)
		return
	}
	respond.NoContent(w)
}

// Items handles GET /api/v1/collections/{id}/items.
func (h *CollectionHandler) Items(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid collection id")
		return
	}

	col, err := h.db.GetCollection(r.Context(), id)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	if !h.requireOwnerOrAdmin(w, r, col) {
		return
	}

	// Pre-compute allowed library set. Nil means admin → no filtering.
	var allowed map[uuid.UUID]struct{}
	if h.access != nil {
		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			respond.Unauthorized(w, r)
			return
		}
		var aerr error
		allowed, aerr = h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
		if aerr != nil {
			h.logger.ErrorContext(r.Context(), "collections: allowed libraries", "err", aerr)
			respond.InternalError(w, r)
			return
		}
	}
	libAllowed := func(lid uuid.UUID) bool {
		if allowed == nil {
			return true
		}
		_, ok := allowed[lid]
		return ok
	}

	// Auto-genre collections query media_items directly.
	if col.Type == "auto_genre" && col.Genre != nil {
		limit := int32(50)
		offset := int32(0)
		if v, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 32); err == nil && v > 0 {
			limit = int32(v)
		}
		if v, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 32); err == nil && v >= 0 {
			offset = int32(v)
		}
		rows, err := h.db.ListItemsByGenre(r.Context(), gen.ListItemsByGenreParams{
			Genres: []string{*col.Genre}, Limit: limit, Offset: offset,
		})
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list items by genre", "genre", *col.Genre, "err", err)
			respond.InternalError(w, r)
			return
		}
		total, _ := h.db.CountItemsByGenre(r.Context(), []string{*col.Genre})
		out := make([]collectionItemResponse, 0, len(rows))
		for _, row := range rows {
			if !libAllowed(row.LibraryID) {
				continue
			}
			var rating *float64
			if f8, err := row.Rating.Float64Value(); err == nil && f8.Valid {
				rating = &f8.Float64
			}
			out = append(out, collectionItemResponse{
				ID:         row.ID.String(),
				Title:      row.Title,
				Type:       row.Type,
				Year:       row.Year,
				Rating:     rating,
				PosterPath: row.PosterPath,
				DurationMS: row.DurationMs,
			})
		}
		respond.List(w, r, out, total, "")
		return
	}

	// Playlist — read from collection_items join.
	rows, err := h.db.ListCollectionItems(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list collection items", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]collectionItemResponse, 0, len(rows))
	for _, row := range rows {
		if !libAllowed(row.LibraryID) {
			continue
		}
		var rating *float64
		if f8, err := row.Rating.Float64Value(); err == nil && f8.Valid {
			rating = &f8.Float64
		}
		out = append(out, collectionItemResponse{
			ID:         row.ID.String(),
			Title:      row.Title,
			Type:       row.Type,
			Year:       row.Year,
			Rating:     rating,
			PosterPath: row.PosterPath,
			DurationMS: row.DurationMs,
			Position:   row.Position,
		})
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// AddItem handles POST /api/v1/collections/{id}/items.
func (h *CollectionHandler) AddItem(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid collection id")
		return
	}
	col, err := h.db.GetCollection(r.Context(), id)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	if !h.requireOwnerOrAdminMutate(w, r, col) {
		return
	}
	var body struct {
		MediaItemID string `json:"media_item_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid body")
		return
	}
	itemID, err := uuid.Parse(body.MediaItemID)
	if err != nil {
		respond.BadRequest(w, r, "invalid media_item_id")
		return
	}
	_, err = h.db.AddCollectionItem(r.Context(), gen.AddCollectionItemParams{
		CollectionID: id,
		MediaItemID:  itemID,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "add collection item", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// RemoveItem handles DELETE /api/v1/collections/{id}/items/{itemId}.
func (h *CollectionHandler) RemoveItem(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid collection id")
		return
	}
	col, err := h.db.GetCollection(r.Context(), id)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	if !h.requireOwnerOrAdminMutate(w, r, col) {
		return
	}
	itemID, err := uuid.Parse(chi.URLParam(r, "itemId"))
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	if err := h.db.RemoveCollectionItem(r.Context(), gen.RemoveCollectionItemParams{
		CollectionID: id,
		MediaItemID:  itemID,
	}); err != nil {
		respond.NotFound(w, r)
		return
	}
	respond.NoContent(w)
}
