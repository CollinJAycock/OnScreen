package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// collectionTypeManual is the collections.type of a movie / TV collection an
// admin curates (migration 00037). Server-owned (user_id NULL), shared with
// every caller who can see at least one of its members.
const collectionTypeManual = "manual"

// maxBulkCollectionAdd bounds one POST /collections/{id}/items body.
const maxBulkCollectionAdd = 500

// manualMemberTypes are the item types a manual collection holds.
var manualMemberTypes = map[string]bool{"movie": true, "show": true}

// validItemOrder reports whether s is a manual collection listing order.
func validItemOrder(s string) bool {
	return s == "custom" || s == "release" || s == "title"
}

// CollectionManualDB is the slice of generated queries manual collections
// need. Separate from CollectionDB so existing fakes keep compiling; wired via
// WithManual. ReorderPlaylistItems is the playlists' position rewrite, which
// works on any collection.
type CollectionManualDB interface {
	CreateManualCollection(ctx context.Context, arg gen.CreateManualCollectionParams) (gen.Collection, error)
	SetCollectionItemOrder(ctx context.Context, arg gen.SetCollectionItemOrderParams) error
	SetCollectionPosterItem(ctx context.Context, arg gen.SetCollectionPosterItemParams) error
	ListVisibleManualCollections(ctx context.Context, arg gen.ListVisibleManualCollectionsParams) ([]gen.ListVisibleManualCollectionsRow, error)
	ListLibraryManualCollections(ctx context.Context, arg gen.ListLibraryManualCollectionsParams) ([]gen.ListLibraryManualCollectionsRow, error)
	ListManualCollectionsForItem(ctx context.Context, mediaItemID uuid.UUID) ([]gen.ListManualCollectionsForItemRow, error)
	ReorderPlaylistItems(ctx context.Context, arg gen.ReorderPlaylistItemsParams) error
}

// WithManual enables manual collections: creating them, their settings and
// order, their covers and visibility in the listings.
func (h *CollectionHandler) WithManual(db CollectionManualDB) *CollectionHandler {
	h.manual = db
	return h
}

// manualCover is what the listing shows for a manual collection the caller
// can see: its visible member count and a cover from a visible member.
type manualCover struct {
	itemCount int64
	poster    *string
}

// visibleManualCovers returns, for each manual collection with a member the
// caller may see, its cover. Unrestricted callers get every non-empty one.
func (h *CollectionHandler) visibleManualCovers(ctx context.Context, v viewerScope) (map[uuid.UUID]manualCover, error) {
	rows, err := h.manual.ListVisibleManualCollections(ctx, gen.ListVisibleManualCollectionsParams{
		LibraryIds:    v.allowedLibIDs,
		MaxRatingRank: v.maxRank,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]manualCover, len(rows))
	for _, row := range rows {
		c := manualCover{itemCount: row.ItemCount}
		if row.CoverPosterPath != "" {
			p := row.CoverPosterPath
			c.poster = &p
		}
		out[row.ID] = c
	}
	return out, nil
}

// decorateManualList drops the manual collections a non-admin caller can't
// see into (no visible member: the name alone could describe what's hidden)
// and attaches each one's cover and visible count. Admins keep empty ones so
// they can fill them.
func (h *CollectionHandler) decorateManualList(w http.ResponseWriter, r *http.Request, out []collectionResponse) ([]collectionResponse, bool) {
	has := false
	for _, c := range out {
		if c.Type == collectionTypeManual {
			has = true
			break
		}
	}
	if !has {
		return out, true
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if h.manual == nil {
		// Not wired: fail closed, as for franchises.
		kept := out[:0]
		for _, c := range out {
			if c.Type != collectionTypeManual {
				kept = append(kept, c)
			}
		}
		return kept, true
	}
	v, ok := scopeFor(w, r, h.access, "collections: allowed libraries", func(msg string, args ...any) {
		h.logger.ErrorContext(r.Context(), msg, args...)
	})
	if !ok {
		return nil, false
	}
	covers, err := h.visibleManualCovers(r.Context(), v)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "collections: visible manual collections", "err", err)
		respond.InternalError(w, r)
		return nil, false
	}
	isAdmin := claims != nil && claims.IsAdmin
	kept := out[:0]
	for _, c := range out {
		if c.Type == collectionTypeManual {
			id, _ := uuid.Parse(c.ID)
			cover, visible := covers[id]
			if !visible && !isAdmin {
				continue
			}
			c.PosterPath = cover.poster
			n := cover.itemCount
			c.ItemCount = &n
		}
		kept = append(kept, c)
	}
	return kept, true
}

