-- Studios (production companies) and networks, and which movies and shows
-- they made (migration 00049). Written by the enricher from TMDB and NFO
-- <studio> tags; read by /api/v1/studios and the item detail page.
--
-- Every read that lists items takes the caller's scope the way the library
-- browse queries do: library_ids (NULL = every library, i.e. admin or a
-- deployment without ACLs; empty = no grants), max_rating_rank (NULL = no
-- ceiling) and the label restriction (blocked_labels / allowed_labels, NULL
-- = none).

-- name: UpsertStudio :one
-- One row per (kind, name) whatever the case; a TMDB id fills in the first
-- time one is known and is kept after that.
INSERT INTO studios (name, kind, tmdb_id)
VALUES (sqlc.arg(name)::text, sqlc.arg(kind)::text, sqlc.narg(tmdb_id)::int)
ON CONFLICT (kind, lower(name)) DO UPDATE
SET tmdb_id = COALESCE(studios.tmdb_id, EXCLUDED.tmdb_id)
RETURNING id;

-- name: ReplaceItemStudios :exec
-- Sets an item's studios to studio_ids, in that order, in one statement so
-- a reader never sees the item between "old list removed" and "new list
-- added". An empty array clears them. The ids must be distinct.
WITH removed AS (
    DELETE FROM media_item_studios
    WHERE media_item_id = sqlc.arg(media_item_id)
      AND studio_id <> ALL(sqlc.arg(studio_ids)::uuid[])
)
INSERT INTO media_item_studios (media_item_id, studio_id, position)
SELECT sqlc.arg(media_item_id), s.id, (s.ord - 1)::int
FROM unnest(sqlc.arg(studio_ids)::uuid[]) WITH ORDINALITY AS s(id, ord)
ON CONFLICT (media_item_id, studio_id) DO UPDATE SET position = EXCLUDED.position;

-- name: ListItemStudios :many
-- An item's studios in provider order. The caller has already passed the
-- item's own visibility checks; a studio carries no visibility of its own.
SELECT s.id, s.name, s.kind, s.tmdb_id
FROM media_item_studios mis
JOIN studios s ON s.id = mis.studio_id
WHERE mis.media_item_id = $1
ORDER BY mis.position, s.name;

-- name: GetStudio :one
SELECT id, name, kind, tmdb_id FROM studios WHERE id = $1;

-- name: ListStudiosWithCounts :many
-- Studios with at least one item the caller can see, and how many, optionally
-- narrowed to one library and one kind.
SELECT s.id, s.name, s.kind, s.tmdb_id, count(DISTINCT mi.id)::bigint AS item_count
FROM studios s
JOIN media_item_studios mis ON mis.studio_id = s.id
JOIN media_items mi ON mi.id = mis.media_item_id
WHERE mi.deleted_at IS NULL
  AND (sqlc.narg('library_id')::uuid IS NULL OR mi.library_id = sqlc.narg('library_id')::uuid)
  AND (sqlc.narg('kind')::text IS NULL OR s.kind = sqlc.narg('kind')::text)
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
GROUP BY s.id, s.name, s.kind, s.tmdb_id
ORDER BY lower(s.name), s.kind;

-- name: ListStudioItems :many
-- A studio's items the caller can see, one page, sorted like library browse:
-- sort is title | year | rating | created_at, sort_asc the direction. Ties
-- fall back to the sort title, then the id, so pages never overlap.
SELECT mi.id, mi.library_id, mi.type, mi.title, mi.sort_title, mi.original_title,
       mi.year, mi.summary, mi.rating, mi.duration_ms, mi.genres, mi.poster_path,
       mi.content_rating, mi.originally_available_at, mi.created_at, mi.updated_at
FROM media_item_studios mis
JOIN media_items mi ON mi.id = mis.media_item_id
WHERE mis.studio_id = sqlc.arg(studio_id)
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('library_id')::uuid IS NULL OR mi.library_id = sqlc.narg('library_id')::uuid)
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY
    CASE WHEN sqlc.arg(sort)::text = 'year' AND sqlc.arg(sort_asc)::bool THEN mi.year END ASC NULLS LAST,
    CASE WHEN sqlc.arg(sort)::text = 'year' AND NOT sqlc.arg(sort_asc)::bool THEN mi.year END DESC NULLS LAST,
    CASE WHEN sqlc.arg(sort)::text = 'rating' AND sqlc.arg(sort_asc)::bool THEN mi.rating END ASC NULLS LAST,
    CASE WHEN sqlc.arg(sort)::text = 'rating' AND NOT sqlc.arg(sort_asc)::bool THEN mi.rating END DESC NULLS LAST,
    CASE WHEN sqlc.arg(sort)::text = 'created_at' AND sqlc.arg(sort_asc)::bool THEN mi.created_at END ASC,
    CASE WHEN sqlc.arg(sort)::text = 'created_at' AND NOT sqlc.arg(sort_asc)::bool THEN mi.created_at END DESC,
    CASE WHEN sqlc.arg(sort_asc)::bool THEN lower(mi.sort_title) END ASC,
    CASE WHEN NOT sqlc.arg(sort_asc)::bool THEN lower(mi.sort_title) END DESC,
    mi.id
LIMIT sqlc.arg(limit_n)::int OFFSET sqlc.arg(offset_n)::int;

-- name: CountStudioItems :one
-- The total behind ListStudioItems: same filters, no page.
SELECT count(*)::bigint
FROM media_item_studios mis
JOIN media_items mi ON mi.id = mis.media_item_id
WHERE mis.studio_id = sqlc.arg(studio_id)
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('library_id')::uuid IS NULL OR mi.library_id = sqlc.narg('library_id')::uuid)
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[]);
