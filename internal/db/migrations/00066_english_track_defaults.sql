-- +goose Up
-- +goose StatementBegin
-- English audio and subtitles by default, subtitles in the "foreign" mode
-- (owner decisions 2026-11-24; docs/foreign-audio-subtitles.md).
--
-- A preferred subtitle language turns that language's subtitles on by
-- itself, and subtitle_mode says when:
--   always  — whenever the file has them (full track before forced)
--   forced  — forced tracks only (foreign-language dialogue)
--   foreign — forced only while the audio is in that language; when the
--             audio is in another (known) language, the full track
-- So an English film plays without subtitles and a Japanese one with
-- English subtitles.
--
-- forced_subtitles_only stays for the apps that predate subtitle_mode: every
-- write keeps it equal to subtitle_mode <> 'always', so to those apps
-- "foreign" reads as forced-only, which is what they did before.
ALTER TABLE users
    ADD COLUMN subtitle_mode TEXT NOT NULL DEFAULT 'foreign'
        CONSTRAINT users_subtitle_mode_check CHECK (subtitle_mode IN ('always', 'forced', 'foreign'));
ALTER TABLE users ALTER COLUMN preferred_audio_lang SET DEFAULT 'eng';
ALTER TABLE users ALTER COLUMN preferred_subtitle_lang SET DEFAULT 'eng';
ALTER TABLE users ALTER COLUMN forced_subtitles_only SET DEFAULT true;

-- New accounts take the defaults (every INSERT INTO users leaves these
-- columns alone, managed profiles included). Existing accounts:
--   audio: no choice (NULL, or "" from an older client) → English.
--   subtitles chosen (including a TV app's "off"): the mode their
--     forced-only switch meant: on → forced, off → always.
--   subtitles not chosen → English, foreign.
UPDATE users SET preferred_audio_lang = 'eng'
 WHERE preferred_audio_lang IS NULL OR preferred_audio_lang = '';
UPDATE users SET subtitle_mode = CASE WHEN forced_subtitles_only THEN 'forced' ELSE 'always' END
 WHERE preferred_subtitle_lang IS NOT NULL AND preferred_subtitle_lang <> '';
UPDATE users SET preferred_subtitle_lang = 'eng', subtitle_mode = 'foreign', forced_subtitles_only = true
 WHERE preferred_subtitle_lang IS NULL OR preferred_subtitle_lang = '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The defaults go back and subtitle_mode goes. The backfilled languages
-- stay: they can't be told apart from the same choice made in Settings.
-- forced_subtitles_only already says what the older apps should do.
ALTER TABLE users DROP COLUMN subtitle_mode;
ALTER TABLE users ALTER COLUMN preferred_audio_lang DROP DEFAULT;
ALTER TABLE users ALTER COLUMN preferred_subtitle_lang DROP DEFAULT;
ALTER TABLE users ALTER COLUMN forced_subtitles_only SET DEFAULT false;
-- +goose StatementEnd
