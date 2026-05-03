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
-- Resolves the resume position for a single (user, media) pair by
-- reading the latest watch_event directly, instead of going through
-- the watch_state materialized view. The view filters
-- event_type IN ('stop', 'scrobble') and only refreshes on stop —
-- so during active playback (a stream of 'play' ticks every 10 s)
-- the view shows the last *finished* session, not the in-progress
-- one. If the player is force-killed before its final 'stop' PUT
-- lands, the resume position is lost entirely.
--
-- This query sees every event the client publishes, so the next
-- detail-page fetch always reflects the latest known position.
-- The view is still used by ListWatchStateForUser (history bulk
-- read) where eventual consistency is fine.
--
-- "watched" is sticky: once *any* past session for this (user,
-- media) reached past 90% completion, the status stays "watched"
-- even if the user later rewatched from the start (which would
-- otherwise overwrite the latest event with a tiny position and
-- demote the show back to in_progress). This matches Plex /
-- Jellyfin's behaviour — the watched indicator is meant to mean
-- "the user has finished this at least once," not "the latest
-- click landed past the 90% mark." Only a manual mark-as-unwatched
-- (separate flow) should clear it.
SELECT
    l.user_id,
    l.media_id,
    l.position_ms,
    l.duration_ms,
    CASE
        WHEN EXISTS (
            SELECT 1 FROM watch_events ec
            WHERE ec.user_id = $1 AND ec.media_id = $2
              AND ec.duration_ms IS NOT NULL AND ec.duration_ms > 0
              AND ec.position_ms::float / NULLIF(ec.duration_ms, 0) > 0.9
            LIMIT 1
        )                                                       THEN 'watched'
        WHEN l.duration_ms IS NULL OR l.duration_ms = 0         THEN 'unwatched'
        WHEN l.position_ms::float / NULLIF(l.duration_ms, 0) > 0.9 THEN 'watched'
        WHEN l.position_ms > 0                                  THEN 'in_progress'
        ELSE                                                         'unwatched'
    END AS status,
    l.occurred_at AS last_watched_at,
    l.client_id   AS last_client_id,
    l.client_name AS last_client_name
FROM watch_events l
WHERE l.user_id = $1 AND l.media_id = $2
ORDER BY l.occurred_at DESC
LIMIT 1;

-- name: ListWatchStateForUser :many
SELECT user_id, media_id, position_ms, duration_ms, status, last_watched_at,
       last_client_id, last_client_name
FROM watch_state
WHERE user_id = $1
ORDER BY last_watched_at DESC
LIMIT 10000;

-- name: ListRecentClientNamesForUser :many
-- Distinct client_name values the user has scrobbled from in the last
-- 30 days, sorted most-recently-used first. Drives the "Play on…"
-- device picker in the player chrome — devices that haven't reported
-- in a while are filtered out so the menu doesn't accumulate stale
-- entries (a phone the user has since replaced, a TV they don't own
-- anymore). 30 days is the same window the watch_events partitioning
-- uses so the query reads from the hot partition.
SELECT we.client_name, MAX(we.occurred_at)::timestamptz AS last_seen
FROM watch_events we
WHERE we.user_id = $1
  AND we.client_name IS NOT NULL
  AND we.client_name != ''
  AND we.occurred_at > NOW() - INTERVAL '30 days'
GROUP BY we.client_name
ORDER BY last_seen DESC
LIMIT 50;

-- name: ListWatchHistory :many
-- Collapse consecutive 'stop'/'scrobble' events for the same media that occur
-- within a 30-minute window into a single row, keeping the LATEST event in the
-- group. This prevents the same playback session from showing multiple times
-- in the user's history when both an explicit stop and an onDestroy stop fire,
-- or when external clients emit redundant scrobble events.
--
-- v2.1 Track G item 4: optional max_rating_rank gate. Hides items
-- whose content_rating ranks above the caller's ceiling — important
-- when an admin lowers a profile's ceiling after the profile has
-- already accumulated history; the old entries should disappear too,
-- not just future plays. Same lenient null-passes-through semantics
-- as the rest of the rating-gated queries.
WITH events AS (
    SELECT we.id, we.user_id, we.media_id, we.event_type,
           we.position_ms, we.duration_ms, we.client_name, we.client_id,
           we.occurred_at,
           LEAD(we.occurred_at) OVER (
               PARTITION BY we.user_id, we.media_id
               ORDER BY we.occurred_at
           ) AS next_at
    FROM watch_events we
    WHERE we.user_id = sqlc.arg('user_id')::uuid
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
WHERE (e.next_at IS NULL OR (e.next_at - e.occurred_at) > INTERVAL '30 minutes')
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(m.content_rating) <= sqlc.narg('max_rating_rank')::int)
ORDER BY e.occurred_at DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;
