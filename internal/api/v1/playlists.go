package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// PlaylistDB is the subset of DB ops the playlist handler needs. All mutation
// queries are reused from the collections table with type='playlist'; the two
// playlist-specific queries are ListMyPlaylists (user-scoped) and
// ReorderPlaylistItems (bulk position rewrite).
type PlaylistDB interface {
	ListMyPlaylists(ctx context.Context, userID pgtype.UUID) ([]gen.Collection, error)
	GetCollection(ctx context.Context, id uuid.UUID) (gen.Collection, error)
	CreateCollection(ctx context.Context, arg gen.CreateCollectionParams) (gen.Collection, error)
	UpdateCollection(ctx context.Context, arg gen.UpdateCollectionParams) (gen.Collection, error)
	DeleteCollection(ctx context.Context, id uuid.UUID) error
	ListCollectionItems(ctx context.Context, collectionID uuid.UUID) ([]gen.ListCollectionItemsRow, error)
	AddCollectionItem(ctx context.Context, arg gen.AddCollectionItemParams) (gen.CollectionItem, error)
	RemoveCollectionItem(ctx context.Context, arg gen.RemoveCollectionItemParams) error
	ReorderPlaylistItems(ctx context.Context, arg gen.ReorderPlaylistItemsParams) error
}

// PlaylistHandler serves /api/v1/playlists. Playlists live in the collections
// table with type='playlist'; the handler enforces user ownership on every
// mutation so a user can't touch another user's rows.
type PlaylistHandler struct {
	db     PlaylistDB
	access LibraryAccessChecker
	logger *slog.Logger
}

// NewPlaylistHandler wires a handler.
func NewPlaylistHandler(db PlaylistDB, logger *slog.Logger) *PlaylistHandler {
	return &PlaylistHandler{db: db, logger: logger}
}

// WithLibraryAccess enables per-library ACL filtering when listing items.
func (h *PlaylistHandler) WithLibraryAccess(a LibraryAccessChecker) *PlaylistHandler {
	h.access = a
	return h
}

type playlistResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func toPlaylistResponse(c gen.Collection) playlistResponse {
	return playlistResponse{
		ID:          c.ID.String(),
		Name:        c.Name,
		Description: c.Description,
		CreatedAt:   c.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt:   c.UpdatedAt.Time.Format(time.RFC3339),
	}
}

type playlistItemResponse struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Year       *int32   `json:"year,omitempty"`
	Rating     *float64 `json:"rating,omitempty"`
	PosterPath *string  `json:"poster_path,omitempty"`
	DurationMS *int64   `json:"duration_ms,omitempty"`
	Position   int32    `json:"position"`
}

// List handles GET /api/v1/playlists — the caller's playlists only.
func (h *PlaylistHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	userPG := pgtype.UUID{Bytes: [16]byte(claims.UserID), Valid: true}
	rows, err := h.db.ListMyPlaylists(r.Context(), userPG)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list playlists", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]playlistResponse, len(rows))
	for i, c := range rows {
		out[i] = toPlaylistResponse(c)
	}
	respond.Success(w, r, out)
}

// Create handles POST /api/v1/playlists.
func (h *PlaylistHandler) Create(w http.ResponseWriter, r *http.Request) {
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
		Type:        "playlist",
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create playlist", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Created(w, r, toPlaylistResponse(col))
}

// Update handles PATCH /api/v1/playlists/{id} — rename and/or re-describe.
func (h *PlaylistHandler) Update(w http.ResponseWriter, r *http.Request) {
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
		name = col.Name // PATCH semantics: unchanged when omitted
	}
	desc := body.Description
	if desc == nil {
		desc = col.Description
	}
	updated, err := h.db.UpdateCollection(r.Context(), gen.UpdateCollectionParams{
		ID: id, Name: name, Description: desc,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "update playlist", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, toPlaylistResponse(updated))
}

