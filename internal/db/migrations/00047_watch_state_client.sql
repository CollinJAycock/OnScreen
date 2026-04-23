-- +goose Up
-- Extend watch_state to carry last_client_name + last_client_id so the
-- resume UI can surface "pick up where you left off on Living Room TV"
-- instead of just a bare position. The materialized view already picks
-- the most-recent event per (user, media); adding these columns is a
-- straight passthrough.

DROP MATERIALIZED VIEW watch_state CASCADE;

CREATE MATERIALIZED VIEW watch_state AS
SELECT DISTINCT ON (user_id, media_id)
    user_id,
    media_id,
    position_ms,
    duration_ms,
    CASE
        WHEN position_ms::float / NULLIF(duration_ms, 0) > 0.9 THEN 'watched'
        WHEN position_ms > 0                                    THEN 'in_progress'
        ELSE                                                         'unwatched'
    END AS status,
    occurred_at AS last_watched_at,
    client_id   AS last_client_id,
    client_name AS last_client_name
FROM watch_events
WHERE event_type IN ('stop', 'scrobble')
ORDER BY user_id, media_id, occurred_at DESC;

CREATE UNIQUE INDEX ON watch_state(user_id, media_id);
CREATE INDEX        ON watch_state(user_id, status);

-- +goose Down
-- Restore the original view shape so down-migrations are clean.
DROP MATERIALIZED VIEW watch_state CASCADE;

CREATE MATERIALIZED VIEW watch_state AS
SELECT DISTINCT ON (user_id, media_id)
    user_id,
    media_id,
    position_ms,
    duration_ms,
    CASE
        WHEN position_ms::float / NULLIF(duration_ms, 0) > 0.9 THEN 'watched'
        WHEN position_ms > 0                                    THEN 'in_progress'
        ELSE                                                         'unwatched'
    END AS status,
    occurred_at AS last_watched_at
FROM watch_events
WHERE event_type IN ('stop', 'scrobble')
ORDER BY user_id, media_id, occurred_at DESC;

CREATE UNIQUE INDEX ON watch_state(user_id, media_id);
CREATE INDEX        ON watch_state(user_id, status);
