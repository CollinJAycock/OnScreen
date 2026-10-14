package v1

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/studios"
)

// ItemStudioReader lists an item's studios / networks. Satisfied by
// *studios.Service.
type ItemStudioReader interface {
	ItemStudios(ctx context.Context, itemID uuid.UUID) ([]studios.Studio, error)
}

// ItemNFOWriter writes an item's NFO next to its media when the library
// saves NFOs. Satisfied by *scanner.Enricher; it logs its own failures.
type ItemNFOWriter interface {
	WriteItemNFO(ctx context.Context, itemID uuid.UUID)
}

// WithStudios enables the "studios" field on movie and show details.
func (h *ItemHandler) WithStudios(s ItemStudioReader) *ItemHandler {
	h.studios = s
	return h
}

// WithNFOWriter makes a manual metadata edit rewrite the item's NFO (in
// libraries with save_nfo on), so the file never lags the edit.
func (h *ItemHandler) WithNFOWriter(w ItemNFOWriter) *ItemHandler {
	h.nfoWriter = w
	return h
}

// ItemStudioRef is one studio (movies) or network (shows) on item details;
// /api/v1/studios/{id}/items lists its titles.
type ItemStudioRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // "studio" | "network"
}

// studiosFor is a movie's or show's studios, or nil when it has none or the
// lookup isn't wired. A studio carries no visibility of its own: the caller
// already passed the item's checks.
func (h *ItemHandler) studiosFor(ctx context.Context, itemID uuid.UUID, itemType string) []ItemStudioRef {
	if h.studios == nil || (itemType != "movie" && itemType != "show") {
		return nil
	}
	list, err := h.studios.ItemStudios(ctx, itemID)
	if err != nil {
		h.logger.WarnContext(ctx, "item studios", "id", itemID, "err", err)
		return nil
	}
	if len(list) == 0 {
		return nil
	}
	out := make([]ItemStudioRef, len(list))
	for i, s := range list {
		out[i] = ItemStudioRef{ID: s.ID.String(), Name: s.Name, Kind: s.Kind}
	}
	return out
}

// writeNFOAfterEdit rewrites the item's NFO in the background, detached
// from the request: the response doesn't wait on a network share.
func (h *ItemHandler) writeNFOAfterEdit(ctx context.Context, itemID uuid.UUID) {
	if h.nfoWriter == nil {
		return
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	go func() {
		defer cancel()
		h.nfoWriter.WriteItemNFO(bg, itemID)
	}()
}
