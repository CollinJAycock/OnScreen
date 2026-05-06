-- +goose Up
-- Hub-query optimization: drop the 41,515-row skip scan that
-- ListRecentlyAdded incurred on both per-library and global paths.
--
-- Today the planner uses idx_media_items_created (a single-column
-- partial on created_at DESC WHERE deleted_at IS NULL) and walks
-- newest-first, FILTERING by type='episode' and library_id at the
-- heap. On the QA TV-shows library that's ~41k non-episode rows
-- skipped per query, ~270ms wall time, ×6 in parallel for the
-- per-library hub strips.
--
-- The composite below carries (library_id, created_at DESC) under a
-- partial WHERE that excludes the skip rows up front — no heap
-- filter, no rows-removed-by-filter accounting. Per-library hub
-- queries drop from ~270ms to estimated <50ms each.
--
-- Why two indexes (library_id-leading vs type-leading): the global
-- ListRecentlyAdded variant (no library_id filter) wouldn't benefit
-- from the leading-library_id index because pg can't skip-scan a
-- btree's leading column. A second narrow partial keyed on the
-- created_at DESC sort column itself, scoped via WHERE to episodes
-- only, kills the global skip-scan too.
--
-- Both are partial on (deleted_at IS NULL AND type = 'episode'):
-- soft-deleted rows and non-episode rows aren't part of any
-- recently-added episode strip, so they don't belong in the index.

CREATE INDEX IF NOT EXISTS idx_media_items_episodes_lib_recent
    ON media_items(library_id, created_at DESC)
    WHERE deleted_at IS NULL AND type = 'episode';

CREATE INDEX IF NOT EXISTS idx_media_items_episodes_recent
    ON media_items(created_at DESC)
    WHERE deleted_at IS NULL AND type = 'episode';

-- Refresh stats on watch_events + media_items. The Trending query
-- planner picked a Hash Join with media_items as the OUTER side
-- because it estimated the post-aggregation bucket set at ~5,200
-- rows when the actual is ~15. With correct stats it nest-loops
-- with PK lookups instead and the 360ms outer seq-scan vanishes.
-- Cheap and idempotent, so we run it inline with the index build.
ANALYZE watch_events;
ANALYZE media_items;

-- +goose Down
DROP INDEX IF EXISTS idx_media_items_episodes_lib_recent;
DROP INDEX IF EXISTS idx_media_items_episodes_recent;
