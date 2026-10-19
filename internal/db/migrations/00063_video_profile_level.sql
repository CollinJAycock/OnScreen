-- +goose Up
-- +goose StatementBegin
-- The primary video stream's codec profile and level, as ffprobe reports
-- them: video_profile is its profile name ("High", "Main 10", "Main"),
-- video_level its raw level number (H.264 level_idc, e.g. 41 = 4.1; HEVC
-- general_level_idc, 30 x the level, e.g. 153 = 5.1; AV1 seq_level_idx,
-- e.g. 13 = 5.1). The play decision compares them with the maximum
-- profile / level a client declares in X-Client-Capabilities (h264Profile,
-- hevcLevel, ...). NULL until the file is (re)probed, and for a profile or
-- level ffprobe doesn't know; NULL is never gated.
ALTER TABLE public.media_files ADD COLUMN video_profile text;
ALTER TABLE public.media_files ADD COLUMN video_level integer;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.media_files DROP COLUMN video_level;
ALTER TABLE public.media_files DROP COLUMN video_profile;
-- +goose StatementEnd
