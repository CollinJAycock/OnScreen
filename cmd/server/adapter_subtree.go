package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// subtreeDeleter implements v1.ItemSubtreeDeleter on top of the
// generated queries. Soft-deletes the item plus every descendant via
// the recursive parent_id walk, then hard-deletes attached files so
// the next scan doesn't restore the row through
// RestoreMediaItemAncestry. Per the "delete = hard delete" rule,
// no status='deleted' tombstones are left behind.
type subtreeDeleter struct{ q *gen.Queries }

func (d *subtreeDeleter) SoftDeleteSubtree(ctx context.Context, itemID uuid.UUID) error {
	if err := d.q.SoftDeleteMediaItemSubtree(ctx, itemID); err != nil {
		return err
	}
	_, err := d.q.HardDeleteMediaFilesForSubtree(ctx, itemID)
	return err
}
