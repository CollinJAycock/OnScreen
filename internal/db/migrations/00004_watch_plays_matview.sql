-- +goose Up
-- +goose StatementBegin
-- watch_plays was a plain VIEW that runs a lead() window over ALL stop/scrobble
-- history to apply its 30-min per-(user,media) dedupe. The analytics dashboard
-- hits it 5× per load (overview COUNT/SUM is unbounded; per-day/top/recent/
-- bandwidth are date-filtered but the date predicate can't be pushed inside the
-- window), and the page auto-refreshes every 30s — so that full-history window
-- was recomputed constantly on the request path and dominated page load.
--
-- watch_plays is analytics-only, which makes a periodically-refreshed
-- materialized copy safe: the window is computed off-request by the
-- refresh_watch_plays scheduled task (REFRESH ... CONCURRENTLY), and the
-- dashboard queries read a cheap, indexed table instead.
DROP VIEW IF EXISTS public.watch_plays;
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
-- Unique index is required for REFRESH MATERIALIZED VIEW CONCURRENTLY; id is the
-- source watch_events row id, unique per terminal event.
CREATE UNIQUE INDEX watch_plays_id_idx ON public.watch_plays (id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Drives every date-bounded analytics query (the 30/90-day windows).
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
CREATE VIEW public.watch_plays AS
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
