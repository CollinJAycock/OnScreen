-- name: RebuildItemCooccurrence :exec
-- Replaces the entire item_cooccurrence table from current watch_events.
-- Run nightly by the update_item_cooccurrence scheduler task. Wrapped
-- in TRUNCATE-then-INSERT semantics so a partial run (process killed
-- mid-aggregation) doesn't leave a half-stale table — either the new
-- snapshot is in place or the previous one is.
--
-- The pair-generation join produces (user, item_a, item_b) tuples
-- where item_a < item_b across every user's watch history; counting
-- DISTINCT users per pair is the cooccurrence score. event_type filter
-- mirrors the trending query: 'play' / 'scrobble' / 'stop' represent
-- intent to watch (skipping 'pause' / 'seek' which fire constantly).
--
-- Limited to the last 365 days so a user's tastes from 5 years ago
-- don't dominate today's recommendations. The window is liberal —
-- aggressive trimming would starve the long-tail item recommendations
-- that are most valuable to surface.
WITH user_items AS (
    SELECT DISTINCT user_id, media_id
    FROM watch_events
    WHERE event_type IN ('play', 'scrobble', 'stop')
      AND occurred_at >= NOW() - INTERVAL '365 days'
),
pairs AS (
    SELECT
        ua.user_id           AS u,
        LEAST(ua.media_id, ub.media_id)    AS pair_a,
        GREATEST(ua.media_id, ub.media_id) AS pair_b
    FROM user_items ua
    JOIN user_items ub ON ub.user_id = ua.user_id AND ua.media_id < ub.media_id
)
INSERT INTO item_cooccurrence (item_a, item_b, score, computed_at)
SELECT pair_a, pair_b, COUNT(DISTINCT u) AS s, NOW()
FROM pairs
GROUP BY pair_a, pair_b
HAVING COUNT(DISTINCT u) > 0
ON CONFLICT (item_a, item_b) DO UPDATE
    SET score       = EXCLUDED.score,
        computed_at = EXCLUDED.computed_at;

-- name: TruncateItemCooccurrence :exec
-- Companion to RebuildItemCooccurrence — wipes stale pairs that no
-- longer have any cooccurrence (e.g. the only user who watched both
-- items deleted their account). The scheduler task runs TRUNCATE
-- before the rebuild so we don't accumulate dead rows.
TRUNCATE TABLE item_cooccurrence;

-- name: ListCooccurrentItems :many
-- Top N items most cooccurrent with the given seed item. Symmetric:
-- the seed could be in either column of the stored pair, so we union
-- both directions. Filters out items the user has already watched
-- (any non-empty watch_state row counts as watched), and items whose
-- type isn't directly playable (no shows / artists / podcasts as
-- recommendation targets — only movie / episode).
--
-- max_rating_rank applies the per-user parental ceiling. Library
-- access is enforced in the handler (the query doesn't know the
-- caller's grant set).
SELECT m.id, m.library_id, m.type, m.title,
       m.year, m.poster_path, m.fanart_path, m.thumb_path,
       m.duration_ms, m.updated_at,
       co.score
FROM (
    SELECT ic1.item_b AS other_id, ic1.score FROM item_cooccurrence ic1 WHERE ic1.item_a = sqlc.arg('seed')
    UNION ALL
    SELECT ic2.item_a AS other_id, ic2.score FROM item_cooccurrence ic2 WHERE ic2.item_b = sqlc.arg('seed')
) co
JOIN media_items m ON m.id = co.other_id
LEFT JOIN watch_state ws ON ws.media_id = m.id AND ws.user_id = sqlc.arg('user_id')
WHERE m.deleted_at IS NULL
  AND m.type IN ('movie', 'episode')
  AND ws.media_id IS NULL  -- exclude already-watched
  AND (sqlc.narg('max_rating_rank')::int IS NULL OR content_rating_rank(m.content_rating) <= sqlc.narg('max_rating_rank'))
ORDER BY co.score DESC, m.updated_at DESC
LIMIT sqlc.arg('result_limit')::int;

-- name: ListSeedItemsForUser :many
-- The user's most-recently completed items, used as seeds for
-- "Because you watched X" rows. "Completed" here is generous —
-- watch_state.status can be 'completed' (explicit) or position past
-- 90% of duration (implicit). Stops at LIMIT seeds because the home
-- hub renders one row per seed; > 3 rows of the same shape gets
-- noisy.
SELECT m.id, m.title, m.poster_path, m.thumb_path, m.updated_at
FROM watch_state ws
JOIN media_items m ON m.id = ws.media_id
WHERE ws.user_id = $1
  AND m.deleted_at IS NULL
  AND m.type IN ('movie', 'episode')
  AND (ws.status = 'completed'
       OR (ws.duration_ms > 0 AND ws.position_ms::float / ws.duration_ms::float >= 0.9))
ORDER BY ws.last_watched_at DESC
LIMIT $2;
