-- +goose Up
ALTER TABLE users ADD COLUMN parent_user_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE users ADD COLUMN avatar_url TEXT;

CREATE INDEX idx_users_parent ON users(parent_user_id) WHERE parent_user_id IS NOT NULL;

-- Managed profiles cannot be admins.
ALTER TABLE users ADD CONSTRAINT chk_managed_not_admin
    CHECK (parent_user_id IS NULL OR is_admin = false);

-- +goose Down
ALTER TABLE users DROP CONSTRAINT IF EXISTS chk_managed_not_admin;
DROP INDEX IF EXISTS idx_users_parent;
ALTER TABLE users DROP COLUMN IF EXISTS avatar_url;
ALTER TABLE users DROP COLUMN IF EXISTS parent_user_id;
