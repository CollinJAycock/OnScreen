-- name: ListOPDSBooks :many
-- Books for the OPDS catalog (/opds/…): one row per book with its first
-- active file, in the given book libraries (the caller's visible ones — the
-- handler always passes an explicit list), under the caller's rating ceiling
-- and label restriction. sort 'recent' lists newest first, anything else by
-- title; a non-NULL query narrows to search matches (same matching as
-- SearchMediaItems). Paged by lim/off.
SELECT mi.id, mi.library_id, mi.title, mi.original_title, mi.summary,
       mi.year, mi.genres, mi.poster_path, mi.created_at, mi.updated_at,
       f.id AS file_id, f.file_path, f.file_size
FROM media_items mi
JOIN LATERAL (
    SELECT mf.id, mf.file_path, mf.file_size FROM media_files mf
    WHERE mf.media_item_id = mi.id AND mf.status = 'active'
    ORDER BY mf.created_at, mf.id
    LIMIT 1
) f ON true
WHERE mi.library_id = ANY(sqlc.arg('library_ids')::uuid[])
  AND mi.type = 'book'
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('query')::text IS NULL
       OR mi.search_vector @@ websearch_to_tsquery('english', sqlc.narg('query')::text)
       OR mi.title % sqlc.narg('query')::text
       OR (mi.original_title IS NOT NULL AND mi.original_title % sqlc.narg('query')::text))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY
  CASE WHEN sqlc.arg('sort')::text = 'recent' THEN mi.created_at END DESC,
  mi.sort_title, mi.id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;
