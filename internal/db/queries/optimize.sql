-- Optimized versions (migration 00056): download_conversions rows with
-- purpose 'optimize' — an admin-requested MP4 copy of a file at one of three
-- profiles, kept until the source goes or changes, or the admin deletes it.
-- The runner claims and encodes them with the downloads (downloads.sql).

-- name: GetLiveOptimizeConversion :one
-- The file's queued, running or ready optimized version at this profile.
SELECT * FROM download_conversions
WHERE purpose = 'optimize'
  AND media_file_id = @media_file_id
  AND quality = @quality
  AND status IN ('queued', 'running', 'ready');

-- name: CreateOptimizeConversion :one
-- Queues an optimized version. Zero rows (pgx.ErrNoRows) when a live one for
-- the file and profile appeared since the caller looked.
INSERT INTO download_conversions (
    media_item_id, media_file_id, quality, purpose, requested_by, source_file_hash
) VALUES (
    @media_item_id, @media_file_id, @quality, 'optimize', @requested_by, @source_file_hash
)
ON CONFLICT (media_file_id, quality)
    WHERE status IN ('queued', 'running', 'ready')
DO NOTHING
RETURNING *;

-- name: ListOptimizeConversionViews :many
-- The admin's list, newest first, with what the UI needs to name the item
-- (an episode's show title and numbers). Every filter is optional.
SELECT dc.id, dc.media_item_id, dc.media_file_id, dc.quality, dc.status,
       dc.progress, dc.error, dc.size_bytes, dc.created_at, dc.started_at,
       dc.completed_at, dc.width, dc.height, dc.bitrate_kbps, dc.duration_ms,
       dc.requested_by,
       mi.title AS item_title, mi.type AS item_type, mi.library_id,
       mi.index AS item_index, season.index AS season_index,
       show.title AS show_title
FROM download_conversions dc
JOIN media_items mi ON mi.id = dc.media_item_id
LEFT JOIN media_items season ON season.id = mi.parent_id AND mi.type = 'episode'
LEFT JOIN media_items show ON show.id = season.parent_id
WHERE dc.purpose = 'optimize'
  AND (sqlc.narg('item_id')::uuid IS NULL OR dc.media_item_id = sqlc.narg('item_id')::uuid)
  AND (sqlc.narg('library_id')::uuid IS NULL OR mi.library_id = sqlc.narg('library_id')::uuid)
  AND (sqlc.narg('status')::text IS NULL OR dc.status = sqlc.narg('status')::text)
ORDER BY dc.created_at DESC, dc.id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountOptimizeConversionsByStatus :many
-- Per-status totals under the same filters (the status filter aside): the
-- list's header ("3 queued, 1 encoding, 40 ready").
SELECT dc.status, COUNT(*)::bigint AS n
FROM download_conversions dc
JOIN media_items mi ON mi.id = dc.media_item_id
WHERE dc.purpose = 'optimize'
  AND (sqlc.narg('item_id')::uuid IS NULL OR dc.media_item_id = sqlc.narg('item_id')::uuid)
  AND (sqlc.narg('library_id')::uuid IS NULL OR mi.library_id = sqlc.narg('library_id')::uuid)
GROUP BY dc.status;

-- name: GetOptimizeConversionView :one
SELECT dc.id, dc.media_item_id, dc.media_file_id, dc.quality, dc.status,
       dc.progress, dc.error, dc.size_bytes, dc.created_at, dc.started_at,
       dc.completed_at, dc.width, dc.height, dc.bitrate_kbps, dc.duration_ms,
       dc.requested_by,
       mi.title AS item_title, mi.type AS item_type, mi.library_id,
       mi.index AS item_index, season.index AS season_index,
       show.title AS show_title
FROM download_conversions dc
JOIN media_items mi ON mi.id = dc.media_item_id
LEFT JOIN media_items season ON season.id = mi.parent_id AND mi.type = 'episode'
LEFT JOIN media_items show ON show.id = season.parent_id
WHERE dc.id = @id AND dc.purpose = 'optimize';

-- name: DeleteOptimizeConversion :one
-- Removes an optimized version or job whatever its state, returning it (a
-- running encode is then stopped by its heartbeat, a ready file removed by
-- the caller).
DELETE FROM download_conversions
WHERE id = @id AND purpose = 'optimize'
RETURNING id, media_item_id, media_file_id, quality, status;

-- name: ListPlayableOptimizedForItem :many
-- An item's finished optimized versions that still match their source: the
-- source file is active and its content hash is the one that was encoded
-- (a file replaced in place is not offered while the cleanup task catches
-- up). Highest bitrate first.
SELECT dc.* FROM download_conversions dc
JOIN media_files mf ON mf.id = dc.media_file_id
WHERE dc.purpose = 'optimize'
  AND dc.status = 'ready'
  AND dc.media_item_id = @media_item_id
  AND mf.status = 'active'
  AND (dc.source_file_hash IS NULL OR mf.file_hash IS NULL OR dc.source_file_hash = mf.file_hash)
ORDER BY dc.bitrate_kbps DESC NULLS LAST, dc.created_at DESC;

-- name: GetPlayableOptimizedVersion :one
-- One finished optimized version under the same rule, for its stream route.
SELECT dc.* FROM download_conversions dc
JOIN media_files mf ON mf.id = dc.media_file_id
WHERE dc.id = @id
  AND dc.purpose = 'optimize'
  AND dc.status = 'ready'
  AND mf.status = 'active'
  AND (dc.source_file_hash IS NULL OR mf.file_hash IS NULL OR dc.source_file_hash = mf.file_hash);

-- name: ListStaleOptimizeConversions :many
-- Optimized versions whose source was replaced in place since they were
-- encoded (the scanner re-hashed different bytes under the same path). The
-- cleanup task deletes them.
SELECT dc.* FROM download_conversions dc
JOIN media_files mf ON mf.id = dc.media_file_id
WHERE dc.purpose = 'optimize'
  AND dc.status IN ('queued', 'running', 'ready')
  AND dc.source_file_hash IS NOT NULL
  AND mf.file_hash IS NOT NULL
  AND dc.source_file_hash <> mf.file_hash;

-- name: ListLiveOptimizeConversionIDs :many
-- Ids whose files belong in the optimized directory; anything else there is
-- an orphan (its source file was deleted and the row cascaded away).
SELECT id FROM download_conversions
WHERE purpose = 'optimize' AND status IN ('queued', 'running', 'ready');

-- name: ListOptimizeTargetsUnder :many
-- Expands items an admin picked (movies, shows, seasons, episodes, …) into
-- the video items under them, themselves included.
WITH RECURSIVE subtree AS (
    SELECT mi.id, mi.type FROM media_items mi
    WHERE mi.id = ANY(sqlc.arg('root_ids')::uuid[]) AND mi.deleted_at IS NULL
    UNION
    SELECT m.id, m.type
    FROM media_items m
    JOIN subtree s ON m.parent_id = s.id
    WHERE m.deleted_at IS NULL
)
SELECT subtree.id FROM subtree
WHERE subtree.type IN ('movie', 'episode', 'home_video', 'music_video')
LIMIT sqlc.arg('lim')::int;

-- name: ListOptimizeTargetsInLibrary :many
-- A library's video items that have a playable video file, oldest first.
SELECT mi.id FROM media_items mi
WHERE mi.library_id = @library_id
  AND mi.deleted_at IS NULL
  AND mi.type IN ('movie', 'episode', 'home_video', 'music_video')
  AND EXISTS (
      SELECT 1 FROM media_files mf
      WHERE mf.media_item_id = mi.id AND mf.status = 'active' AND mf.video_codec IS NOT NULL
  )
ORDER BY mi.created_at, mi.id
LIMIT sqlc.arg('lim')::int;

-- name: ListCollectionMemberIDs :many
-- A collection's stored members (manual, franchise and smart collections
-- keep them in collection_items).
SELECT media_item_id FROM collection_items WHERE collection_id = @collection_id;

-- name: ListGenreMemberIDs :many
-- An auto-genre collection's members: the movies and shows of that genre.
SELECT id FROM media_items
WHERE deleted_at IS NULL
  AND type IN ('movie', 'show')
  AND genres @> ARRAY[@genre::text];
