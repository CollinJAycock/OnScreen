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
--
-- "Latest in its group" is a NOT EXISTS probe for a later terminal event of
-- the same media within 30 minutes, not a LEAD() window: the window had to
-- number every event the user ever finished before OFFSET could skip any,
-- so each page cost the whole history. The probe lets the planner walk
-- idx_watch_events_user_history newest-first and stop after LIMIT + OFFSET
-- rows, one indexed probe per row. The two agree: LEAD's next_at is the
-- nearest later event, so "next_at - occurred_at > 30 min" is "no event in
-- (occurred_at, occurred_at + 30 min]". Events sharing a timestamp, which
-- LEAD ordered arbitrarily (keeping one of them), keep the highest id here.
-- Paging from the watch_plays matview would read the same rows but up to a
-- refresh behind, and History has to show what just finished.
SELECT we.id, we.user_id, we.media_id, we.event_type,
       we.position_ms, we.duration_ms, we.client_name, we.client_id,
       we.occurred_at,
       m.library_id AS library_id,
       m.type AS media_type, m.title AS media_title, m.year AS media_year,
       -- Movies and tracks keep their art in poster_path (a track's is its
       -- album's); only episodes carry a thumb_path still, which the handler
       -- swaps for the show's poster. Without the fallbacks History showed
       -- movies and tracks as blank tiles.
       COALESCE(m.thumb_path, m.poster_path, par.poster_path) AS media_thumb
FROM watch_events we
JOIN media_items m ON m.id = we.media_id
LEFT JOIN media_items par ON par.id = m.parent_id
WHERE we.user_id = sqlc.arg('user_id')::uuid
  AND we.event_type IN ('stop', 'scrobble')
  AND NOT EXISTS (
      SELECT 1 FROM watch_events nx
      WHERE nx.user_id = we.user_id
        AND nx.media_id = we.media_id
        AND nx.event_type IN ('stop', 'scrobble')
        -- Both bounds on occurred_at so the probe is an index range; the
        -- id test breaks a same-timestamp tie.
        AND nx.occurred_at >= we.occurred_at
        AND nx.occurred_at <= we.occurred_at + INTERVAL '30 minutes'
        AND (nx.occurred_at > we.occurred_at OR nx.id > we.id)
  )
  AND (sqlc.narg('max_rating_rank')::int IS NULL
       OR content_rating_rank(m.content_rating) <= sqlc.narg('max_rating_rank')::int)
  AND media_item_labels_permitted(m.labels, m.parent_id, sqlc.narg('blocked_labels')::text[], sqlc.narg('allowed_labels')::text[])
-- id: a stable order for same-timestamp rows, so OFFSET pages don't overlap.
ORDER BY we.occurred_at DESC, we.id DESC
LIMIT sqlc.arg('lim')::int OFFSET sqlc.arg('off')::int;

-- name: WatchPlaysSourceFingerprint :one
-- What the watch_plays matview is computed from, in two numbers: how many
-- terminal (stop / scrobble) events exist and the newest one's time. The
-- refresh_watch_plays task compares it with the value at its last refresh
-- and skips the rebuild when nothing changed. The count catches inserts at
-- any timestamp (an import of old plays) and deletes (an item or user
-- removed, history cascaded away); max() alone would miss both. Both come
-- from one index-only pass over idx_watch_events_event_occurred, a small
-- fraction of the full window + diff a REFRESH ... CONCURRENTLY costs.
SELECT count(*)::bigint AS terminal_events,
       COALESCE(max(occurred_at), 'epoch'::timestamptz)::timestamptz AS latest_at
FROM watch_events
WHERE event_type IN ('stop', 'scrobble');
