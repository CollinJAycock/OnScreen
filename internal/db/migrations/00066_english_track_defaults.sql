-- +goose Up
-- +goose StatementBegin
-- English audio and subtitles by default (owner decision 2026-11-24).
-- Subtitles come with forced_subtitles_only on: a preferred subtitle
-- language turns that language's subtitles on whenever a file has them, and
-- forced-only limits that to forced tracks (foreign-language dialogue), so
-- an ordinary English title still plays without subtitles.
--
-- New accounts (every INSERT INTO users leaves these columns to their
-- defaults, managed profiles included) start that way, and accounts that
-- had made no choice (NULL, or "" from an older client) are moved to it.
-- A language someone chose is left alone, and so is their forced-only
-- setting. "File default" stays available: PUT /users/me/preferences with
-- null stores NULL, and the players then use the tracks the file marks
-- default, as before.
ALTER TABLE users ALTER COLUMN preferred_audio_lang SET DEFAULT 'eng';
ALTER TABLE users ALTER COLUMN preferred_subtitle_lang SET DEFAULT 'eng';
ALTER TABLE users ALTER COLUMN forced_subtitles_only SET DEFAULT true;

UPDATE users SET preferred_audio_lang = 'eng'
 WHERE preferred_audio_lang IS NULL OR preferred_audio_lang = '';
UPDATE users SET preferred_subtitle_lang = 'eng', forced_subtitles_only = true
 WHERE preferred_subtitle_lang IS NULL OR preferred_subtitle_lang = '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The defaults go back. The backfilled values stay: they can't be told
-- apart from the same choice made in Settings.
ALTER TABLE users ALTER COLUMN preferred_audio_lang DROP DEFAULT;
ALTER TABLE users ALTER COLUMN preferred_subtitle_lang DROP DEFAULT;
ALTER TABLE users ALTER COLUMN forced_subtitles_only SET DEFAULT false;
-- +goose StatementEnd
