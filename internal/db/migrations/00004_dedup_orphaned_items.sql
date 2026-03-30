-- +goose Up
-- Soft-delete media_items that have no active or missing files.
-- These orphaned placeholders were created by a bug in FindOrCreateItem where
-- title normalisation was not applied: after TMDB enrichment changed a title
-- from "battle los angeles" to "Battle: Los Angeles", each subsequent scan
-- failed to match the stored title and created a new duplicate item.
-- Leaf types (movie, episode, track) must have at least one file; show/season
-- parents legitimately have no direct files and are excluded.
UPDATE media_items
SET    deleted_at = NOW()
WHERE  deleted_at IS NULL
  AND  type NOT IN ('show', 'season')
  AND  id NOT IN (
    SELECT DISTINCT media_item_id
    FROM   media_files
    WHERE  status IN ('active', 'missing')
  );

-- +goose Down
-- Not reversible: we cannot know which items were orphaned vs intentionally file-free.
