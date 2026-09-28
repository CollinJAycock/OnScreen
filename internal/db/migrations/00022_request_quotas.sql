-- +goose Up
-- +goose StatementBegin
-- Per-user media-request limits, set by an admin (Settings ▸ Users).
--
--   can_request: false blocks the account from creating requests at all (the
--       API answers 403 REQUESTS_DISABLED). Defaults true so every existing
--       account keeps today's behaviour. Admins are never blocked.
--   request_quota_movies / request_quota_tv: how many requests of that type
--       the user may make per rolling window before further requests stop
--       being auto-approved and wait for an admin. NULL = use the server
--       default (Settings ▸ Requests, quota_movies / quota_tv), 0 = unlimited,
--       N > 0 = at most N. The window length is server-wide
--       (quota_window_days). Declined and cancelled requests don't count.
--       Admins are exempt.
ALTER TABLE public.users
    ADD COLUMN can_request boolean DEFAULT true NOT NULL,
    ADD COLUMN request_quota_movies integer,
    ADD COLUMN request_quota_tv integer,
    ADD CONSTRAINT users_request_quota_movies_check CHECK (request_quota_movies >= 0),
    ADD CONSTRAINT users_request_quota_tv_check CHECK (request_quota_tv >= 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.users
    DROP CONSTRAINT IF EXISTS users_request_quota_movies_check,
    DROP CONSTRAINT IF EXISTS users_request_quota_tv_check,
    DROP COLUMN IF EXISTS can_request,
    DROP COLUMN IF EXISTS request_quota_movies,
    DROP COLUMN IF EXISTS request_quota_tv;
-- +goose StatementEnd
