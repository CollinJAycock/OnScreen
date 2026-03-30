-- +goose Up
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS google_id         TEXT UNIQUE,
    ADD COLUMN IF NOT EXISTS google_avatar_url  TEXT;

-- +goose Down
ALTER TABLE users
    DROP COLUMN IF EXISTS google_avatar_url,
    DROP COLUMN IF EXISTS google_id;
