-- M3U playlist import and export (GET /playlists/{id}/export.m3u8,
-- POST /playlists/import). Every query takes the caller's visibility — the
-- library grant set (NULL = admin, every library), the content-rating ceiling
-- and the label restriction — so neither direction can reach an item the
-- caller can't see.

-- name: ListM3UExportRows :many
-- One row per item id, in the order given, for the items that have an active
-- file and pass the caller's visibility. artist is what the #EXTINF line
-- shows before the title: a track's primary credited artist, else its
-- grandparent (the album artist for a track, the show for an episode).
SELECT mi.id, mi.library_id, mi.type, mi.title, mi.duration_ms,
       COALESCE(
         (SELECT a.title FROM track_artists ta
            JOIN media_items a ON a.id = ta.artist_id
           WHERE ta.track_id = mi.id AND ta.role = 'artist'
           ORDER BY ta.position LIMIT 1),
         gp.title, '')::text AS artist,
       f.file_path, f.duration_ms AS file_duration_ms,
       l.scan_paths
FROM unnest(sqlc.arg('item_ids')::uuid[]) WITH ORDINALITY AS t(id, ord)
JOIN media_items mi ON mi.id = t.id
JOIN libraries l ON l.id = mi.library_id
LEFT JOIN media_items p ON p.id = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
JOIN LATERAL (
    SELECT mf.file_path, mf.duration_ms FROM media_files mf
    WHERE mf.media_item_id = mi.id AND mf.status = 'active'
    ORDER BY mf.part_index NULLS FIRST, mf.created_at, mf.id
    LIMIT 1
) f ON true
WHERE mi.deleted_at IS NULL
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY t.ord;

-- name: FindM3UFilesByPath :many
-- Exact path lookup (media_files_file_path_key) for import entries: the
-- absolute paths as written, plus the candidates built from a relative entry
-- and each library root.
SELECT f.file_path, f.media_item_id, mi.title
FROM media_files f
JOIN media_items mi ON mi.id = f.media_item_id
WHERE f.file_path = ANY(sqlc.arg('paths')::text[])
  AND f.status = 'active'
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[]);

-- name: FindM3UFilesByBasename :many
-- Candidates for the suffix match: files whose lower-cased file name equals
-- one of the entries' file names. The handler then compares the trailing
-- directory components. One pass over media_files per import; capped so a
-- playlist of very common file names can't pull the whole table.
SELECT f.file_path, f.media_item_id, mi.title
FROM media_files f
JOIN media_items mi ON mi.id = f.media_item_id
WHERE lower(regexp_replace(f.file_path, '^.*[\\/]', '')) = ANY(sqlc.arg('basenames')::text[])
  AND f.status = 'active'
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY f.file_path
LIMIT sqlc.arg('lim')::int;

-- name: FindM3UTracksByTitle :many
-- Last resort for an entry no path matched: tracks whose title equals the
-- #EXTINF title (case-insensitively). The handler compares the artist.
SELECT mi.id, mi.title,
       COALESCE(
         (SELECT a.title FROM track_artists ta
            JOIN media_items a ON a.id = ta.artist_id
           WHERE ta.track_id = mi.id AND ta.role = 'artist'
           ORDER BY ta.position LIMIT 1),
         gp.title, '')::text AS artist,
       COALESCE(gp.title, '')::text AS album_artist
FROM media_items mi
LEFT JOIN media_items p ON p.id = mi.parent_id
LEFT JOIN media_items gp ON gp.id = p.parent_id
WHERE mi.type = 'track'
  AND lower(mi.title) = ANY(sqlc.arg('titles')::text[])
  AND mi.deleted_at IS NULL
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(mi.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY mi.id
LIMIT sqlc.arg('lim')::int;

-- name: AppendCollectionItems :exec
-- Appends items to a playlist in the order given, after anything already in
-- it. An item already present is skipped (callers de-duplicate the list).
INSERT INTO collection_items (collection_id, media_item_id, position)
SELECT sqlc.arg('collection_id')::uuid, t.id,
       (t.ord - 1)::int + COALESCE(
         (SELECT MAX(ci.position) + 1 FROM collection_items ci
           WHERE ci.collection_id = sqlc.arg('collection_id')::uuid), 0)
FROM unnest(sqlc.arg('item_ids')::uuid[]) WITH ORDINALITY AS t(id, ord)
ON CONFLICT (collection_id, media_item_id) DO NOTHING;