// manualDetail fills a manual collection's cover and count for Get, and 404s
// a non-admin caller who can't see any of its members. ok=false means a
// response was already written.
func (h *CollectionHandler) manualDetail(w http.ResponseWriter, r *http.Request, col gen.Collection, resp *collectionResponse) bool {
	claims := middleware.ClaimsFromContext(r.Context())
	if h.manual == nil {
		if claims != nil && claims.IsAdmin {
			return true
		}
		respond.NotFound(w, r)
		return false
	}
	v, ok := scopeFor(w, r, h.access, "collections: allowed libraries", func(msg string, args ...any) {
		h.logger.ErrorContext(r.Context(), msg, args...)
	})
	if !ok {
		return false
	}
	covers, err := h.visibleManualCovers(r.Context(), v)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "collections: manual visibility", "id", col.ID, "err", err)
		respond.InternalError(w, r)
		return false
	}
	cover, visible := covers[col.ID]
	if !visible && !claims.IsAdmin {
		respond.NotFound(w, r)
		return false
	}
	resp.PosterPath = cover.poster
	n := cover.itemCount
	resp.ItemCount = &n
	return true
}

// createManual handles POST /collections with type "manual". Admins only:
// a manual collection is shared with everyone who can see its members.
func (h *CollectionHandler) createManual(w http.ResponseWriter, r *http.Request, name string, description *string, itemOrder string) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	if !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	if h.manual == nil {
		respond.BadRequest(w, r, "manual collections are not available on this server")
		return
	}
	if itemOrder == "" {
		itemOrder = "custom"
	}
	if !validItemOrder(itemOrder) {
		respond.BadRequest(w, r, "item_order must be custom, release or title")
		return
	}
	col, err := h.manual.CreateManualCollection(r.Context(), gen.CreateManualCollectionParams{
		Name:        name,
		Description: description,
		ItemOrder:   itemOrder,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create manual collection", "err", err)
		respond.InternalError(w, r)
		return
	}
	resp := toCollectionResponse(col)
	zero := int64(0)
	resp.ItemCount = &zero
	respond.Created(w, r, resp)
}

// updateManualSettings applies a PATCH's item_order and poster_item_id to a
// manual collection. An empty poster_item_id clears it (back to the first
// member's poster); a non-empty one must name a member. ok=false means a
// response was already written.
func (h *CollectionHandler) updateManualSettings(w http.ResponseWriter, r *http.Request, col gen.Collection, itemOrder, posterItemID *string) bool {
	if itemOrder == nil && posterItemID == nil {
		return true
	}
	if col.Type != collectionTypeManual || h.manual == nil {
		respond.BadRequest(w, r, "item_order and poster_item_id apply to manual collections only")
		return false
	}
	if itemOrder != nil && !validItemOrder(*itemOrder) {
		respond.BadRequest(w, r, "item_order must be custom, release or title")
		return false
	}
	var poster pgtype.UUID
	if posterItemID != nil && *posterItemID != "" {
		itemID, err := uuid.Parse(*posterItemID)
		if err != nil {
			respond.BadRequest(w, r, "invalid poster_item_id")
			return false
		}
		member, err := h.isManualMember(r.Context(), col.ID, itemID)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "collection poster: membership", "id", col.ID, "err", err)
			respond.InternalError(w, r)
			return false
		}
		if !member {
			respond.BadRequest(w, r, "poster_item_id must be a member of the collection")
			return false
		}
		poster = pgtype.UUID{Bytes: [16]byte(itemID), Valid: true}
	}
	if itemOrder != nil {
		if err := h.manual.SetCollectionItemOrder(r.Context(), gen.SetCollectionItemOrderParams{ID: col.ID, ItemOrder: *itemOrder}); err != nil {
			h.logger.ErrorContext(r.Context(), "collection item order", "id", col.ID, "err", err)
			respond.InternalError(w, r)
			return false
		}
	}
	if posterItemID != nil {
		if err := h.manual.SetCollectionPosterItem(r.Context(), gen.SetCollectionPosterItemParams{ID: col.ID, PosterItemID: poster}); err != nil {
			h.logger.ErrorContext(r.Context(), "collection poster", "id", col.ID, "err", err)
			respond.InternalError(w, r)
			return false
		}
	}
	return true
}

func (h *CollectionHandler) isManualMember(ctx context.Context, colID, itemID uuid.UUID) (bool, error) {
	rows, err := h.manual.ListManualCollectionsForItem(ctx, itemID)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.ID == colID {
			return true, nil
		}
	}
	return false, nil
}

