-- +goose Up
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS github_id   TEXT UNIQUE,
    ADD COLUMN IF NOT EXISTS discord_id  TEXT UNIQUE;

-- +goose Down
ALTER TABLE users
    DROP COLUMN IF EXISTS discord_id,
    DROP COLUMN IF EXISTS github_id;
