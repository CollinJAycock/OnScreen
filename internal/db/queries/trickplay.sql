-- name: GetTrickplayStatus :one
-- Returns the current trickplay state for an item, or pgx.ErrNoRows if no
-- row exists yet. Callers should treat ErrNoRows as "not generated".
SELECT item_id, file_id, status, sprite_count, interval_sec,
       thumb_width, thumb_height, grid_cols, grid_rows,
       last_attempted_at, last_error, generated_at, chapter_thumb_count
FROM trickplay_status
WHERE item_id = $1;

-- name: UpsertTrickplayPending :exec
-- Marks an item as pending generation, recording which file will be used.
-- Upserts so a retry overwrites prior failed state and zeros out error.
INSERT INTO trickplay_status (
    item_id, file_id, status, interval_sec, thumb_width, thumb_height,
    grid_cols, grid_rows, last_attempted_at, last_error
) VALUES (
    $1, $2, 'pending', $3, $4, $5, $6, $7, NOW(), NULL
)
ON CONFLICT (item_id) DO UPDATE SET
    file_id           = EXCLUDED.file_id,
    status            = 'pending',
    interval_sec      = EXCLUDED.interval_sec,
    thumb_width       = EXCLUDED.thumb_width,
    thumb_height      = EXCLUDED.thumb_height,
    grid_cols         = EXCLUDED.grid_cols,
    grid_rows         = EXCLUDED.grid_rows,
    last_attempted_at = NOW(),
    last_error        = NULL,
    -- The run wipes the item's directory, chapter thumbnails included.
    chapter_thumb_count = NULL;

-- name: MarkTrickplayDone :exec
-- Records a successful generation run: sprite count, generation timestamp.
-- The spec columns are left as-written by UpsertTrickplayPending.
UPDATE trickplay_status
SET status       = 'done',
    sprite_count = $2,
    generated_at = NOW(),
    last_error   = NULL
WHERE item_id = $1;

-- name: MarkTrickplayFailed :exec
-- Records a failed generation attempt so retries are rate-limited and admins
-- can see the reason. last_attempted_at already reflects the run start.
UPDATE trickplay_status
SET status     = 'failed',
    last_error = $2
WHERE item_id = $1;

-- name: DeleteTrickplayStatus :exec
-- Removes the trickplay row (used when an item is deleted or trickplay is
-- disabled for it).
DELETE FROM trickplay_status WHERE item_id = $1;

-- name: SetTrickplayChapterThumbs :exec
-- Records how many chapter thumbnails (chapter_000.jpg, ...) sit next to the
-- item's sprites; 0 = the file has no chapters. Migration 00051.
UPDATE trickplay_status
SET chapter_thumb_count = $2
WHERE item_id = $1;

-- name: GetTrickplayChapterThumbs :one
-- The file whose chapters have thumbnails and how many, for the item detail
-- response's chapter thumb_url. Only a finished run counts.
SELECT file_id, chapter_thumb_count
FROM trickplay_status
WHERE item_id = $1 AND status = 'done' AND chapter_thumb_count > 0;

-- name: ListChapterThumbCandidates :many
-- Items whose sprites predate chapter thumbnails (chapter_thumb_count NULL on
-- a finished run) and whose sprite file has chapters, oldest first: the
-- chapter_thumbnails task's work list.
SELECT ts.item_id
FROM trickplay_status ts
JOIN media_items mi ON mi.id = ts.item_id
JOIN media_files mf ON mf.id = ts.file_id
WHERE ts.status = 'done'
  AND ts.chapter_thumb_count IS NULL
  AND mi.deleted_at IS NULL
  AND mf.status = 'active'
  AND jsonb_typeof(mf.chapters) = 'array'
  AND jsonb_array_length(mf.chapters) > 0
ORDER BY ts.generated_at, ts.item_id
LIMIT sqlc.arg('lim')::int;
