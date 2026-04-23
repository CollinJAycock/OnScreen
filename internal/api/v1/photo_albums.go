package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// PhotoAlbumDB is the slice of generated DB ops the photo-album handler
// needs. Album rows live in the existing `collections` table with
// type='photo_album', so all the create/update/delete CRUD reuses the
// collection queries — only the list / item-list queries are
// photo-album-specific (they join photo_metadata for taken_at + dimensions).
type PhotoAlbumDB interface {
	ListMyPhotoAlbums(ctx context.Context, userID pgtype.UUID) ([]gen.ListMyPhotoAlbumsRow, error)
	ListPhotoAlbumItems(ctx context.Context, collectionID uuid.UUID) ([]gen.ListPhotoAlbumItemsRow, error)
	GetCollection(ctx context.Context, id uuid.UUID) (gen.Collection, error)
	CreateCollection(ctx context.Context, arg gen.CreateCollectionParams) (gen.Collection, error)
	UpdateCollection(ctx context.Context, arg gen.UpdateCollectionParams) (gen.Collection, error)
	DeleteCollection(ctx context.Context, id uuid.UUID) error
	AddCollectionItem(ctx context.Context, arg gen.AddCollectionItemParams) (gen.CollectionItem, error)
	RemoveCollectionItem(ctx context.Context, arg gen.RemoveCollectionItemParams) error
	GetMediaItem(ctx context.Context, id uuid.UUID) (gen.GetMediaItemRow, error)
}

// PhotoAlbumHandler serves /api/v1/photo-albums. Each endpoint enforces
// owner-only access — a user cannot see, modify, or list items of
// another user's album. Items added must be type='photo'; movies/episodes
// are rejected at the AddItem boundary so we don't end up with
// silently-filtered membership rows.
type PhotoAlbumHandler struct {
	db     PhotoAlbumDB
	access LibraryAccessChecker
	logger *slog.Logger
}

// NewPhotoAlbumHandler wires a handler.
func NewPhotoAlbumHandler(db PhotoAlbumDB, logger *slog.Logger) *PhotoAlbumHandler {
	return &PhotoAlbumHandler{db: db, logger: logger}
}

// WithLibraryAccess enables per-library ACL filtering when listing items.
// Without it, all items in an album are returned regardless of the
// caller's library grants.
func (h *PhotoAlbumHandler) WithLibraryAccess(a LibraryAccessChecker) *PhotoAlbumHandler {
	h.access = a
	return h
}

type photoAlbumResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	CoverPath   *string `json:"cover_path,omitempty"`
	ItemCount   int64   `json:"item_count"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func toPhotoAlbumListResponse(r gen.ListMyPhotoAlbumsRow) photoAlbumResponse {
	return photoAlbumResponse{
		ID:          r.ID.String(),
		Name:        r.Name,
		Description: r.Description,
		CoverPath:   r.CoverPath,
		ItemCount:   r.ItemCount,
		CreatedAt:   r.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt:   r.UpdatedAt.Time.Format(time.RFC3339),
	}
}

// toPhotoAlbumDetailResponse converts a base collection row into the
// response shape used by Create/Update/Get. ItemCount and CoverPath are
// zero-valued — those come from the list query only.
func toPhotoAlbumDetailResponse(c gen.Collection) photoAlbumResponse {
	return photoAlbumResponse{
		ID:          c.ID.String(),
		Name:        c.Name,
		Description: c.Description,
		CreatedAt:   c.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt:   c.UpdatedAt.Time.Format(time.RFC3339),
	}
}

type photoAlbumItemResponse struct {
	ID          string     `json:"id"`
	LibraryID   string     `json:"library_id"`
	Title       string     `json:"title"`
	PosterPath  *string    `json:"poster_path,omitempty"`
	TakenAt     *time.Time `json:"taken_at,omitempty"`
	CameraMake  *string    `json:"camera_make,omitempty"`
	CameraModel *string    `json:"camera_model,omitempty"`
	Width       *int32     `json:"width,omitempty"`
	Height      *int32     `json:"height,omitempty"`
	Orientation *int32     `json:"orientation,omitempty"`
	AddedAt     time.Time  `json:"added_at"`
}

// List handles GET /api/v1/photo-albums — caller's albums only.
func (h *PhotoAlbumHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	userPG := pgtype.UUID{Bytes: [16]byte(claims.UserID), Valid: true}
	rows, err := h.db.ListMyPhotoAlbums(r.Context(), userPG)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list photo albums", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]photoAlbumResponse, len(rows))
	for i, row := range rows {
		out[i] = toPhotoAlbumListResponse(row)
	}
	respond.Success(w, r, out)
}

// Create handles POST /api/v1/photo-albums.
func (h *PhotoAlbumHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
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
	name := strings.TrimSpace(body.Name)
	if name == "" {
		respond.BadRequest(w, r, "name is required")
		return
	}
	userPG := pgtype.UUID{Bytes: [16]byte(claims.UserID), Valid: true}
	col, err := h.db.CreateCollection(r.Context(), gen.CreateCollectionParams{
		UserID:      userPG,
		Name:        name,
		Description: body.Description,
		Type:        "photo_album",
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create photo album", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Created(w, r, toPhotoAlbumDetailResponse(col))
}

// Update handles PATCH /api/v1/photo-albums/{id} — rename and/or re-describe.
func (h *PhotoAlbumHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, col, ok := h.loadOwned(w, r, "id")
	if !ok {
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
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = col.Name
	}
	desc := body.Description
	if desc == nil {
		desc = col.Description
	}
	updated, err := h.db.UpdateCollection(r.Context(), gen.UpdateCollectionParams{
		ID: id, Name: name, Description: desc,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "update photo album", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, toPhotoAlbumDetailResponse(updated))
}

// Delete handles DELETE /api/v1/photo-albums/{id}.
func (h *PhotoAlbumHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.loadOwned(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.DeleteCollection(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "delete photo album", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// Items handles GET /api/v1/photo-albums/{id}/items.
func (h *PhotoAlbumHandler) Items(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.loadOwned(w, r, "id")
	if !ok {
		return
	}
	rows, err := h.db.ListPhotoAlbumItems(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list photo album items", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	allowed, err := h.allowedLibraries(r)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "photo album: allowed libraries", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]photoAlbumItemResponse, 0, len(rows))
	for _, row := range rows {
		if allowed != nil {
			if _, ok := allowed[row.LibraryID]; !ok {
				continue
			}
		}
		var takenAt *time.Time
		if row.TakenAt.Valid {
			t := row.TakenAt.Time
			takenAt = &t
		}
		out = append(out, photoAlbumItemResponse{
			ID:          row.ID.String(),
			LibraryID:   row.LibraryID.String(),
			Title:       row.Title,
			PosterPath:  row.PosterPath,
			TakenAt:     takenAt,
			CameraMake:  row.CameraMake,
			CameraModel: row.CameraModel,
			Width:       row.Width,
			Height:      row.Height,
			Orientation: row.Orientation,
			AddedAt:     row.AddedAt.Time,
		})
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// AddItem handles POST /api/v1/photo-albums/{id}/items. The media item
// must be type='photo'; non-photo items are rejected with 400 rather than
// silently filtered, so the caller knows the add didn't take.
func (h *PhotoAlbumHandler) AddItem(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.loadOwned(w, r, "id")
	if !ok {
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
	mi, err := h.db.GetMediaItem(r.Context(), itemID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get media item for photo album add", "err", err)
		respond.InternalError(w, r)
		return
	}
	if mi.Type != "photo" {
		respond.BadRequest(w, r, "only photo items can be added to a photo album")
		return
	}
	if _, err := h.db.AddCollectionItem(r.Context(), gen.AddCollectionItemParams{
		CollectionID: id,
		MediaItemID:  itemID,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "add photo album item", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// RemoveItem handles DELETE /api/v1/photo-albums/{id}/items/{itemId}.
func (h *PhotoAlbumHandler) RemoveItem(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.loadOwned(w, r, "id")
	if !ok {
		return
	}
	itemID, err := parseUUID(r, "itemId")
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

// loadOwned parses the {id} URL param, fetches the collection row, and
// verifies it's a photo album owned by the caller. Writes a response and
// returns ok=false on any failure; callers just return when !ok.
func (h *PhotoAlbumHandler) loadOwned(w http.ResponseWriter, r *http.Request, param string) (uuid.UUID, gen.Collection, bool) {
	id, err := parseUUID(r, param)
	if err != nil {
		respond.BadRequest(w, r, "invalid album id")
		return uuid.Nil, gen.Collection{}, false
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return uuid.Nil, gen.Collection{}, false
	}
	col, err := h.db.GetCollection(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.NotFound(w, r)
			return uuid.Nil, gen.Collection{}, false
		}
		h.logger.ErrorContext(r.Context(), "get photo album", "id", id, "err", err)
		respond.InternalError(w, r)
		return uuid.Nil, gen.Collection{}, false
	}
	// Obfuscate non-albums and foreign-owned rows as 404 — don't leak existence.
	if col.Type != "photo_album" || !col.UserID.Valid ||
		uuid.UUID(col.UserID.Bytes) != claims.UserID {
		respond.NotFound(w, r)
		return uuid.Nil, gen.Collection{}, false
	}
	return id, col, true
}

func (h *PhotoAlbumHandler) allowedLibraries(r *http.Request) (map[uuid.UUID]struct{}, error) {
	if h.access == nil {
		return nil, nil
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return nil, nil
	}
	return h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
}
