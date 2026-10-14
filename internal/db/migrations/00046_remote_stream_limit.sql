-- +goose Up
-- +goose StatementBegin
-- Admin-set per-user ceiling for streams from OUTSIDE the home network (NULL =
-- no separate internet cap). Sits next to max_stream_bitrate_kbps (00012),
-- which keeps applying everywhere: a remote stream is held to the lowest of
-- the user's cap, this column, and the server-wide internet limit
-- (transcode_config.remote_max_bitrate_kbps in server_settings). A LAN stream
-- only ever sees max_stream_bitrate_kbps.
ALTER TABLE public.users
    ADD COLUMN max_remote_stream_bitrate_kbps integer;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.users
    DROP COLUMN IF EXISTS max_remote_stream_bitrate_kbps;
-- +goose StatementEnd
