-- name: ListMyPhotoAlbums :many
-- Owned albums plus their item count and the most-recently-taken photo as a
-- cover candidate. Cover falls back to created_at when EXIF taken_at is
-- missing so albums of scanned-from-disk photos still get a tile.
--
-- The count and the cover see only what the album page shows: live photos
-- within the caller's rating ceiling, in libraries they can open
-- (library_ids NULL = all). Otherwise a profile whose ceiling was lowered, or
-- whose library access was revoked, after filling an album still got those
-- photos as its cover.
SELECT
    c.id, c.user_id, c.name, c.description, c.type, c.poster_path,
    c.created_at, c.updated_at,
    (
        SELECT COUNT(*)
        FROM collection_items ci
        JOIN media_items mi ON mi.id = ci.media_item_id
        WHERE ci.collection_id = c.id
          AND mi.deleted_at IS NULL
          AND mi.type = 'photo'
          AND (sqlc.narg('max_rating_rank')::int IS NULL
               OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
          AND (sqlc.narg('library_ids')::uuid[] IS NULL
               OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
    )::bigint AS item_count,
    (
        SELECT mi.poster_path
        FROM collection_items ci
        JOIN media_items mi ON mi.id = ci.media_item_id
        LEFT JOIN photo_metadata pm ON pm.item_id = mi.id
        WHERE ci.collection_id = c.id
          AND mi.deleted_at IS NULL
          AND mi.type = 'photo'
          AND (sqlc.narg('max_rating_rank')::int IS NULL
               OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
          AND (sqlc.narg('library_ids')::uuid[] IS NULL
               OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
        ORDER BY COALESCE(pm.taken_at, mi.created_at) DESC
        LIMIT 1
    ) AS cover_path
FROM collections c
WHERE c.user_id = sqlc.arg('user_id') AND c.type = 'photo_album'
ORDER BY c.updated_at DESC, c.name;

-- name: ListPhotoAlbumItems :many
-- Photos in an album, joined with photo_metadata so the grid can render
-- date-taken and dimensions without a second round-trip. Ordered by EXIF
-- taken_at DESC (falling back to created_at), since photo albums are
-- typically reviewed chronologically rather than in arbitrary user order.
SELECT
    mi.id, mi.library_id, mi.title, mi.poster_path,
    pm.taken_at, pm.camera_make, pm.camera_model,
    pm.width, pm.height, pm.orientation,
    mi.created_at, mi.updated_at,
    ci.added_at
FROM collection_items ci
JOIN media_items mi ON mi.id = ci.media_item_id
LEFT JOIN photo_metadata pm ON pm.item_id = mi.id
WHERE ci.collection_id = $1
  AND mi.deleted_at IS NULL
  AND mi.type = 'photo'
  -- The caller's content-rating ceiling, applied on READ like
  -- ListCollectionItems: the add-time check alone went stale when a profile's
  -- ceiling was lowered after it filled the album.
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  -- Libraries the caller can open (NULL = all), filtered here rather than
  -- after the page is cut, so a page isn't short and total agrees with it.
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
ORDER BY COALESCE(pm.taken_at, mi.created_at) DESC, mi.id
-- NULLIF so a zero lim means "no limit" (the original behaviour); callers pass a
-- positive cap to bound the result. Avoids a forgotten lim silently returning 0 rows.
LIMIT NULLIF(sqlc.arg('lim')::int, 0) OFFSET sqlc.arg('off')::int;

-- name: CountPhotoAlbumItems :one
SELECT COUNT(*)::bigint
FROM collection_items ci
JOIN media_items mi ON mi.id = ci.media_item_id
WHERE ci.collection_id = $1 AND mi.deleted_at IS NULL AND mi.type = 'photo'
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]));
