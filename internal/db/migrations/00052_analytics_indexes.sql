-- +goose Up
-- The analytics page runs five queries that filter watch_events by
-- event_type IN ('stop','scrobble') and (for most) bound by occurred_at
-- over a 30–90 day window. The existing indexes are keyed on user_id
-- and session_id, neither of which helps the analytics path — Postgres
-- was falling back to seq scans and the page was taking ~10s on a
-- non-trivial catalog.
--
-- A composite (event_type, occurred_at DESC) covers all of:
--   - overview's total_plays / total_watch_time_ms (event_type filter)
--   - plays_per_day, bandwidth_per_day, top_played, recent_plays
--     (event_type + occurred_at window)
-- Partial WHERE clause restricts the index to the two event types the
-- analytics queries actually ask about — keeps the index small even on
-- instances that log heavy 'progress' or 'pause' traffic.
CREATE INDEX IF NOT EXISTS idx_watch_events_event_occurred
    ON watch_events (event_type, occurred_at DESC)
    WHERE event_type IN ('stop', 'scrobble');

-- media_files per-library aggregation joins mf.media_item_id against
-- media_items and filters on status='active'. We already have
-- idx_media_files_item (media_item_id), but aggregations benefit from
-- a partial index on the active subset. Tiny and covers several
-- active-only queries (library analytics, codec, container breakdowns).
CREATE INDEX IF NOT EXISTS idx_media_files_active_item
    ON media_files (media_item_id)
    WHERE status = 'active';

-- +goose Down
DROP INDEX IF EXISTS idx_media_files_active_item;
DROP INDEX IF EXISTS idx_watch_events_event_occurred;
