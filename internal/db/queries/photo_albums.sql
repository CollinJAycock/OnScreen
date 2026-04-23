-- name: ListMyPhotoAlbums :many
-- Owned albums plus their item count and the most-recently-taken photo as a
-- cover candidate. Cover falls back to created_at when EXIF taken_at is
-- missing so albums of scanned-from-disk photos still get a tile.
SELECT
    c.id, c.user_id, c.name, c.description, c.type, c.poster_path,
    c.created_at, c.updated_at,
    COALESCE((SELECT COUNT(*) FROM collection_items ci WHERE ci.collection_id = c.id), 0)::bigint AS item_count,
    (
        SELECT mi.poster_path
        FROM collection_items ci
        JOIN media_items mi ON mi.id = ci.media_item_id
        LEFT JOIN photo_metadata pm ON pm.item_id = mi.id
        WHERE ci.collection_id = c.id
          AND mi.deleted_at IS NULL
          AND mi.type = 'photo'
        ORDER BY COALESCE(pm.taken_at, mi.created_at) DESC
        LIMIT 1
    ) AS cover_path
FROM collections c
WHERE c.user_id = $1 AND c.type = 'photo_album'
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
ORDER BY COALESCE(pm.taken_at, mi.created_at) DESC, mi.id;

-- name: CountPhotoAlbumItems :one
SELECT COUNT(*)::bigint
FROM collection_items ci
JOIN media_items mi ON mi.id = ci.media_item_id
WHERE ci.collection_id = $1 AND mi.deleted_at IS NULL AND mi.type = 'photo';
