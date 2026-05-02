-- name: ListCollections :many
SELECT id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules, library_id
FROM collections
WHERE user_id IS NULL OR user_id = sqlc.narg('user_id')
ORDER BY sort_order, name;

-- name: GetCollection :one
SELECT id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules, library_id
FROM collections WHERE id = $1;

-- name: CreateCollection :one
-- v2.1 added the `rules` JSONB column for smart playlists. Static
-- collections / playlists pass NULL; smart_playlist rows store the
-- filter shape that resolves at query time.
INSERT INTO collections (user_id, name, description, type, genre, rules)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules, library_id;

-- name: UpdateCollection :one
UPDATE collections SET name = $2, description = $3, updated_at = NOW()
WHERE id = $1
RETURNING id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules, library_id;

-- name: DeleteCollection :exec
DELETE FROM collections WHERE id = $1;

-- name: ListCollectionItems :many
-- v2.1 Track G item 4: optional max_rating_rank gate filters items
-- whose content_rating ranks above the caller's ceiling. NULL ranks
-- (no max set, no rating recorded) pass through — the same lenient
-- semantics used by every other rating-gated query so that
-- as-yet-uncategorised media isn't hidden by accident.
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
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
ORDER BY ci.position;

-- name: ListItemsByGenre :many
-- v2.1 Track G item 4: optional max_rating_rank gate. Backs the
-- "More like /Action" auto-genre row on the home hub, which a kid
-- profile would otherwise see populated with R-rated thrillers.
SELECT id, library_id, type, title, sort_title, year, rating, poster_path, duration_ms, created_at
FROM media_items
WHERE deleted_at IS NULL
  AND sqlc.arg('genre')::text = ANY(genres)
  AND type IN ('movie', 'show')
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank')::int)
ORDER BY rating DESC NULLS LAST
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountItemsByGenre :one
-- Mirrors ListItemsByGenre's filter so the paginated total matches
-- the rows the user can actually see — without the same gate the
-- pagination footer would lie ("Page 1 of 12" but only 4 visible).
SELECT COUNT(*) FROM media_items
WHERE deleted_at IS NULL AND sqlc.arg('genre')::text = ANY(genres) AND type IN ('movie', 'show')
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(content_rating) <= sqlc.narg('max_rating_rank')::int);

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
RETURNING id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, library_id;

-- name: ListAutoGenreCollections :many
SELECT id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules, library_id
FROM collections
WHERE type = 'auto_genre'
ORDER BY name;

-- name: UpsertEventCollection :one
-- Find-or-create the event_folder collection for (libraryID, name).
-- The home_video scanner calls this once per file under a non-root
-- folder, so the same collection gets reused across scan passes and
-- across concurrent goroutines processing files in the same folder.
-- Idempotent via the partial unique index from migration 00071.
--
-- ON CONFLICT DO UPDATE on a no-op (updated_at = updated_at) lets the
-- RETURNING clause fire for both insert and conflict — a bare DO
-- NOTHING would return no rows on conflict, forcing a separate
-- SELECT round-trip just to fetch the existing id.
INSERT INTO collections (library_id, name, type)
VALUES ($1, $2, 'event_folder')
ON CONFLICT (library_id, name) WHERE type = 'event_folder'
DO UPDATE SET updated_at = collections.updated_at
RETURNING id, library_id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules;

-- name: ListEventCollectionsForLibrary :many
-- Auto-created event collections under one library, used by the
-- home_video library page to render the "Events" shelf above the
-- date-bucketed grid.
SELECT id, library_id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules
FROM collections
WHERE type = 'event_folder' AND library_id = $1
ORDER BY name;
