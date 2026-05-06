-- name: GetUserWatchStatus :one
-- Returns the per-(user, item) watching status. Used by the detail
-- page to render the current selection in the dropdown.
SELECT user_id, media_item_id, status, created_at, updated_at
FROM user_watch_status
WHERE user_id = $1 AND media_item_id = $2;

-- name: UpsertUserWatchStatus :one
-- Sets the watching status for the (user, item) pair. ON CONFLICT
-- updates only `status` + `updated_at` so first-set creates a row
-- and subsequent changes refresh the timestamp without losing the
-- created_at anchor (useful for "first marked Plan to Watch on …"
-- analytics later).
INSERT INTO user_watch_status (user_id, media_item_id, status)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, media_item_id) DO UPDATE
    SET status = EXCLUDED.status,
        updated_at = NOW()
RETURNING user_id, media_item_id, status, created_at, updated_at;

-- name: DeleteUserWatchStatus :exec
-- Removes the (user, item) row entirely. Distinct from setting
-- status to a sentinel value — "no status" is a meaningful state
-- (the user has neither queued nor classified the item) different
-- from "Plan to Watch".
DELETE FROM user_watch_status
WHERE user_id = $1 AND media_item_id = $2;

-- name: ListUserWatchStatusByStatus :many
-- Lists items the user has marked with `status`, joined to
-- media_items so the caller renders without a second round trip.
-- Drives the "Plan to Watch" / "Currently Watching" / etc. list
-- views — anime-tracker UX, generic across types.
SELECT mi.id, mi.library_id, mi.type, mi.title, mi.sort_title,
       mi.year, mi.summary, mi.rating, mi.duration_ms,
       mi.poster_path, mi.fanart_path, mi.thumb_path,
       mi.tmdb_id, mi.tvdb_id, mi.anilist_id, mi.mal_id, mi.kind,
       mi.parent_id, mi.index,
       uws.status, uws.created_at AS status_created_at,
       uws.updated_at AS status_updated_at
FROM user_watch_status uws
JOIN media_items mi ON mi.id = uws.media_item_id
WHERE uws.user_id = $1
  AND uws.status = $2
  AND mi.deleted_at IS NULL
ORDER BY uws.updated_at DESC
LIMIT $3 OFFSET $4;

-- name: CountUserWatchStatusByStatus :one
-- Total count for paginated UI — Plan to Watch list shows "1 of 47"
-- under the page heading.
SELECT COUNT(*)::bigint
FROM user_watch_status uws
JOIN media_items mi ON mi.id = uws.media_item_id
WHERE uws.user_id = $1
  AND uws.status = $2
  AND mi.deleted_at IS NULL;
