-- +goose Up
-- Fix duplicate top-level items (movies, shows) caused by concurrent scanner
-- goroutines racing through FindOrCreateItem. For each set of duplicates
-- sharing the same (library_id, type, title, year), keep the one with artwork,
-- reassign files from duplicates to the survivor, then soft-delete the dupes.

-- Step 1: Reassign media_files from duplicates to the survivor.
-- Survivor = the row with poster_path set (enriched), or oldest if tied.
WITH dupes AS (
    SELECT id, library_id, type, title, COALESCE(year, 0) AS yr,
           ROW_NUMBER() OVER (
               PARTITION BY library_id, type, title, COALESCE(year, 0)
               ORDER BY (poster_path IS NOT NULL) DESC, created_at ASC, id ASC
           ) AS rn
    FROM media_items
    WHERE parent_id IS NULL
      AND deleted_at IS NULL
),
survivors AS (
    SELECT id AS survivor_id, library_id, type, title, yr
    FROM dupes WHERE rn = 1
),
losers AS (
    SELECT d.id AS loser_id, s.survivor_id
    FROM dupes d
    JOIN survivors s ON s.library_id = d.library_id
                    AND s.type = d.type
                    AND s.title = d.title
                    AND s.yr = d.yr
    WHERE d.rn > 1
)
UPDATE media_files
SET media_item_id = losers.survivor_id
FROM losers
WHERE media_files.media_item_id = losers.loser_id;

-- Step 2: Reassign children (seasons) from duplicates to the survivor.
WITH dupes AS (
    SELECT id, library_id, type, title, COALESCE(year, 0) AS yr,
           ROW_NUMBER() OVER (
               PARTITION BY library_id, type, title, COALESCE(year, 0)
               ORDER BY (poster_path IS NOT NULL) DESC, created_at ASC, id ASC
           ) AS rn
    FROM media_items
    WHERE parent_id IS NULL
      AND deleted_at IS NULL
),
survivors AS (
    SELECT id AS survivor_id, library_id, type, title, yr
    FROM dupes WHERE rn = 1
),
losers AS (
    SELECT d.id AS loser_id, s.survivor_id
    FROM dupes d
    JOIN survivors s ON s.library_id = d.library_id
                    AND s.type = d.type
                    AND s.title = d.title
                    AND s.yr = d.yr
    WHERE d.rn > 1
)
UPDATE media_items
SET parent_id = losers.survivor_id
FROM losers
WHERE media_items.parent_id = losers.loser_id;

-- Step 3: Soft-delete the duplicates.
WITH dupes AS (
    SELECT id, library_id, type, title, COALESCE(year, 0) AS yr,
           ROW_NUMBER() OVER (
               PARTITION BY library_id, type, title, COALESCE(year, 0)
               ORDER BY (poster_path IS NOT NULL) DESC, created_at ASC, id ASC
           ) AS rn
    FROM media_items
    WHERE parent_id IS NULL
      AND deleted_at IS NULL
)
UPDATE media_items
SET deleted_at = NOW(), updated_at = NOW()
WHERE id IN (SELECT id FROM dupes WHERE rn > 1);

-- Step 4: Prevent future duplicates with a unique partial index.
CREATE UNIQUE INDEX idx_media_items_library_type_title_year
ON media_items(library_id, type, title, COALESCE(year, 0))
WHERE parent_id IS NULL AND deleted_at IS NULL;

-- Refresh hub view to reflect dedup.
REFRESH MATERIALIZED VIEW CONCURRENTLY hub_recently_added;

-- +goose Down
DROP INDEX IF EXISTS idx_media_items_library_type_title_year;
