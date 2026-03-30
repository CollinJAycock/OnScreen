-- +goose Up
ALTER TABLE users
    DROP COLUMN IF EXISTS plex_id,
    DROP COLUMN IF EXISTS plex_token,
    DROP COLUMN IF EXISTS plex_username,
    DROP COLUMN IF EXISTS plex_token_validated_at;

-- +goose Down
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS plex_id BIGINT UNIQUE,
    ADD COLUMN IF NOT EXISTS plex_token TEXT,
    ADD COLUMN IF NOT EXISTS plex_username TEXT,
    ADD COLUMN IF NOT EXISTS plex_token_validated_at TIMESTAMPTZ;
