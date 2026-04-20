-- +goose Up
-- Per-user library access control. A row grants access; absence denies.
-- Admins bypass this table entirely at the application layer.
--
-- For backward compatibility, this migration seeds every existing non-admin
-- user with access to every existing library so the deploy doesn't silently
-- blank out catalogs. New users and new libraries start with no grants —
-- admins must open the Users tab to grant access.
CREATE TABLE library_access (
    user_id     UUID NOT NULL REFERENCES users(id)     ON DELETE CASCADE,
    library_id  UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    granted_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, library_id)
);

CREATE INDEX idx_library_access_library ON library_access(library_id);

INSERT INTO library_access (user_id, library_id)
SELECT u.id, l.id
FROM users u
CROSS JOIN libraries l
WHERE u.is_admin = false
  AND l.deleted_at IS NULL
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS library_access;
