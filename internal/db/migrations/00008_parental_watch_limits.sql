-- +goose Up
-- +goose StatementBegin
-- Per-user parental watch policy. One row per user an admin has placed under
-- limits; no row (or all-NULL fields) means unrestricted. Strictly additive /
-- opt-in, mirroring user_scrobble. Enforced server-side at the progress and
-- transcode-start chokepoints. Times are minutes-from-midnight in the server's
-- local timezone — the self-hosted server lives in the household, so its local
-- clock is the household clock; a per-policy timezone can layer on later.
CREATE TABLE public.user_watch_limit (
    user_id              uuid NOT NULL,
    -- Daily cap in minutes of active watching. NULL = no daily cap.
    daily_limit_minutes  integer,
    -- Allowed window as minutes-from-local-midnight in [0,1440). When both are
    -- set: start < end is a same-day window; start > end is an overnight window
    -- that wraps midnight (e.g. 1320..360 = 22:00–06:00). NULL = no time-of-day
    -- restriction. The two columns are set/cleared together (enforced in the API).
    allowed_start_minute integer,
    allowed_end_minute   integer,
    created_at           timestamp with time zone DEFAULT now() NOT NULL,
    updated_at           timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT user_watch_limit_pkey PRIMARY KEY (user_id),
    CONSTRAINT user_watch_limit_user_fk FOREIGN KEY (user_id)
        REFERENCES public.users (id) ON DELETE CASCADE,
    CONSTRAINT user_watch_limit_daily_chk
        CHECK (daily_limit_minutes IS NULL OR daily_limit_minutes >= 0),
    CONSTRAINT user_watch_limit_start_chk
        CHECK (allowed_start_minute IS NULL OR (allowed_start_minute >= 0 AND allowed_start_minute < 1440)),
    CONSTRAINT user_watch_limit_end_chk
        CHECK (allowed_end_minute IS NULL OR (allowed_end_minute >= 0 AND allowed_end_minute < 1440))
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-user-per-day accumulator of active watch seconds, advanced from the
-- playback progress heartbeat. last_tick_at lets the upsert add only the
-- clamped wall-clock delta since the previous tick, so paused / closed-lid
-- gaps and missed heartbeats don't inflate the count. day is the server-local
-- calendar date; (user_id, day) is the natural key.
CREATE TABLE public.user_watch_usage (
    user_id         uuid NOT NULL,
    day             date NOT NULL,
    watched_seconds integer DEFAULT 0 NOT NULL,
    last_tick_at    timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT user_watch_usage_pkey PRIMARY KEY (user_id, day),
    CONSTRAINT user_watch_usage_user_fk FOREIGN KEY (user_id)
        REFERENCES public.users (id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS public.user_watch_usage;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS public.user_watch_limit;
-- +goose StatementEnd
