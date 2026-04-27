-- +goose Up
-- Per-profile library-access inheritance flag. v2.1 Track G item 3.
--
-- Default true on every row — for top-level users (parent_user_id IS
-- NULL) the column is irrelevant (their lookup is always their own
-- grants, with no parent to resolve to), so the default is a no-op
-- there. For managed profiles, default-true reverses the v2.0 UX
-- footgun where a freshly-created kid profile saw nothing on every
-- private library because no admin had thought to copy the grants
-- across.
--
-- When true: library access checks resolve to the *parent's* user_id,
-- so the profile sees exactly what the parent sees on private
-- libraries — useful as a safe default since admins generally create
-- profiles for their own household.
--
-- When false: the profile uses its own library_access rows — admins
-- can narrow per-profile (kid sees Family Movies only, even though
-- the parent has access to 4K Movies too). Couples to the
-- parental-rating-ceiling work that shipped in v2.0.

ALTER TABLE users ADD COLUMN inherit_library_access BOOLEAN NOT NULL DEFAULT true;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS inherit_library_access;