// Reorder handles PUT /api/v1/collections/{id}/items/order: body
// {"item_ids": [...]}, the members in their new order (positions 0..N-1).
// Members left out keep their position. Manual collections only; their
// custom order is what item_order "custom" lists.
func (h *CollectionHandler) Reorder(w http.ResponseWriter, r *http.Request) {
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
	if !h.requireOwnerOrAdminMutate(w, r, col) || rejectManagedMutation(w, r, col) {
		return
	}
	if col.Type != collectionTypeManual || h.manual == nil {
		respond.BadRequest(w, r, "only manual collections are reordered here; playlists use /playlists/{id}/items/order")
		return
	}
	var body struct {
		ItemIDs []string `json:"item_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid body")
		return
	}
	ids, ok := parseUniqueIDs(w, r, body.ItemIDs, "order array")
	if !ok {
		return
	}
	if err := h.manual.ReorderPlaylistItems(r.Context(), gen.ReorderPlaylistItemsParams{CollectionID: id, ItemIds: ids}); err != nil {
		h.logger.ErrorContext(r.Context(), "reorder collection items", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// parseUniqueIDs parses a list of item ids, refusing malformed and repeated
// ones. ok=false means a response was already written.
func parseUniqueIDs(w http.ResponseWriter, r *http.Request, raw []string, what string) ([]uuid.UUID, bool) {
	ids := make([]uuid.UUID, 0, len(raw))
	seen := make(map[uuid.UUID]struct{}, len(raw))
	for _, s := range raw {
		u, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			respond.BadRequest(w, r, "invalid item id in "+what)
			return nil, false
		}
		if _, dup := seen[u]; dup {
			respond.BadRequest(w, r, "duplicate item id in "+what)
			return nil, false
		}
		seen[u] = struct{}{}
		ids = append(ids, u)
	}
	return ids, true
}

// addManualItems adds a validated batch to a manual collection: every item
// must exist, be visible to the caller (library grant + rating ceiling, 404
// otherwise, as for single adds) and be a movie or show. Nothing is added
// unless all pass. Items already in the collection are left where they are.
func (h *CollectionHandler) addManualItems(w http.ResponseWriter, r *http.Request, colID uuid.UUID, ids []uuid.UUID) {
	if len(ids) == 0 {
		respond.BadRequest(w, r, "media_item_ids is empty")
		return
	}
	if len(ids) > maxBulkCollectionAdd {
		respond.BadRequest(w, r, "too many items in one request")
		return
	}
	for _, itemID := range ids {
		mi, err := h.db.GetMediaItem(r.Context(), itemID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				respond.NotFound(w, r)
				return
			}
			h.logger.ErrorContext(r.Context(), "collection add: get media item", "err", err)
			respond.InternalError(w, r)
			return
		}
		if !itemAddAllowed(w, r, h.access, h.logger, mi) {
			return
		}
		if !manualMemberTypes[mi.Type] {
			respond.BadRequest(w, r, "a collection holds movies and shows")
			return
		}
	}
	for _, itemID := range ids {
		if _, err := h.db.AddCollectionItem(r.Context(), gen.AddCollectionItemParams{
			CollectionID: colID,
			MediaItemID:  itemID,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			h.logger.ErrorContext(r.Context(), "add collection item", "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	respond.NoContent(w)
}

// libraryManualCollections lists the manual collections with a visible member
// in a library, for its Collections tab. Best-effort: nil when not wired.
func (h *CollectionHandler) libraryManualCollections(ctx context.Context, libID uuid.UUID, maxRank *int32) ([]libraryCollectionResponse, error) {
	if h.manual == nil {
		return nil, nil
	}
	rows, err := h.manual.ListLibraryManualCollections(ctx, gen.ListLibraryManualCollectionsParams{
		LibraryID:     libID,
		MaxRatingRank: maxRank,
	})
	if err != nil {
		return nil, err
	}
	out := make([]libraryCollectionResponse, 0, len(rows))
	for _, row := range rows {
		c := libraryCollectionResponse{
			ID:        row.ID.String(),
			Name:      row.Name,
			Type:      row.Type,
			ItemCount: row.ItemCount,
		}
		if row.CoverPosterPath != "" {
			p := row.CoverPosterPath
			c.PosterPath = &p
		}
		out = append(out, c)
	}
	return out, nil
}

// sortLibraryCollections orders a library's collections by name.
func sortLibraryCollections(out []libraryCollectionResponse) {
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
}
