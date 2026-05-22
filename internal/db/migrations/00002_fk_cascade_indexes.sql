-- +goose Up
-- +goose StatementBegin
-- ────────────────────────────────────────────────────────────────────────────
-- FK cascade-delete index gaps.
--
-- The squashed 00001_init header claims "every FK column targeted by a
-- cascade DELETE/UPDATE has a covering index." Two cascade paths slipped
-- through, and they're exactly what made a library purge hang for >30 min
-- in production (the PurgeDeletedLibraryBatch loop in media.sql):
--
--   1. watch_events.media_id → media_items(id) ON DELETE CASCADE.
--      There was NO standalone index on watch_events.media_id. The
--      composite idx_watch_events_user_media (user_id, media_id,
--      occurred_at) does NOT help: a B-tree can't satisfy an equality
--      on media_id without the leading user_id column, and the RI
--      cascade trigger fires `DELETE FROM watch_events WHERE media_id = $1`
--      with no user_id. So every parent media_item delete seq-scanned
--      ALL watch_events partitions.
--
--   2. media_items.parent_id → media_items(id) ON DELETE CASCADE.
--      idx_media_items_parent exists but is PARTIAL (WHERE deleted_at
--      IS NULL). The cascade's `WHERE parent_id = $1` predicate doesn't
--      imply that, so the planner can't use the partial index for the
--      cascade — it seq-scanned media_items per deleted parent too.
--
-- Both indexes below are non-partial so the RI cascade trigger can use
-- them. We ADD rather than replace the partial index (the server lock
-- forbids dropping existing schema objects; the partial index still
-- backs the deleted_at-filtered child-listing queries).
--
-- watch_events is RANGE-partitioned by occurred_at. CREATE INDEX WITHOUT
-- the ONLY keyword builds a partitioned index: Postgres creates it on
-- every existing partition and automatically creates it on any future
-- partition the PartitionWorker spins up via CREATE TABLE ... PARTITION
-- OF. No per-partition bookkeeping needed.
--
-- Plain (non-CONCURRENT) CREATE INDEX: it runs inside goose's migration
-- transaction and takes a brief write lock. At current deployment scale
-- (self-hosted, monthly partitions) the build is seconds. CONCURRENTLY
-- is not an option here anyway — Postgres rejects it on a partitioned
-- parent, and goose runs migrations in a transaction. An operator with
-- an unusually large watch_events can pre-build the per-partition
-- indexes CONCURRENTLY out-of-band before applying this.
-- ────────────────────────────────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_watch_events_media
    ON public.watch_events USING btree (media_id);

CREATE INDEX IF NOT EXISTS idx_media_items_parent_all
    ON public.media_items USING btree (parent_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS public.idx_media_items_parent_all;
DROP INDEX IF EXISTS public.idx_watch_events_media;
-- +goose StatementEnd
