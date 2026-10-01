-- +goose Up
-- +goose StatementBegin
-- A watch event without a duration keeps the duration watch_progress already
-- has. 00023's rollup copied the latest event's duration over the stored one,
-- so a single progress report that carried no duration (a player that hadn't
-- learned the length yet) erased a known one, and the item read unwatched and
-- dropped out of Continue Watching until a report with a duration came in.
--
-- Completion is still judged only against the event's own duration: an event
-- without one never sets last_completed_at, even when its position is past
-- 90% of the stored duration. That duration is whatever a client last
-- reported, so an event it didn't come from can't mark the item watched (or
-- scrobble it) against it. Everything else is 00023's function unchanged.
CREATE OR REPLACE FUNCTION public.watch_progress_apply_event() RETURNS trigger
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
                           THEN COALESCE(EXCLUDED.duration_ms, wp.duration_ms) ELSE wp.duration_ms END,
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

-- +goose Down
-- +goose StatementBegin
-- 00023's function: the latest event's duration replaces the stored one,
-- NULL included.
CREATE OR REPLACE FUNCTION public.watch_progress_apply_event() RETURNS trigger
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
