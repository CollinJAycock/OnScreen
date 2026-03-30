-- +goose Up
-- Fix duplicate hierarchy items (seasons, episodes, albums, tracks) caused by
-- concurrent scanner goroutines racing through FindOrCreateHierarchyItem before
-- a DB-level uniqueness constraint existed. For each set of duplicates sharing
-- the same (parent_id, type, index), we keep the oldest row, reassign children
-- and files from the duplicates to the survivor, then delete the duplicates.

-- Step 1: Reassign children of duplicate items to the survivor.
-- The survivor is the row with the smallest created_at (first inserted).
WITH dupes AS (
    SELECT id, parent_id, type, index,
           ROW_NUMBER() OVER (
               PARTITION BY parent_id, type, index
               ORDER BY created_at ASC, id ASC
           ) AS rn
    FROM media_items
    WHERE parent_id IS NOT NULL
      AND index IS NOT NULL
      AND deleted_at IS NULL
),
survivors AS (
    SELECT id AS survivor_id, parent_id, type, index
    FROM dupes WHERE rn = 1
),
losers AS (
    SELECT d.id AS loser_id, s.survivor_id
    FROM dupes d
    JOIN survivors s ON s.parent_id = d.parent_id
                    AND s.type = d.type
                    AND s.index = d.index
    WHERE d.rn > 1
)
UPDATE media_items
SET parent_id = losers.survivor_id
FROM losers
WHERE media_items.parent_id = losers.loser_id;

-- Step 2: Reassign files from duplicates to the survivor.
WITH dupes AS (
    SELECT id, parent_id, type, index,
           ROW_NUMBER() OVER (
               PARTITION BY parent_id, type, index
               ORDER BY created_at ASC, id ASC
           ) AS rn
    FROM media_items
    WHERE parent_id IS NOT NULL
      AND index IS NOT NULL
      AND deleted_at IS NULL
),
survivors AS (
    SELECT id AS survivor_id, parent_id, type, index
    FROM dupes WHERE rn = 1
),
losers AS (
    SELECT d.id AS loser_id, s.survivor_id
    FROM dupes d
    JOIN survivors s ON s.parent_id = d.parent_id
                    AND s.type = d.type
                    AND s.index = d.index
    WHERE d.rn > 1
)
UPDATE media_files
SET media_item_id = losers.survivor_id
FROM losers
WHERE media_files.media_item_id = losers.loser_id;

-- Step 3: Delete the duplicate items.
WITH dupes AS (
    SELECT id, parent_id, type, index,
           ROW_NUMBER() OVER (
               PARTITION BY parent_id, type, index
               ORDER BY created_at ASC, id ASC
           ) AS rn
    FROM media_items
    WHERE parent_id IS NOT NULL
      AND index IS NOT NULL
      AND deleted_at IS NULL
)
DELETE FROM media_items
WHERE id IN (SELECT id FROM dupes WHERE rn > 1);

-- Step 4: Prevent future duplicates with a unique partial index.
CREATE UNIQUE INDEX idx_media_items_parent_type_index
ON media_items(parent_id, type, index)
WHERE parent_id IS NOT NULL AND index IS NOT NULL AND deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_media_items_parent_type_index;
