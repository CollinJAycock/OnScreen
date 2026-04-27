-- name: ListMyPlaylists :many
-- Both static playlists (type='playlist', items in collection_items)
-- and smart playlists (type='smart_playlist', items resolved from
-- the rules JSONB at query time) — surface them in the same list so
-- the user sees all of theirs in one place. The handler renders the
-- smart-vs-static distinction via the type field.
SELECT id, user_id, name, description, type, genre, poster_path, sort_order, created_at, updated_at, rules
FROM collections
WHERE user_id = $1 AND type IN ('playlist', 'smart_playlist')
ORDER BY updated_at DESC, name;

-- name: ReorderPlaylistItems :exec
-- Takes an ordered array of media_item_ids and rewrites their positions 0..N-1.
-- Items not in the list keep their prior position — caller should pass the full list.
UPDATE collection_items ci
SET position = sub.idx - 1
FROM (
    SELECT id, idx
    FROM unnest(sqlc.arg('item_ids')::uuid[]) WITH ORDINALITY AS t(id, idx)
) sub
WHERE ci.collection_id = sqlc.arg('collection_id') AND ci.media_item_id = sub.id;
