-- +goose Up
ALTER TABLE users ADD COLUMN preferred_audio_lang TEXT;
ALTER TABLE users ADD COLUMN preferred_subtitle_lang TEXT;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS preferred_subtitle_lang;
ALTER TABLE users DROP COLUMN IF EXISTS preferred_audio_lang;
