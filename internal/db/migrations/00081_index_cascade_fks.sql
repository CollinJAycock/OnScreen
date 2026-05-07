-- +goose Up
-- Three FKs reference media_items(id) but the referencing column had
-- no index of its own — Postgres must seq-scan the entire referencing
-- table for each parent row touched by a cascade DELETE/UPDATE. On QA
-- this turned a library purge of ~6,100 items into a >30-minute
-- transaction that couldn't even complete inside the Cloudflare edge
-- timeout for the synchronous variant.
--
-- Each is a partial index (WHERE col IS NOT NULL) because all three
-- columns are nullable and the common case is a row that doesn't
-- point at any media_item — full indexes would waste space and write
-- bandwidth on the dominant null case.

-- Plain CREATE INDEX (no CONCURRENTLY) because Goose wraps each
-- migration in a transaction by default and CONCURRENTLY can't run
-- inside one. Pre-launch the brief table lock during creation has
-- no user impact; revisit if/when we ship to a busy deployment.

CREATE INDEX IF NOT EXISTS idx_notifications_item_id
    ON notifications(item_id)
    WHERE item_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_media_requests_fulfilled_item_id
    ON media_requests(fulfilled_item_id)
    WHERE fulfilled_item_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_recordings_item_id
    ON recordings(item_id)
    WHERE item_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_recordings_item_id;
DROP INDEX IF EXISTS idx_media_requests_fulfilled_item_id;
DROP INDEX IF EXISTS idx_notifications_item_id;
