package v1

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/franchise"
)

// ItemFranchiseDB is what the movie detail's "collection" reference needs.
type ItemFranchiseDB interface {
	GetFranchiseForMediaItem(ctx context.Context, mediaItemID uuid.UUID) (gen.GetFranchiseForMediaItemRow, error)
	ListMediaItemsByTMDBIDs(ctx context.Context, arg gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error)
	CountCollectionItems(ctx context.Context, arg gen.CountCollectionItemsParams) (int64, error)
}

// ItemCollectionRef is the additive "collection" field on a movie's detail:
// the franchise collection it belongs to. PartCount is how many films of the
// franchise the caller would see on the collection page (owned + visible
// missing parts); OwnedCount how many of those are in the server and
// visible to the caller.
type ItemCollectionRef struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PartCount  int    `json:"part_count"`
	OwnedCount int    `json:"owned_count"`
}

// ItemManualCollectionsDB is what the "collections" field on movie and show
// details needs.
type ItemManualCollectionsDB interface {
	ListManualCollectionsForItem(ctx context.Context, mediaItemID uuid.UUID) ([]gen.ListManualCollectionsForItemRow, error)
}

// ItemManualCollectionRef is one manual collection an item belongs to.
type ItemManualCollectionRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// WithManualCollections enables the "collections" field on movie and show
// details.
func (h *ItemHandler) WithManualCollections(db ItemManualCollectionsDB) *ItemHandler {
	h.manualCollections = db
	return h
}

// manualCollectionRefs lists the manual collections an item belongs to. The
// caller has already passed the item's visibility check and the item is a
// member, so each collection has a member the caller can see (the rule the
// collections listing applies). Best-effort: a failure omits the field.
func (h *ItemHandler) manualCollectionRefs(ctx context.Context, itemID uuid.UUID) []ItemManualCollectionRef {
	if h.manualCollections == nil {
		return nil
	}
	rows, err := h.manualCollections.ListManualCollectionsForItem(ctx, itemID)
	if err != nil {
		h.logger.WarnContext(ctx, "item manual collections", "item_id", itemID, "err", err)
		return nil
	}
	if len(rows) == 0 {
		return nil
	}
	out := make([]ItemManualCollectionRef, 0, len(rows))
	for _, row := range rows {
		out = append(out, ItemManualCollectionRef{ID: row.ID.String(), Name: row.Name})
	}
	return out
}

// WithFranchise enables the "collection" reference on movie details.
func (h *ItemHandler) WithFranchise(db ItemFranchiseDB) *ItemHandler {
	h.franchise = db
	return h
}

// franchiseRef resolves the franchise collection of a movie the caller is
// already allowed to see. Best-effort: any failure omits the field rather
// than failing the detail request, and an ACL lookup failure omits it too
// (fail closed — counts are computed over the caller's visible set).
func (h *ItemHandler) franchiseRef(ctx context.Context, claims *auth.Claims, itemID uuid.UUID) *ItemCollectionRef {
	if h.franchise == nil || claims == nil {
		return nil
	}
	row, err := h.franchise.GetFranchiseForMediaItem(ctx, itemID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			h.logger.WarnContext(ctx, "item franchise lookup", "item_id", itemID, "err", err)
		}
		return nil
	}
	v := viewerScope{claims: claims, maxRank: maxRatingRankFromClaims(claims.MaxContentRating)}
	if h.access != nil {
		allowed, aerr := h.access.AllowedLibraryIDs(ctx, claims.UserID, claims.IsAdmin)
		if aerr != nil {
			h.logger.WarnContext(ctx, "item franchise: allowed libraries", "err", aerr)
			return nil
		}
		if allowed != nil {
			v.allowedLibIDs = make([]uuid.UUID, 0, len(allowed))
			for id := range allowed {
				v.allowedLibIDs = append(v.allowedLibIDs, id)
			}
		}
	}

	ref := &ItemCollectionRef{ID: row.ID.String(), Name: row.Name}
	if parts := franchise.DecodeParts(row.Parts); len(parts) > 0 {
		out, owned, perr := franchiseParts(ctx, h.franchise, nil, v, parts)
		if perr != nil {
			h.logger.WarnContext(ctx, "item franchise parts", "item_id", itemID, "err", perr)
			return nil
		}
		ref.PartCount, ref.OwnedCount = len(out), owned
	} else {
		// No TMDB snapshot yet: fall back to the visible members.
		n, cerr := h.franchise.CountCollectionItems(ctx, gen.CountCollectionItemsParams{
			CollectionID:  row.ID,
			LibraryIds:    v.allowedLibIDs,
			MaxRatingRank: v.maxRank,
		})
		if cerr != nil {
			h.logger.WarnContext(ctx, "item franchise count", "item_id", itemID, "err", cerr)
			return nil
		}
		ref.PartCount, ref.OwnedCount = int(n), int(n)
	}
	// The movie itself is visible (the caller got past Get's gates), so it
	// always counts, even if TMDB's parts list lags its membership.
	if ref.OwnedCount < 1 {
		ref.OwnedCount = 1
	}
	if ref.PartCount < ref.OwnedCount {
		ref.PartCount = ref.OwnedCount
	}
	return ref
}
