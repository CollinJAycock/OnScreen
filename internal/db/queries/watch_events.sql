-- Watch event queries for Phase 2 playback recording.

-- name: InsertWatchEvent :one
INSERT INTO watch_events (
    user_id, media_id, file_id, session_id,
    event_type, position_ms, duration_ms,
    client_id, client_name, client_ip, decision, occurred_at
) VALUES (
    @user_id, @media_id, @file_id, @session_id,
    @event_type, @position_ms, @duration_ms,
    @client_id, @client_name, @client_ip, @decision, @occurred_at
) RETURNING id, occurred_at;

-- name: GetWatchState :one
-- Resolves the caller's watch state for one item from user_watch_state
-- (migration 00023) — the single derivation every surface shares. The
-- underlying watch_progress rollup is updated by a trigger on every
-- watch_events insert, so a `play` tick lands on the next detail-page fetch
-- even if the player is force-killed before its final `stop`.
--
-- "watched" is sticky: once any session since the latest manual mark passed
-- 90%, the status stays "watched" even if the user later rewatched from the
-- start (Plex / Jellyfin behave the same). A manual mark-as-unwatched clears
-- it; a manual mark-as-watched sets it. position_ms is the latest event's
-- position unless a newer mark reset it to 0; resumable says whether that
-- position is a mid-way resume point. No row = the user never touched the
-- item (callers treat ErrNoRows as unwatched).
SELECT user_id, media_id, position_ms, duration_ms, status, resumable,
       last_activity_at AS last_watched_at,
       last_client_id, last_client_name
FROM user_watch_state
WHERE user_id = $1 AND media_id = $2;

-- name: GetWatchStatesForItems :many
-- Batch form of GetWatchState for a set of media IDs — used by the children
-- listing to avoid an N+1. Media the user has no row for simply don't appear;
-- the caller treats an absent id as unwatched.
SELECT user_id, media_id, position_ms, duration_ms, status, resumable,
       last_activity_at AS last_watched_at,
       last_client_id, last_client_name
FROM user_watch_state
WHERE user_id = sqlc.arg('user_id')
  AND media_id = ANY(sqlc.arg('media_ids')::uuid[]);

-- name: ListWatchStateForUser :many
SELECT user_id, media_id, position_ms, duration_ms, status, resumable,
       last_activity_at AS last_watched_at,
       last_client_id, last_client_name
FROM user_watch_state
WHERE user_id = $1
ORDER BY last_activity_at DESC
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
