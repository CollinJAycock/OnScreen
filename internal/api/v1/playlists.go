package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
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
// queries are reused from the collections table with type='playlist' or
// type='smart_playlist'; the two playlist-specific queries are
// ListMyPlaylists (user-scoped) and ReorderPlaylistItems (bulk position
// rewrite). Smart playlists store their rule JSON in collections.rules and
// resolve via ListMediaItemsForSmartPlaylist at query time.
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
	ListMediaItemsForSmartPlaylist(ctx context.Context, arg gen.ListMediaItemsForSmartPlaylistParams) ([]gen.ListMediaItemsForSmartPlaylistRow, error)
}

// SmartPlaylistRules is the JSON body stored in collections.rules for
// smart playlists. v2.1 Stage 1 limits the grammar to the filters
// already supported by ListMediaItemsForSmartPlaylist — no nested
// AND/OR groups, no expression language. This keeps the evaluator a
// thin layer over the existing query path and makes the visual rule
// builder (deferred to v2.2) easy to design later. Empty fields are
// treated as "no constraint" — a rule with only Type set returns
// every item of that type the user can see.
type SmartPlaylistRules struct {
	Types     []string `json:"types,omitempty"`      // e.g. ["movie","episode"]
	Genres    []string `json:"genres,omitempty"`     // OR within (any match)
	YearMin   *int     `json:"year_min,omitempty"`
	YearMax   *int     `json:"year_max,omitempty"`
	RatingMin *float64 `json:"rating_min,omitempty"`
	Limit     *int     `json:"limit,omitempty"`      // default 50, max 500
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
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description *string             `json:"description,omitempty"`
	// Type distinguishes static playlists (collection_items) from
	// smart playlists (rules-evaluated). Frontend branches on this
	// to surface a "Smart" badge and gate the manual add/remove
	// buttons.
	Type        string              `json:"type"`
	Rules       *SmartPlaylistRules `json:"rules,omitempty"`
	CreatedAt   string              `json:"created_at"`
	UpdatedAt   string              `json:"updated_at"`
}

