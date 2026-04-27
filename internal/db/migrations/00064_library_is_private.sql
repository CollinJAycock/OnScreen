-- +goose Up
-- Library-level visibility flag. Two-state model:
--
--   is_private = false  →  every authenticated user can see the library
--                          (the v2.0 default behaviour, preserved on
--                          existing rows by the DEFAULT clause)
--
--   is_private = true   →  only users with an explicit row in
--                          library_access can see it; non-granted
--                          users get the same 404 they'd get for a
--                          non-existent library
--
-- v2.0 had per-user grants but no library-level toggle, so the only
-- way to scope a library was to grant every user explicitly — hostile
-- onboarding, especially for OIDC/SAML auto-provisioned accounts that
-- start with zero grants. Adding the flag lets admins keep a "Movies"
-- library public-by-default while marking, say, "4K Movies" private.
--
-- Default false to preserve current behaviour: an existing install
-- where every user already had implicit access (because nothing was
-- private) keeps that access after migration. Admins flip the flag
-- on libraries they want to gate.

ALTER TABLE libraries ADD COLUMN is_private BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE libraries DROP COLUMN IF EXISTS is_private;
