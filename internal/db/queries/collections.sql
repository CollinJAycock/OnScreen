-- name: ListCollections :many
SELECT id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at
FROM collections
WHERE user_id IS NULL OR user_id = sqlc.narg('user_id')
ORDER BY sort_order, name;

-- name: GetCollection :one
SELECT id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at
FROM collections WHERE id = $1;

-- name: CreateCollection :one
INSERT INTO collections (user_id, name, description, type, genre)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at;

-- name: UpdateCollection :one
UPDATE collections SET name = $2, description = $3, updated_at = NOW()
WHERE id = $1
RETURNING id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at;

-- name: DeleteCollection :exec
DELETE FROM collections WHERE id = $1;

-- name: ListCollectionItems :many
SELECT mi.id, mi.library_id, mi.type, mi.title, mi.sort_title,
       mi.year, mi.rating,
       COALESCE(grandparent.poster_path, parent.poster_path, mi.poster_path,
                grandparent.thumb_path, parent.thumb_path, mi.thumb_path) AS poster_path,
       mi.duration_ms,
       ci.position, ci.added_at
FROM collection_items ci
JOIN media_items mi ON mi.id = ci.media_item_id
LEFT JOIN media_items parent ON parent.id = mi.parent_id
LEFT JOIN media_items grandparent ON grandparent.id = parent.parent_id
WHERE ci.collection_id = $1 AND mi.deleted_at IS NULL
ORDER BY ci.position;

-- name: ListItemsByGenre :many
SELECT id, library_id, type, title, sort_title, year, rating, poster_path, duration_ms, created_at
FROM media_items
WHERE deleted_at IS NULL
  AND $1 = ANY(genres)
  AND type IN ('movie', 'show')
ORDER BY rating DESC NULLS LAST
LIMIT $2 OFFSET $3;

-- name: CountItemsByGenre :one
SELECT COUNT(*) FROM media_items
WHERE deleted_at IS NULL AND $1 = ANY(genres) AND type IN ('movie', 'show');

-- name: AddCollectionItem :one
INSERT INTO collection_items (collection_id, media_item_id, position)
VALUES ($1, $2, COALESCE((SELECT MAX(position)+1 FROM collection_items WHERE collection_id = $1), 0))
ON CONFLICT (collection_id, media_item_id) DO NOTHING
RETURNING id, collection_id, media_item_id, position, added_at;

-- name: RemoveCollectionItem :exec
DELETE FROM collection_items WHERE collection_id = $1 AND media_item_id = $2;

-- name: UpsertAutoGenreCollection :one
INSERT INTO collections (name, type, genre)
VALUES ($1, 'auto_genre', $1)
ON CONFLICT DO NOTHING
RETURNING id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at;

-- name: ListAutoGenreCollections :many
SELECT id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at
FROM collections
WHERE type = 'auto_genre'
ORDER BY name;
