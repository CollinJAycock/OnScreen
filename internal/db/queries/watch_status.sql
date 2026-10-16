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

-- The watchlist queries (ListWatchlist, CountWatchlist,
-- CountWatchlistByStatusType) share one filter set, kept identical so the
-- paged rows, the total and the tab counts agree for a restricted user:
--   * only the caller's own rows (a managed profile's claims carry its own id)
--   * live items only (deleted_at IS NULL)
--   * library_ids: the caller's grant set (NULL = admin, no filter)
--   * the content-rating ceiling and the per-user label restriction. The
--     rating falls back to the parent's, then the grandparent's (as the digest
--     does): an episode or season usually carries no rating of its own, and
--     without the fallback an episode of a show above the ceiling would list.

-- name: ListWatchlist :many
-- The /watchlist page: the caller's watching-status rows joined to the item
-- and its parent / grandparent (season + show for an episode, album + artist
-- for a track) so a card renders without a second round trip. status NULL =
-- every status; item_type NULL = every type. sort 'title' orders by sort
-- title, anything else by when the item entered its current status, newest
-- first. The id tiebreaker keeps OFFSET paging stable.
SELECT mi.id, mi.library_id, mi.type, mi.title, mi.year, mi.index,
       mi.parent_id, mi.poster_path, mi.fanart_path, mi.thumb_path,
       mi.duration_ms,
       p.title       AS parent_title,
       p.index       AS parent_index,
       p.poster_path AS parent_poster_path,
       gp.id          AS grandparent_id,
       gp.title       AS grandparent_title,
       gp.poster_path AS grandparent_poster_path,
       uws.status,
       uws.created_at AS status_created_at,
       uws.updated_at AS status_updated_at,
       media_watch_bucket(sqlc.arg('user_id')::uuid, mi.id, mi.type, sqlc.narg('max_rating_rank')::int)::text AS watch_state
FROM user_watch_status uws
JOIN media_items mi ON mi.id = uws.media_item_id AND mi.deleted_at IS NULL
LEFT JOIN media_items p ON p.id = mi.parent_id AND p.deleted_at IS NULL
LEFT JOIN media_items gp ON gp.id = p.parent_id AND gp.deleted_at IS NULL
WHERE uws.user_id = sqlc.arg('user_id')::uuid
  AND (sqlc.narg('status')::text IS NULL OR uws.status = sqlc.narg('status')::text)
  AND (sqlc.narg('item_type')::text IS NULL OR mi.type = sqlc.narg('item_type')::text)
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(COALESCE(NULLIF(mi.content_rating, ''), NULLIF(p.content_rating, ''), gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
ORDER BY CASE WHEN sqlc.arg('sort')::text = 'title'
              THEN lower(COALESCE(NULLIF(mi.sort_title, ''), mi.title)) END ASC,
         uws.updated_at DESC, mi.id
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: CountWatchlist :one
-- Total for ListWatchlist's filters (the page's "N items" + paging).
SELECT COUNT(*)::bigint
FROM user_watch_status uws
JOIN media_items mi ON mi.id = uws.media_item_id AND mi.deleted_at IS NULL
LEFT JOIN media_items p ON p.id = mi.parent_id AND p.deleted_at IS NULL
LEFT JOIN media_items gp ON gp.id = p.parent_id AND gp.deleted_at IS NULL
WHERE uws.user_id = sqlc.arg('user_id')::uuid
  AND (sqlc.narg('status')::text IS NULL OR uws.status = sqlc.narg('status')::text)
  AND (sqlc.narg('item_type')::text IS NULL OR mi.type = sqlc.narg('item_type')::text)
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(COALESCE(NULLIF(mi.content_rating, ''), NULLIF(p.content_rating, ''), gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[]);

-- name: CountWatchlistByStatusType :many
-- Per-(status, type) counts under the same filters: the page's tab badges
-- and the type filter's options (only types the caller actually has).
SELECT uws.status, mi.type, COUNT(*)::bigint AS count
FROM user_watch_status uws
JOIN media_items mi ON mi.id = uws.media_item_id AND mi.deleted_at IS NULL
LEFT JOIN media_items p ON p.id = mi.parent_id AND p.deleted_at IS NULL
LEFT JOIN media_items gp ON gp.id = p.parent_id AND gp.deleted_at IS NULL
WHERE uws.user_id = sqlc.arg('user_id')::uuid
  AND (sqlc.narg('library_ids')::uuid[] IS NULL
       OR mi.library_id = ANY(sqlc.narg('library_ids')::uuid[]))
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(COALESCE(NULLIF(mi.content_rating, ''), NULLIF(p.content_rating, ''), gp.content_rating)) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(mi.labels, mi.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
GROUP BY uws.status, mi.type
ORDER BY uws.status, mi.type;
