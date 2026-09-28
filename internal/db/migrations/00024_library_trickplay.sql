-- +goose Up
-- +goose StatementBegin
-- Per-library switch for automatic trickplay (seek-bar thumbnail) generation.
-- When true, the server queues the library's video items that have no
-- sprites yet after every scan, and the nightly trickplay_backfill task works
-- through the back catalogue. Defaults false at the column level so a library
-- inserted by anything that doesn't name the column (old tooling, a restore of
-- a pre-migration dump) never opts in by accident; the create path turns it
-- on for new video libraries explicitly.
ALTER TABLE public.libraries
    ADD COLUMN trickplay_enabled boolean DEFAULT false NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Existing video libraries opt in: seek previews are the expected behaviour
-- for movies and series, and the generation queue is bounded (one ffmpeg job
-- at a time, paused while a transcode runs), so turning it on for a large
-- existing library costs background CPU, not playback headroom. Music, photo,
-- book, audiobook, podcast and DVR libraries stay off.
UPDATE public.libraries
SET trickplay_enabled = true
WHERE type IN ('movie', 'show', 'home_video', 'anime', 'cartoons');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.libraries
    DROP COLUMN IF EXISTS trickplay_enabled;
-- +goose StatementEnd
