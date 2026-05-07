-- +goose Up
-- Per-user quality + playback preferences. Consumed by native clients so
-- they can pre-validate stream requests without each client
-- re-implementing "am I on cellular → clamp to 1 Mbps."
--
-- All fields nullable = "no preference" — the client transcode decision
-- falls back to server-wide defaults (settings/transcode) when a user
-- hasn't set anything. Language prefs already live in this table from
-- an earlier migration; these columns round out the story.

ALTER TABLE users
    ADD COLUMN max_video_bitrate_kbps INT,
    ADD COLUMN max_audio_bitrate_kbps INT,
    ADD COLUMN max_video_height       INT,
    ADD COLUMN preferred_video_codec  TEXT,
    ADD COLUMN forced_subtitles_only  BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
-- IF EXISTS guards: a previous botched up-migration may have left
-- the columns half-applied; explicit guards make the down idempotent.
ALTER TABLE users
    DROP COLUMN IF EXISTS forced_subtitles_only,
    DROP COLUMN IF EXISTS preferred_video_codec,
    DROP COLUMN IF EXISTS max_video_height,
    DROP COLUMN IF EXISTS max_audio_bitrate_kbps,
    DROP COLUMN IF EXISTS max_video_bitrate_kbps;