// Delete handles DELETE /api/v1/playlists/{id}.
func (h *PlaylistHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.loadOwned(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.DeleteCollection(r.Context(), id); err != nil {
		h.logger.ErrorContext(r.Context(), "delete playlist", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// Items handles GET /api/v1/playlists/{id}/items.
func (h *PlaylistHandler) Items(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.loadOwned(w, r, "id")
	if !ok {
		return
	}
	rows, err := h.db.ListCollectionItems(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list playlist items", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	allowed, err := h.allowedLibraries(r)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "playlist: allowed libraries", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]playlistItemResponse, 0, len(rows))
	for _, row := range rows {
		if allowed != nil {
			if _, ok := allowed[row.LibraryID]; !ok {
				continue
			}
		}
		var rating *float64
		if f8, err := row.Rating.Float64Value(); err == nil && f8.Valid {
			rating = &f8.Float64
		}
		out = append(out, playlistItemResponse{
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

// AddItem handles POST /api/v1/playlists/{id}/items.
func (h *PlaylistHandler) AddItem(w http.ResponseWriter, r *http.Request) {
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
	if _, err := h.db.AddCollectionItem(r.Context(), gen.AddCollectionItemParams{
		CollectionID: id,
		MediaItemID:  itemID,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "add playlist item", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// RemoveItem handles DELETE /api/v1/playlists/{id}/items/{itemId}.
func (h *PlaylistHandler) RemoveItem(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.loadOwned(w, r, "id")
	if !ok {
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

// Reorder handles PUT /api/v1/playlists/{id}/items/order. Body is a JSON array
// of media_item_ids in the desired order; the server assigns positions 0..N-1.
// Items missing from the array keep their prior position.
func (h *PlaylistHandler) Reorder(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.loadOwned(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		ItemIDs []string `json:"item_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid body")
		return
	}
	ids := make([]uuid.UUID, 0, len(body.ItemIDs))
	seen := make(map[uuid.UUID]struct{}, len(body.ItemIDs))
	for _, s := range body.ItemIDs {
		u, err := uuid.Parse(s)
		if err != nil {
			respond.BadRequest(w, r, "invalid item id in order array")
			return
		}
		if _, dup := seen[u]; dup {
			respond.BadRequest(w, r, "duplicate item id in order array")
			return
		}
		seen[u] = struct{}{}
		ids = append(ids, u)
	}
	if err := h.db.ReorderPlaylistItems(r.Context(), gen.ReorderPlaylistItemsParams{
		CollectionID: id, ItemIds: ids,
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "reorder playlist items", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// loadOwned parses the {id} URL param, fetches the collection row, and
// verifies it's a playlist owned by the caller. Writes a response and
// returns ok=false on any failure; callers just return when !ok.
func (h *PlaylistHandler) loadOwned(w http.ResponseWriter, r *http.Request, param string) (uuid.UUID, gen.Collection, bool) {
	id, err := parseUUID(r, param)
	if err != nil {
		respond.BadRequest(w, r, "invalid playlist id")
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
		h.logger.ErrorContext(r.Context(), "get playlist", "id", id, "err", err)
		respond.InternalError(w, r)
		return uuid.Nil, gen.Collection{}, false
	}
	// Obfuscate non-playlists and foreign-owned rows as 404 — don't leak existence.
	if col.Type != "playlist" || !col.UserID.Valid ||
		uuid.UUID(col.UserID.Bytes) != claims.UserID {
		respond.NotFound(w, r)
		return uuid.Nil, gen.Collection{}, false
	}
	return id, col, true
}

// allowedLibraries returns nil when no filtering should be applied (no ACL
// configured, or caller is admin). Otherwise returns the set of library IDs
// the caller can read.
func (h *PlaylistHandler) allowedLibraries(r *http.Request) (map[uuid.UUID]struct{}, error) {
	if h.access == nil {
		return nil, nil
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return nil, nil // loadOwned already guarantees non-nil claims before Items is reachable
	}
	return h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
}
