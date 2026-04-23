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
SELECT user_id, media_id, position_ms, duration_ms, status, last_watched_at,
       last_client_id, last_client_name
FROM watch_state
WHERE user_id = $1 AND media_id = $2;

-- name: ListWatchStateForUser :many
SELECT user_id, media_id, position_ms, duration_ms, status, last_watched_at,
       last_client_id, last_client_name
FROM watch_state
WHERE user_id = $1
ORDER BY last_watched_at DESC
LIMIT 10000;

-- name: ListWatchHistory :many
-- Collapse consecutive 'stop'/'scrobble' events for the same media that occur
-- within a 30-minute window into a single row, keeping the LATEST event in the
-- group. This prevents the same playback session from showing multiple times
-- in the user's history when both an explicit stop and an onDestroy stop fire,
-- or when external clients emit redundant scrobble events.
WITH events AS (
    SELECT we.id, we.user_id, we.media_id, we.event_type,
           we.position_ms, we.duration_ms, we.client_name, we.client_id,
           we.occurred_at,
           LEAD(we.occurred_at) OVER (
               PARTITION BY we.user_id, we.media_id
               ORDER BY we.occurred_at
           ) AS next_at
    FROM watch_events we
    WHERE we.user_id = $1
      AND we.event_type IN ('stop', 'scrobble')
)
SELECT e.id, e.user_id, e.media_id, e.event_type,
       e.position_ms, e.duration_ms, e.client_name, e.client_id,
       e.occurred_at,
       m.library_id AS library_id,
       m.type AS media_type, m.title AS media_title, m.year AS media_year,
       m.thumb_path AS media_thumb
FROM events e
JOIN media_items m ON m.id = e.media_id
WHERE e.next_at IS NULL OR (e.next_at - e.occurred_at) > INTERVAL '30 minutes'
ORDER BY e.occurred_at DESC
LIMIT $2 OFFSET $3;
