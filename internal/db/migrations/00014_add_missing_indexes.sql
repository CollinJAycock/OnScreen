-- +goose Up
-- Add missing indexes for OAuth user lookups and token tables.
-- These columns are queried by GetUserByEmail, GetUserByGoogleID, etc.

CREATE INDEX IF NOT EXISTS idx_users_email ON users (email) WHERE email IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_google_id ON users (google_id) WHERE google_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_github_id ON users (github_id) WHERE github_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_users_discord_id ON users (discord_id) WHERE discord_id IS NOT NULL;

-- Partial indexes for active (unused) tokens — covers the WHERE clause in lookup queries.
CREATE INDEX IF NOT EXISTS idx_password_reset_tokens_hash
    ON password_reset_tokens (token_hash)
    WHERE used_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_invite_tokens_hash
    ON invite_tokens (token_hash)
    WHERE used_at IS NULL;

-- Watch events foreign key index for history JOINs.
CREATE INDEX IF NOT EXISTS idx_watch_events_file_id ON watch_events (file_id);

-- +goose Down
DROP INDEX IF EXISTS idx_watch_events_file_id;
DROP INDEX IF EXISTS idx_invite_tokens_hash;
DROP INDEX IF EXISTS idx_password_reset_tokens_hash;
DROP INDEX IF EXISTS idx_users_discord_id;
DROP INDEX IF EXISTS idx_users_github_id;
DROP INDEX IF EXISTS idx_users_google_id;
DROP INDEX IF EXISTS idx_users_email;
