-- +goose Up
-- +goose StatementBegin
-- Video bit depth (8 / 10 / 12) of the file's primary video stream, derived
-- from the stream's pix_fmt (e.g. yuv420p10le → 10) at scan time. Distinct
-- from media_files.bit_depth, which is the AUDIO bit depth (16/24-bit PCM/
-- FLAC). Used by the play-decision path to avoid direct-playing / remuxing
-- 10-bit video (HEVC Main 10, H.264 Hi10P) to a client that can only decode
-- 8-bit. NULL until the file is (re)probed; treated as 8-bit by clients.
ALTER TABLE public.media_files ADD COLUMN video_bit_depth integer;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.media_files DROP COLUMN video_bit_depth;
-- +goose StatementEnd
