package v1

import (
	"context"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// LibraryParentDB is the optional title lookup behind parent_title on the
// library listing: one batched query per page. *gen.Queries satisfies it.
type LibraryParentDB interface {
	ListMediaItemTitles(ctx context.Context, ids []uuid.UUID) ([]gen.ListMediaItemTitlesRow, error)
}

// WithParentTitles wires parent_title on GET /libraries/:id/items. Without it
// child rows still carry parent_id; only the title is omitted.
func (h *LibraryHandler) WithParentTitles(db LibraryParentDB) *LibraryHandler {
	h.parentDB = db
	return h
}

// attachParentTitles fills parent_title on the rows of one listing page that
// have a parent (albums, music videos, seasons, … listed via ?type=). A
// top-level listing has no parents and makes no query. Failures only drop
// the field — the listing itself already succeeded.
func (h *LibraryHandler) attachParentTitles(ctx context.Context, items []media.Item, out []MediaItemResponse) {
	if h.parentDB == nil {
		return
	}
	seen := make(map[uuid.UUID]struct{})
	var ids []uuid.UUID
	for _, it := range items {
		if it.ParentID == nil {
			continue
		}
		if _, dup := seen[*it.ParentID]; dup {
			continue
		}
		seen[*it.ParentID] = struct{}{}
		ids = append(ids, *it.ParentID)
	}
	if len(ids) == 0 {
		return
	}
	rows, err := h.parentDB.ListMediaItemTitles(ctx, ids)
	if err != nil {
		h.logger.WarnContext(ctx, "library items: parent titles", "err", err)
		return
	}
	titles := make(map[uuid.UUID]string, len(rows))
	for _, row := range rows {
		titles[row.ID] = row.Title
	}
	for i, it := range items {
		if it.ParentID == nil {
			continue
		}
		if t, ok := titles[*it.ParentID]; ok {
			out[i].ParentTitle = &t
		}
	}
}
