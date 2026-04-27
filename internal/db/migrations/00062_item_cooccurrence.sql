-- +goose Up
-- Item-to-item watch cooccurrence: for every pair of items (A, B), how
-- many distinct users have watched both. Powers the "Because you
-- watched X" recommendation row on the home hub. v2.1 uses this in
-- place of the originally-roadmapped pgvector embedding pipeline:
--
--   Cooccurrence is the algorithm Plex uses for its recommendation
--   row, and for a homelab-scale library with sub-thousands of items
--   it produces results comparable to dense embeddings without the
--   Python-sidecar / model-download / GPU operational tax. If a future
--   user library hits the scale where cooccurrence breaks down (long-
--   tail items with no overlap), the embedding pipeline can layer on
--   top — the hub-row consumer is agnostic to which similarity engine
--   produced the recommendations.
--
-- The aggregation is one-directional in storage (pair stored once
-- with item_a < item_b) but symmetric in lookup (the query unions
-- both directions). Rebuilt nightly by a scheduler task; full rebuild
-- rather than incremental because the data is small (O(items²) at
-- worst, but in practice O(items × avg_user_overlap) which is much
-- smaller).
--
-- Score is the count of distinct users who watched both items. Higher
-- scores → stronger recommendation signal. Index on (item_a, score
-- DESC) makes the lookup-top-N-for-a-seed-item query a single index
-- scan.

CREATE TABLE item_cooccurrence (
    item_a       UUID NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    item_b       UUID NOT NULL REFERENCES media_items(id) ON DELETE CASCADE,
    score        INT  NOT NULL,
    computed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (item_a, item_b),
    CHECK (item_a < item_b)  -- canonical ordering: dedup guarantee
);

CREATE INDEX idx_item_cooccurrence_a_score ON item_cooccurrence(item_a, score DESC);
CREATE INDEX idx_item_cooccurrence_b_score ON item_cooccurrence(item_b, score DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_item_cooccurrence_b_score;
DROP INDEX IF EXISTS idx_item_cooccurrence_a_score;
DROP TABLE IF EXISTS item_cooccurrence;