func toPlaylistResponse(c gen.Collection) playlistResponse {
	out := playlistResponse{
		ID:          c.ID.String(),
		Name:        c.Name,
		Description: c.Description,
		Type:        c.Type,
		CreatedAt:   c.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt:   c.UpdatedAt.Time.Format(time.RFC3339),
	}
	if len(c.Rules) > 0 {
		var rules SmartPlaylistRules
		if err := json.Unmarshal(c.Rules, &rules); err == nil {
			out.Rules = &rules
		}
	}
	return out
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
		Name        string              `json:"name"`
		Description *string             `json:"description"`
		Rules       *SmartPlaylistRules `json:"rules,omitempty"`
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

	// rules present → smart playlist; resolves at query time. Empty rule
	// objects are accepted (the user can fill them in later via PATCH);
	// the empty-types case in the evaluator returns nothing rather than
	// every item in the library, so a half-configured smart playlist
	// shows as empty in the UI rather than dumping the world.
	collType := "playlist"
	var rulesJSON []byte
	if body.Rules != nil {
		collType = "smart_playlist"
		raw, err := json.Marshal(body.Rules)
		if err != nil {
			respond.InternalError(w, r)
			return
		}
		rulesJSON = raw
	}

	userPG := pgtype.UUID{Bytes: [16]byte(claims.UserID), Valid: true}
	col, err := h.db.CreateCollection(r.Context(), gen.CreateCollectionParams{
		UserID:      userPG,
		Name:        name,
		Description: body.Description,
		Type:        collType,
		Rules:       rulesJSON,
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

// Items handles GET /api/v1/playlists/{id}/items. For static playlists
// returns the rows from collection_items in their stored order; for
// smart playlists evaluates the stored JSON rules at query time, so
// newly-imported items matching the rules show up immediately.
func (h *PlaylistHandler) Items(w http.ResponseWriter, r *http.Request) {
	id, col, ok := h.loadOwned(w, r, "id")
	if !ok {
		return
	}
	allowed, err := h.allowedLibraries(r)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "playlist: allowed libraries", "err", err)
		respond.InternalError(w, r)
		return
	}

	if col.Type == "smart_playlist" {
		out, err := h.resolveSmartPlaylist(r, col, allowed)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "smart playlist resolve", "id", id, "err", err)
			respond.InternalError(w, r)
			return
		}
		respond.List(w, r, out, int64(len(out)), "")
		return
	}

	rows, err := h.db.ListCollectionItems(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list playlist items", "id", id, "err", err)
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

// resolveSmartPlaylist parses the rules JSON, builds a query against
// media_items scoped to the user's accessible libraries, and returns
// matching rows. v2.1 Stage 1 supports type / genre / year range /
// rating min / limit; nested AND/OR groups land alongside the visual
// rule builder in v2.2.
func (h *PlaylistHandler) resolveSmartPlaylist(r *http.Request, col gen.Collection, allowed map[uuid.UUID]struct{}) ([]playlistItemResponse, error) {
	var rules SmartPlaylistRules
	if len(col.Rules) > 0 {
		if err := json.Unmarshal(col.Rules, &rules); err != nil {
			return nil, err
		}
	}
	// Empty types → no rows. Avoids a half-configured playlist returning
	// every item in the library, which is probably not what the user
	// wanted when they left the field blank.
	if len(rules.Types) == 0 {
		return []playlistItemResponse{}, nil
	}

	// Library-access filter applied at the SQL layer via the array param.
	// allowed=nil means admin → no filter; pass every library the user
	// can hit. We query the library list lazily (only on smart-playlist
	// resolution) so static playlists don't pay this cost.
	libIDs, err := h.libraryIDsForUser(r.Context(), allowed)
	if err != nil {
		return nil, err
	}
	if len(libIDs) == 0 {
		return []playlistItemResponse{}, nil
	}

	limit := 50
	if rules.Limit != nil && *rules.Limit > 0 {
		limit = *rules.Limit
		if limit > 500 {
			limit = 500
		}
	}

	params := gen.ListMediaItemsForSmartPlaylistParams{
		LibraryIds:  libIDs,
		Types:       rules.Types,
		ResultLimit: int32(limit),
	}
	// Single genre filter for v2.1 Stage 1 — multiple-genre OR matches
	// would require a different query shape (genre = ANY(rules.genres)).
	// Defer until the visual rule builder needs it.
	if len(rules.Genres) > 0 {
		g := rules.Genres[0]
		params.Genre = &g
	}
	if rules.YearMin != nil {
		v := int32(*rules.YearMin)
		params.YearMin = &v
	}
	if rules.YearMax != nil {
		v := int32(*rules.YearMax)
		params.YearMax = &v
	}
	if rules.RatingMin != nil {
		// pgtype.Numeric uses big.Int + exponent. For our 0–10 rating
		// range, multiply by 100 and use exponent -2 → 7.5 becomes
		// {Int: 750, Exp: -2}. Avoids parsing a string round-trip.
		num := pgtype.Numeric{Valid: true, Exp: -2}
		num.Int = big.NewInt(int64(*rules.RatingMin * 100))
		params.RatingMin = num
	}
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		if rk := maxRatingRankFromClaims(claims.MaxContentRating); rk != nil {
			params.MaxRatingRank = rk
		}
	}

	rows, err := h.db.ListMediaItemsForSmartPlaylist(r.Context(), params)
	if err != nil {
		return nil, err
	}
	out := make([]playlistItemResponse, 0, len(rows))
	for i, row := range rows {
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
			// Smart playlists don't have a stable user-curated order —
			// position mirrors query-result order so the frontend can
			// still iterate without needing a special-case branch.
			Position: int32(i),
		})
	}
	return out, nil
}

// libraryIDsForUser returns the UUIDs the smart-playlist evaluator
// should scope the query to. allowed=nil (admin) → all library ids
// the user could hit; otherwise → just the granted set.
func (h *PlaylistHandler) libraryIDsForUser(_ context.Context, allowed map[uuid.UUID]struct{}) ([]uuid.UUID, error) {
	if allowed == nil {
		// Admin — no library filter. Empty array means "any library."
		// Postgres ANY(ARRAY[]::uuid[]) evaluates to false (no match)
		// so we use a sentinel uuid that won't ever exist. Cleaner
		// than a separate "no-filter" code path.
		return []uuid.UUID{uuid.Nil}, nil
	}
	out := make([]uuid.UUID, 0, len(allowed))
	for id := range allowed {
		out = append(out, id)
	}
	return out, nil
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
