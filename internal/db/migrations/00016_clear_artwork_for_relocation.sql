-- +goose Up
-- Artwork is now stored next to media files (in library scan_paths) instead of
-- the legacy .artwork/ directory under MEDIA_PATH. Clear all existing artwork
-- paths so the next library scan re-downloads artwork to the correct locations.
UPDATE media_items
SET poster_path = NULL,
    fanart_path = NULL,
    thumb_path  = NULL,
    updated_at  = NOW()
WHERE poster_path IS NOT NULL
   OR fanart_path IS NOT NULL
   OR thumb_path  IS NOT NULL;

-- +goose Down
-- Artwork paths are populated by the scanner/enricher; no data to restore.
