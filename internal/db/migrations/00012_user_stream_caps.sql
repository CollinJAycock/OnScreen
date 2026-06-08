-- +goose Up
-- +goose StatementBegin
-- Admin-enforced per-user streaming ceilings (NULL = no cap). Distinct from the
-- self-set quality preference (max_video_bitrate_kbps): these are set only by an
-- admin and clamped server-side at transcode start, so a shared / guest account
-- can't exceed them no matter what the client requests.
ALTER TABLE public.users
    ADD COLUMN max_concurrent_streams integer,
    ADD COLUMN max_stream_bitrate_kbps integer;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.users
    DROP COLUMN IF EXISTS max_concurrent_streams,
    DROP COLUMN IF EXISTS max_stream_bitrate_kbps;
-- +goose StatementEnd
