-- +goose Up
-- Per-library "auto-grant on user creation" flag. v2.1 Track G item 2.
--
-- The flag only matters for is_private=true libraries — public ones
-- are visible to every authenticated user without any grant entry, so
-- "auto-grant new users to this public library" is a no-op. The
-- frontend gates the toggle on is_private=true and the backend just
-- enforces the policy.
--
-- Use case: an all-private install (admin marked every library
-- private) where new users would otherwise see nothing on first
-- sign-in. Toggling auto_grant_new_users on the libraries the admin
-- wants new accounts to default-into avoids the "barren home page"
-- problem for OIDC/SAML/LDAP auto-provisioned users.
--
-- Default false — preserves the v2.0/Stage-1 behaviour where private
-- libraries are explicitly grant-only. Admins opt in per library.

ALTER TABLE libraries ADD COLUMN auto_grant_new_users BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE libraries DROP COLUMN IF EXISTS auto_grant_new_users;
