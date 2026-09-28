-- +goose Up
-- +goose StatementBegin
-- Per-(user, media) watch record: a rollup of the user's watch_events for the
-- item plus their latest manual played/unplayed mark. This is the ONE place
-- watch state is derived from (through the user_watch_state view below); the
-- previous derivations — GetWatchState's direct watch_events scan, and the
-- watch_state materialized view that Continue Watching and recommendations
-- read — disagreed with each other (the view was not "sticky watched" and only
-- saw stop/scrobble events) and had no notion of a manual mark.
--
-- Event rollup columns (maintained by the watch_events trigger below, never by
-- application code, so every insert path — first-party players, scrobbles,
-- test fixtures — keeps it current):
--   position_ms / duration_ms / last_client_*: the latest event (by occurred_at)
--   last_event_at: occurred_at of that latest event
--   last_completed_at: the most recent event that passed 90% of the duration —
--       the "sticky watched" signal. Storing the time rather than a flag lets a
--       later unplayed mark supersede it (see the view).
--
-- Manual mark columns (written only by POST/DELETE /items/{id}/watched):
--   mark_state: 'watched' | 'unwatched'; marked_at: when. A mark overrides
--   everything that happened before marked_at; events after it derive
--   normally. Marks never touch watch_events, so play counts and every
--   analytics surface built on watch_plays are unaffected by them.
--
-- The rollup also outlives watch_events partition retention: detaching an old
-- month used to silently forget that the user had ever finished a title.
CREATE TABLE public.watch_progress (
    user_id uuid NOT NULL,
    media_id uuid NOT NULL,
    position_ms bigint DEFAULT 0 NOT NULL,
    duration_ms bigint,
    last_event_at timestamp with time zone,
    last_client_id text,
    last_client_name text,
    last_completed_at timestamp with time zone,
    mark_state text,
    marked_at timestamp with time zone,
    CONSTRAINT watch_progress_pkey PRIMARY KEY (user_id, media_id),
    CONSTRAINT watch_progress_user_id_fkey FOREIGN KEY (user_id)
        REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT watch_progress_media_id_fkey FOREIGN KEY (media_id)
        REFERENCES public.media_items(id) ON DELETE CASCADE,
    CONSTRAINT watch_progress_mark_state_check
        CHECK (mark_state IS NULL OR mark_state = ANY (ARRAY['watched'::text, 'unwatched'::text])),
    CONSTRAINT watch_progress_mark_pair_check
        CHECK ((mark_state IS NULL) = (marked_at IS NULL)),
    CONSTRAINT watch_progress_has_source_check
        CHECK (last_event_at IS NOT NULL OR marked_at IS NOT NULL)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- FK cascade from media_items deletes needs a media_id-leading index (the PK
-- leads with user_id), same reasoning as 00002's watch_events index.
CREATE INDEX idx_watch_progress_media ON public.watch_progress USING btree (media_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Continue Watching dismissals. A row hides media_id's tile — a movie, or a
-- SHOW for TV (episode and season dismissals are stored against their show,
-- matching the one-tile-per-show Continue Watching rollup) — from every
-- Continue Watching and Next Up row until the user records watch activity
-- newer than dismissed_at.
CREATE TABLE public.continue_watching_dismissals (
    user_id uuid NOT NULL,
    media_id uuid NOT NULL,
    dismissed_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT continue_watching_dismissals_pkey PRIMARY KEY (user_id, media_id),
    CONSTRAINT continue_watching_dismissals_user_id_fkey FOREIGN KEY (user_id)
        REFERENCES public.users(id) ON DELETE CASCADE,
    CONSTRAINT continue_watching_dismissals_media_id_fkey FOREIGN KEY (media_id)
        REFERENCES public.media_items(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_continue_watching_dismissals_media
    ON public.continue_watching_dismissals USING btree (media_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- Folds one watch_events row into watch_progress. Out-of-order arrivals (a
-- client flushing a queued event late) only move the "latest" columns when
-- they really are the latest; last_completed_at only ever moves forward.
-- The 90% threshold is the same one every earlier derivation used.
CREATE FUNCTION public.watch_progress_apply_event() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    INSERT INTO public.watch_progress AS wp (
        user_id, media_id, position_ms, duration_ms, last_event_at,
        last_client_id, last_client_name, last_completed_at
    ) VALUES (
        NEW.user_id, NEW.media_id, NEW.position_ms, NEW.duration_ms, NEW.occurred_at,
        NEW.client_id, NEW.client_name,
        CASE WHEN NEW.duration_ms > 0
                  AND NEW.position_ms::double precision / NEW.duration_ms::double precision > 0.9
             THEN NEW.occurred_at END
    )
    ON CONFLICT (user_id, media_id) DO UPDATE SET
        position_ms = CASE WHEN wp.last_event_at IS NULL OR EXCLUDED.last_event_at >= wp.last_event_at
                           THEN EXCLUDED.position_ms ELSE wp.position_ms END,
        duration_ms = CASE WHEN wp.last_event_at IS NULL OR EXCLUDED.last_event_at >= wp.last_event_at
                           THEN EXCLUDED.duration_ms ELSE wp.duration_ms END,
        last_client_id = CASE WHEN wp.last_event_at IS NULL OR EXCLUDED.last_event_at >= wp.last_event_at
                              THEN EXCLUDED.last_client_id ELSE wp.last_client_id END,
        last_client_name = CASE WHEN wp.last_event_at IS NULL OR EXCLUDED.last_event_at >= wp.last_event_at
                                THEN EXCLUDED.last_client_name ELSE wp.last_client_name END,
        last_event_at = GREATEST(wp.last_event_at, EXCLUDED.last_event_at),
        last_completed_at = GREATEST(wp.last_completed_at, EXCLUDED.last_completed_at);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Row triggers on a partitioned table are cloned onto every partition,
-- including ones the partition worker creates later. Created BEFORE the
-- backfill: CREATE TRIGGER takes a lock that blocks concurrent inserts into
-- watch_events for the rest of this transaction, so no event can land between
-- the backfill snapshot and the trigger going live.
CREATE TRIGGER watch_events_apply_progress
    AFTER INSERT ON public.watch_events
    FOR EACH ROW EXECUTE FUNCTION public.watch_progress_apply_event();
-- +goose StatementEnd

-- +goose StatementBegin
-- Backfill from the history still inside the retention window.
INSERT INTO public.watch_progress (
    user_id, media_id, position_ms, duration_ms, last_event_at,
    last_client_id, last_client_name, last_completed_at
)
SELECT DISTINCT ON (we.user_id, we.media_id)
       we.user_id, we.media_id, we.position_ms, we.duration_ms, we.occurred_at,
       we.client_id, we.client_name,
       max(we.occurred_at) FILTER (
           WHERE we.duration_ms > 0
             AND we.position_ms::double precision / we.duration_ms::double precision > 0.9
       ) OVER (PARTITION BY we.user_id, we.media_id)
FROM public.watch_events we
ORDER BY we.user_id, we.media_id, we.occurred_at DESC;
-- +goose StatementEnd

-- +goose StatementBegin
-- The single definition of a user's watch state for one item. Every query that
-- needs watched / in-progress / resume position reads this view.
--
--   live            events after the latest mark (or all events, never marked)
--   status          'watched'  — marked watched, or a live event passed 90%
--                                ("sticky": a rewatch from the start keeps it)
--                   'in_progress' — live latest event partway through
--                   'unwatched'   — otherwise (incl. marked unwatched with no
--                                   activity since)
--   position_ms     resume point: the live latest event's position, else 0
--   resumable       has a mid-way resume point (0 < pos <= 90%) from a live
--                   event — drives Continue Watching. Independent of the
--                   sticky status: a rewatch in progress of a watched title
--                   is watched AND resumable (Plex/Jellyfin behave the same).
--   last_activity_at  latest of the last event and the mark
CREATE VIEW public.user_watch_state AS
SELECT s.user_id,
       s.media_id,
       (CASE WHEN s.live THEN s.position_ms ELSE 0 END)::bigint AS position_ms,
       s.duration_ms,
       (CASE
            WHEN s.mark_state = 'watched' THEN 'watched'
            WHEN s.completed_live THEN 'watched'
            WHEN NOT s.live THEN 'unwatched'
            WHEN s.duration_ms IS NULL OR s.duration_ms <= 0 THEN 'unwatched'
            WHEN s.position_ms > 0 THEN 'in_progress'
            ELSE 'unwatched'
        END)::text AS status,
       COALESCE(s.live
            AND s.duration_ms > 0
            AND s.position_ms > 0
            AND s.position_ms::double precision / s.duration_ms::double precision <= 0.9,
            false)::boolean AS resumable,
       GREATEST(s.last_event_at, s.marked_at)::timestamptz AS last_activity_at,
       s.last_event_at,
       s.last_client_id,
       s.last_client_name,
       s.mark_state,
       s.marked_at
FROM (
    SELECT wp.user_id, wp.media_id, wp.position_ms, wp.duration_ms,
           wp.last_event_at, wp.last_client_id, wp.last_client_name,
           wp.mark_state, wp.marked_at,
           (wp.last_event_at IS NOT NULL
                AND (wp.marked_at IS NULL OR wp.last_event_at > wp.marked_at)) AS live,
           (wp.last_completed_at IS NOT NULL
                AND (wp.marked_at IS NULL OR wp.last_completed_at > wp.marked_at)) AS completed_live
    FROM public.watch_progress wp
) s;
-- +goose StatementEnd

-- +goose StatementBegin
-- Maps each live episode to the containers it rolls up into: its direct parent
-- (the season, or the show for a flat-layout episode) and, through a season,
-- the show. Shared by every show/season rollup (library badges, the watch
-- filter, mark-watched on a show) so "the episodes of X" means one thing.
CREATE VIEW public.episode_ancestry AS
SELECT e.parent_id AS container_id, e.id AS episode_id, e.content_rating
FROM public.media_items e
WHERE e.type = 'episode' AND e.deleted_at IS NULL AND e.parent_id IS NOT NULL
UNION ALL
SELECT s.parent_id AS container_id, e.id AS episode_id, e.content_rating
FROM public.media_items s
JOIN public.media_items e
  ON e.parent_id = s.id AND e.type = 'episode' AND e.deleted_at IS NULL
WHERE s.type = 'season' AND s.deleted_at IS NULL AND s.parent_id IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Watch bucket for any item, as the library watch filter sees it.
--   leaf items: user_watch_state.status (absent row = unwatched)
--   show/season, over the episodes within p_max_rating_rank:
--     watched     — at least one episode and every one watched
--     in_progress — some watched or in progress, not all watched
--     unwatched   — nothing started (or no episodes)
CREATE FUNCTION public.media_watch_bucket(p_user_id uuid, p_media_id uuid, p_type text, p_max_rating_rank integer)
    RETURNS text
    LANGUAGE sql STABLE
    AS $$
    SELECT CASE
        WHEN p_type IN ('show', 'season') THEN (
            SELECT CASE
                WHEN count(*) > 0
                     AND count(*) FILTER (WHERE ws.status = 'watched') = count(*) THEN 'watched'
                WHEN count(*) FILTER (WHERE ws.status IN ('watched', 'in_progress')) > 0 THEN 'in_progress'
                ELSE 'unwatched'
            END
            FROM public.episode_ancestry ea
            LEFT JOIN public.user_watch_state ws
              ON ws.user_id = p_user_id AND ws.media_id = ea.episode_id
            WHERE ea.container_id = p_media_id
              AND (p_max_rating_rank IS NULL
                   OR public.content_rating_rank(ea.content_rating) <= p_max_rating_rank))
        ELSE COALESCE((SELECT ws.status FROM public.user_watch_state ws
                        WHERE ws.user_id = p_user_id AND ws.media_id = p_media_id), 'unwatched')
    END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Superseded by watch_progress + user_watch_state: nothing reads it any more,
-- and its debounced full-history REFRESH was the most expensive thing a
-- playback stop did.
DROP MATERIALIZED VIEW IF EXISTS public.watch_state;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE MATERIALIZED VIEW public.watch_state AS
 SELECT DISTINCT ON (user_id, media_id) user_id,
    media_id,
    position_ms,
    duration_ms,
        CASE
            WHEN (((position_ms)::double precision / (NULLIF(duration_ms, 0))::double precision) > (0.9)::double precision) THEN 'watched'::text
            WHEN (position_ms > 0) THEN 'in_progress'::text
            ELSE 'unwatched'::text
        END AS status,
    occurred_at AS last_watched_at,
    client_id AS last_client_id,
    client_name AS last_client_name
   FROM public.watch_events
  WHERE (event_type = ANY (ARRAY['stop'::text, 'scrobble'::text]))
  ORDER BY user_id, media_id, occurred_at DESC
  WITH NO DATA;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX watch_state_user_id_media_id_idx ON public.watch_state USING btree (user_id, media_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX watch_state_user_id_status_idx ON public.watch_state USING btree (user_id, status);
-- +goose StatementEnd

-- +goose StatementBegin
REFRESH MATERIALIZED VIEW public.watch_state;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS public.media_watch_bucket(uuid, uuid, text, integer);
-- +goose StatementEnd

-- +goose StatementBegin
DROP VIEW IF EXISTS public.episode_ancestry;
-- +goose StatementEnd

-- +goose StatementBegin
DROP VIEW IF EXISTS public.user_watch_state;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TRIGGER IF EXISTS watch_events_apply_progress ON public.watch_events;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS public.watch_progress_apply_event();
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS public.continue_watching_dismissals;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS public.watch_progress;
-- +goose StatementEnd
