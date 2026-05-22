//go:build integration

// Guards the two FK cascade-delete indexes added in
// 00002_fk_cascade_indexes.sql. These cover the ON DELETE CASCADE RI
// lookups that the squashed init left unindexed:
//
//   - watch_events.media_id  → media_items(id)   (per-row cascade on purge)
//   - media_items.parent_id  → media_items(id)   (subtree cascade on purge)
//
// Their absence is what made PurgeDeletedLibraryBatch hang for >30 min
// in production. The composite idx_watch_events_user_media can't serve
// a media_id-only equality (wrong leading column), and the partial
// idx_media_items_parent (WHERE deleted_at IS NULL) can't serve the
// cascade's unqualified parent_id predicate — so a structural check
// that these specific covering indexes exist is the right regression
// guard. We deliberately don't assert a cost-based EXPLAIN plan: on the
// tiny rowcounts a test DB carries the planner correctly prefers a seq
// scan regardless of index presence, so a plan assertion would be noise.
package gen_test

import (
	"context"
	"testing"

	"github.com/onscreen/onscreen/internal/testdb"
)

func TestFKCascadeIndexes_Integration_Exist(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	for _, idx := range []string{
		"public.idx_watch_events_media",
		"public.idx_media_items_parent_all",
	} {
		var oid *string
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1)::text", idx).Scan(&oid); err != nil {
			t.Fatalf("to_regclass(%s): %v", idx, err)
		}
		if oid == nil {
			t.Errorf("index %s missing — 00002_fk_cascade_indexes.sql not applied?", idx)
		}
	}
}

// TestFKCascadeIndexes_Integration_PartitionPropagation proves the
// watch_events index is a *partitioned* index (CREATE INDEX without
// ONLY), so every existing partition carries a child index and any
// future partition the PartitionWorker creates inherits one
// automatically. If someone re-adds the index with ONLY, the child
// count drops to zero and the per-partition cascade goes back to
// seq-scanning.
func TestFKCascadeIndexes_Integration_PartitionPropagation(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	var partitions int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_inherits i
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = 'watch_events'`).Scan(&partitions); err != nil {
		t.Fatalf("count watch_events partitions: %v", err)
	}
	if partitions == 0 {
		t.Fatal("expected watch_events to have partitions; found none")
	}

	var childIndexes int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_inherits i
		JOIN pg_class p ON p.oid = i.inhparent
		WHERE p.relname = 'idx_watch_events_media'`).Scan(&childIndexes); err != nil {
		t.Fatalf("count child indexes of idx_watch_events_media: %v", err)
	}

	if childIndexes != partitions {
		t.Errorf("idx_watch_events_media has %d child indexes but watch_events has %d partitions — index not propagated to every partition (created with ONLY?)",
			childIndexes, partitions)
	}
}
