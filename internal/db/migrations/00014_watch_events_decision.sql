-- +goose Up
-- +goose StatementBegin
-- Persist the playback decision (directPlay / directStream / remux /
-- transcode) on watch events so analytics can chart direct-vs-transcode load
-- over time. Until now the decision only existed on live transcode sessions —
-- there was zero historical visibility into how often the transcode pipeline
-- is actually exercised. Nullable: rows written by clients that predate the
-- field (and the entire backlog) read as "unknown".
ALTER TABLE watch_events ADD COLUMN decision text;
-- +goose StatementEnd

-- +goose StatementBegin
-- Rebuild the analytics matview to carry the new column. Definition is
-- otherwise identical to 00004 (30-min per-(user,media) dedupe of terminal
-- events).
DROP MATERIALIZED VIEW IF EXISTS public.watch_plays;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW public.watch_plays AS
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
            we.decision,
            we.occurred_at,
            lead(we.occurred_at) OVER (PARTITION BY we.user_id, we.media_id ORDER BY we.occurred_at) AS next_at
           FROM public.watch_events we
          WHERE (we.event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]))
        )
 SELECT id,
    user_id,
    media_id,
    file_id,
    session_id,
    event_type,
    position_ms,
    duration_ms,
    client_name,
    client_id,
    client_ip,
    decision,
    occurred_at
   FROM terminal
  WHERE ((next_at IS NULL) OR ((next_at - occurred_at) > '00:30:00'::interval));
-- +goose StatementEnd

-- +goose StatementBegin
-- Unique index is required for REFRESH MATERIALIZED VIEW CONCURRENTLY; id is the
-- source watch_events row id, unique per terminal event.
CREATE UNIQUE INDEX watch_plays_id_idx ON public.watch_plays (id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Drives every date-bounded analytics query (the 7/30/90-day windows).
CREATE INDEX watch_plays_occurred_at_idx ON public.watch_plays (occurred_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- Supports the top-played GROUP BY media_id and the recent/bandwidth joins.
CREATE INDEX watch_plays_media_id_idx ON public.watch_plays (media_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP MATERIALIZED VIEW IF EXISTS public.watch_plays;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE MATERIALIZED VIEW public.watch_plays AS
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
            lead(we.occurred_at) OVER (PARTITION BY we.user_id, we.media_id ORDER BY we.occurred_at) AS next_at
           FROM public.watch_events we
          WHERE (we.event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]))
        )
 SELECT id,
    user_id,
    media_id,
    file_id,
    session_id,
    event_type,
    position_ms,
    duration_ms,
    client_name,
    client_id,
    client_ip,
    occurred_at
   FROM terminal
  WHERE ((next_at IS NULL) OR ((next_at - occurred_at) > '00:30:00'::interval));
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX watch_plays_id_idx ON public.watch_plays (id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX watch_plays_occurred_at_idx ON public.watch_plays (occurred_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX watch_plays_media_id_idx ON public.watch_plays (media_id);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE watch_events DROP COLUMN IF EXISTS decision;
-- +goose StatementEnd
