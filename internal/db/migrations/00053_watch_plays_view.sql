-- +goose Up
-- A single playback session fires two terminal watch_events:
--   * 'scrobble' when the play threshold is crossed (Last.fm convention)
--   * 'stop' when the user navigates away
-- Analytics queries that count or sum these rows naively double-count
-- the same play. The history endpoint already works around this inline
-- with a LEAD-based CTE (see queries/watch_events.sql ListWatchHistory);
-- centralize that dedup as a view so the analytics queries stop
-- reinventing it and so the "what counts as a play" definition lives
-- in one place.
--
-- Rule: within a (user_id, media_id) timeline, any stop/scrobble whose
-- next same-media event lands within 30 minutes is treated as a
-- redundant tail of the same session and dropped. The surviving row is
-- the last event in the cluster, so its occurred_at and duration_ms
-- reflect the end of the playback session.
CREATE VIEW watch_plays AS
WITH terminal AS (
    SELECT we.id,
           we.user_id,
           we.media_id,
           we.file_id,
           we.session_id,
           we.event_type,
           we.position_ms,
           we.duration_ms,
           we.client_name,
           we.client_id,
           we.client_ip,
           we.occurred_at,
           LEAD(we.occurred_at) OVER (
               PARTITION BY we.user_id, we.media_id
               ORDER BY we.occurred_at
           ) AS next_at
    FROM watch_events we
    WHERE we.event_type IN ('stop', 'scrobble')
)
SELECT id, user_id, media_id, file_id, session_id, event_type,
       position_ms, duration_ms, client_name, client_id, client_ip,
       occurred_at
FROM terminal
WHERE next_at IS NULL
   OR (next_at - occurred_at) > INTERVAL '30 minutes';

-- +goose Down
DROP VIEW IF EXISTS watch_plays;
