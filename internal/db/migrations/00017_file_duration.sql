-- +goose Up
-- Add duration_ms to media_files so playback can read it directly from file metadata.
ALTER TABLE media_files ADD COLUMN IF NOT EXISTS duration_ms BIGINT;

-- Backfill from media_items where available.
UPDATE media_files mf
SET duration_ms = mi.duration_ms
FROM media_items mi
WHERE mf.media_item_id = mi.id
  AND mi.duration_ms IS NOT NULL
  AND mf.duration_ms IS NULL;
