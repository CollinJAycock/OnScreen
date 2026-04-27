package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// CooccurrenceUpdater is the slice of *gen.Queries the cooccurrence
// handler needs. Defined here so tests can fake it without spinning
// up real Postgres or pulling in the full Queries type.
type CooccurrenceUpdater interface {
	TruncateItemCooccurrence(ctx context.Context) error
	RebuildItemCooccurrence(ctx context.Context) error
}

// CooccurrenceHandler aggregates watch_events into the
// item_cooccurrence table. Runs nightly via the scheduler. Powers the
// "Because you watched X" recommendation row on the home hub —
// without this task running periodically, the row is empty for every
// user even after they've watched plenty of items, because the
// table only gets populated by this rebuild.
//
// v2.1 uses cooccurrence in place of the originally-roadmapped
// pgvector embeddings: same lookup shape (seed item → top-N similar
// items), no Python sidecar, no embedding model download. For
// homelab-scale libraries the result quality is comparable, and the
// hub-row consumer is agnostic to which engine produced the
// recommendations — a future embedding pipeline can layer on top
// without touching the API.
type CooccurrenceHandler struct {
	q CooccurrenceUpdater
}

// NewCooccurrenceHandler returns a handler ready for registration
// with the scheduler.
func NewCooccurrenceHandler(q CooccurrenceUpdater) *CooccurrenceHandler {
	return &CooccurrenceHandler{q: q}
}

// Run wipes and rebuilds item_cooccurrence in a single transactionless
// pass. The TRUNCATE-then-INSERT shape is intentional: postgres can't
// efficiently express "rebuild this table from scratch" as one DML,
// and the worst-case race (a user reading the table mid-rebuild) is a
// brief empty result on the BYW row, which the home page tolerates.
//
// The rawCfg is currently ignored — there's nothing to configure
// per-run. Reserved for future "rebuild only the last N days" or
// "skip rebuild if already fresh enough" knobs.
func (h *CooccurrenceHandler) Run(ctx context.Context, _ json.RawMessage) (string, error) {
	if err := h.q.TruncateItemCooccurrence(ctx); err != nil {
		return "", fmt.Errorf("truncate item_cooccurrence: %w", err)
	}
	if err := h.q.RebuildItemCooccurrence(ctx); err != nil {
		return "", fmt.Errorf("rebuild item_cooccurrence: %w", err)
	}
	return "rebuilt item_cooccurrence", nil
}

// Compile-time check that *gen.Queries satisfies CooccurrenceUpdater.
// If sqlc regenerates with a renamed query, this fails the build —
// faster than discovering it at scheduler-task wire time.
var _ CooccurrenceUpdater = (*gen.Queries)(nil)
