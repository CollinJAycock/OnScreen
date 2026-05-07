-- +goose Up
-- Lyrics storage for music tracks. Two columns because clients render
-- synced vs. plain very differently (timestamped scroll vs. static
-- paragraph). When a track has both (LRCLIB often returns both), we
-- store both so the UI can let the user pick.
--
-- Sourced at scan time from the file's ID3/Vorbis tags, then (on first
-- read that finds them empty) backfilled from LRCLIB via the lyrics
-- service. The source is not tracked — the on-read fetcher is
-- idempotent and overwrites empty strings only.

ALTER TABLE media_items
    ADD COLUMN lyrics_plain  TEXT,
    ADD COLUMN lyrics_synced TEXT;

-- +goose Down
ALTER TABLE media_items
    DROP COLUMN IF EXISTS lyrics_synced,
    DROP COLUMN IF EXISTS lyrics_plain;
