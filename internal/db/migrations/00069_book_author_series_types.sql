-- +goose Up
-- Author + series hierarchy for audiobooks. Mirrors the music
-- artist → album → track shape: book_author at the top (parent_id
-- null), optionally a book_series under the author, then the
-- audiobook (book) under either author or series, then
-- audiobook_chapter under the book for multi-file rips.
--
-- Schema-only change. The audiobook scanner picks the new types up
-- on the next rescan; existing audiobook rows stay with parent_id
-- null and `original_title=<author>` for backward compatibility, and
-- the library grid keeps rendering them. A rescan migrates them into
-- the hierarchy.
--
-- The /items/{id}/children endpoint already works on any parent
-- chain, so author + series detail pages come for free without API
-- code — the navigation surface picks up the new types as soon as
-- the scanner emits them.

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo',
                  'music_video','audiobook','audiobook_chapter','book_author','book_series',
                  'podcast','podcast_episode','home_video','book'));

-- Backfill: existing audiobook rows have parent_id NULL and the
-- author stashed on original_title (the v2.0 shape). Once the library
-- page top-level switches to book_author, those rows would disappear
-- from the grid until a rescan rebuilt them. The backfill below
-- creates a book_author row per distinct (library_id, original_title)
-- and re-parents the audiobooks at it, so an upgrade doesn't require
-- a rescan to retain visibility.
--
-- sort_title mirrors the Go-side `sortTitle` helper: lowercase, strip
-- a leading "the "/"a "/"an " article. Replicated in SQL so the
-- backfilled rows sort the same as freshly-scanned ones.
INSERT INTO media_items (id, library_id, type, title, sort_title, created_at, updated_at)
SELECT
  gen_random_uuid(),
  library_id,
  'book_author',
  original_title,
  CASE
    WHEN LOWER(original_title) LIKE 'the %' THEN SUBSTRING(LOWER(original_title) FROM 5)
    WHEN LOWER(original_title) LIKE 'a %'   THEN SUBSTRING(LOWER(original_title) FROM 3)
    WHEN LOWER(original_title) LIKE 'an %'  THEN SUBSTRING(LOWER(original_title) FROM 4)
    ELSE LOWER(original_title)
  END,
  NOW(),
  NOW()
FROM (
  SELECT DISTINCT library_id, original_title
  FROM media_items
  WHERE type = 'audiobook'
    AND parent_id IS NULL
    AND original_title IS NOT NULL
    AND original_title != ''
) AS distinct_authors
ON CONFLICT DO NOTHING;

UPDATE media_items b
SET parent_id = a.id
FROM media_items a
WHERE b.type = 'audiobook'
  AND b.parent_id IS NULL
  AND b.original_title IS NOT NULL
  AND b.original_title != ''
  AND a.type = 'book_author'
  AND a.library_id = b.library_id
  AND a.title = b.original_title;

-- +goose Down
-- Drop the hierarchy rows before the constraint excludes them.
-- audiobooks fall back to parent_id null with author on
-- original_title; rescan rebuilds.
DELETE FROM media_items WHERE type IN ('book_author', 'book_series');

ALTER TABLE media_items DROP CONSTRAINT media_items_type_check;
ALTER TABLE media_items ADD CONSTRAINT media_items_type_check
  CHECK (type IN ('movie','show','season','episode','track','album','artist','photo',
                  'music_video','audiobook','audiobook_chapter','podcast','podcast_episode',
                  'home_video','book'));
