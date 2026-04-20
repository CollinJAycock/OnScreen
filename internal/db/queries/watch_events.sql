-- Watch event queries for Phase 2 playback recording.

-- name: InsertWatchEvent :one
INSERT INTO watch_events (
    user_id, media_id, file_id, session_id,
    event_type, position_ms, duration_ms,
    client_id, client_name, client_ip, occurred_at
) VALUES (
    @user_id, @media_id, @file_id, @session_id,
    @event_type, @position_ms, @duration_ms,
    @client_id, @client_name, @client_ip, @occurred_at
) RETURNING id, occurred_at;

-- name: RefreshWatchState :exec
REFRESH MATERIALIZED VIEW CONCURRENTLY watch_state;

-- name: GetWatchState :one
SELECT user_id, media_id, position_ms, duration_ms, status, last_watched_at
FROM watch_state
WHERE user_id = $1 AND media_id = $2;

-- name: ListWatchStateForUser :many
SELECT user_id, media_id, position_ms, duration_ms, status, last_watched_at
FROM watch_state
WHERE user_id = $1
ORDER BY last_watched_at DESC
LIMIT 10000;

-- name: ListWatchHistory :many
SELECT we.id, we.user_id, we.media_id, we.event_type,
       we.position_ms, we.duration_ms, we.client_name, we.client_id,
       we.occurred_at,
       m.library_id AS library_id,
       m.type AS media_type, m.title AS media_title, m.year AS media_year,
       m.thumb_path AS media_thumb
FROM watch_events we
JOIN media_items m ON m.id = we.media_id
WHERE we.user_id = $1
  AND we.event_type IN ('stop', 'scrobble')
ORDER BY we.occurred_at DESC
LIMIT $2 OFFSET $3;
