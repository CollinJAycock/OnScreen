-- Admin labels on media items (media_items.labels, migration 00047) and the
-- per-user label restriction built on them. Labels arrive here already
-- normalised by internal/itemlabel; nothing in this file normalises.

-- name: GetMediaItemLabels :one
-- One item's own labels (not inherited ones), for the admin item view.
SELECT labels FROM media_items WHERE id = $1 AND deleted_at IS NULL;

-- name: SetMediaItemLabels :execrows
-- Replaces one item's labels. Deliberately leaves updated_at alone: labels
-- are access metadata, and bumping it would make every client re-sync the
-- item's metadata for a change none of them display.
UPDATE media_items
SET labels = sqlc.arg('labels')::text[]
WHERE id = sqlc.arg('id')::uuid AND deleted_at IS NULL;

-- name: AddMediaItemLabels :execrows
-- Bulk add. Each item ends up with the sorted union of its labels and the
-- given ones. An item that would exceed max_per_item is left unchanged (and
-- not counted), so a bulk add can never push an item past the cap.
UPDATE media_items mi
SET labels = merged.labels
FROM (
    SELECT m.id,
           ARRAY(SELECT DISTINCT l FROM unnest(m.labels || sqlc.arg('labels')::text[]) AS l ORDER BY l) AS labels
    FROM media_items m
    WHERE m.id = ANY(sqlc.arg('ids')::uuid[]) AND m.deleted_at IS NULL
) merged
WHERE mi.id = merged.id
  AND mi.labels IS DISTINCT FROM merged.labels
  AND cardinality(merged.labels) <= sqlc.arg('max_per_item')::int;

-- name: RemoveMediaItemLabels :execrows
-- Bulk remove; items carrying none of the labels are untouched.
UPDATE media_items
SET labels = ARRAY(SELECT l FROM unnest(labels) AS l WHERE l <> ALL(sqlc.arg('labels')::text[]) ORDER BY l)
WHERE id = ANY(sqlc.arg('ids')::uuid[])
  AND deleted_at IS NULL
  AND labels && sqlc.arg('labels')::text[];

-- name: ListLabelsInUse :many
-- Every label on a live item with how many items carry it, for the admin
-- autocomplete. Reads only labelled rows (idx_media_items_labeled).
SELECT l::text AS label, count(*)::bigint AS item_count
FROM media_items, unnest(labels) AS l
WHERE labels <> '{}'::text[] AND deleted_at IS NULL
GROUP BY l
ORDER BY l;

-- name: ItemLabelsPermitted :one
-- The per-item gate the Go handlers run wherever they apply the rating
-- ceiling to a single item (detail, stream, subtitles, trickplay, ...).
-- Inherited labels count (see media_item_labels_permitted); a missing item
-- is not permitted.
SELECT media_item_id_labels_permitted(sqlc.arg('id')::uuid,
                                      sqlc.narg('blocked_labels')::text[],
                                      sqlc.narg('allowed_labels')::text[])::boolean AS permitted;

-- name: ArtworkLabelsPermitted :one
-- Label half of the /artwork/* gate (GetArtworkContentRating is the rating
-- half): true only when the path belongs to at least one live item in the
-- library and every item it belongs to passes the caller's restriction.
-- NULL (no owning item) is coalesced to false, matching the rating gate's
-- fail-closed handling of unknown provenance.
SELECT COALESCE(bool_and(media_item_labels_permitted(labels, parent_id,
                                                      sqlc.narg('blocked_labels')::text[],
                                                      sqlc.narg('allowed_labels')::text[])), false)::boolean AS permitted
FROM media_items
WHERE library_id = sqlc.arg(library_id)
  AND deleted_at IS NULL
  AND (lower(poster_path) = lower(sqlc.arg(art_path))
       OR lower(fanart_path) = lower(sqlc.arg(art_path))
       OR lower(thumb_path) = lower(sqlc.arg(art_path))
       OR lower(logo_path) = lower(sqlc.arg(art_path))
       OR lower(banner_path) = lower(sqlc.arg(art_path))
       OR lower(disc_art_path) = lower(sqlc.arg(art_path)));

-- name: UpdateUserLabelRestrictions :exec
-- Sets a user's blocked / allowed labels on the target AND every managed
-- profile beneath it — the same cascade as UpdateUserContentRating, for the
-- same reason: a profile must never be less restricted than its owner.
UPDATE users
SET blocked_labels = sqlc.arg('blocked_labels')::text[],
    allowed_labels = sqlc.arg('allowed_labels')::text[],
    updated_at = NOW()
WHERE id = sqlc.arg('id')::uuid OR parent_user_id = sqlc.arg('id')::uuid;

-- name: GetUserLabelRestrictions :one
SELECT blocked_labels, allowed_labels FROM users WHERE id = $1;

-- name: FilterLabelPermittedItems :many
-- The batched form of ItemLabelsPermitted: which of ids pass the caller's
-- label restriction. For Go-side filters over a list already in hand (an
-- item's children, a person's filmography) without a query per row.
SELECT id FROM media_items
WHERE id = ANY(sqlc.arg('ids')::uuid[])
  AND media_item_labels_permitted(labels, parent_id,
                                  sqlc.narg('blocked_labels')::text[],
                                  sqlc.narg('allowed_labels')::text[]);
