-- name: ListLibraryAccessByUser :many
-- Returns library IDs the user has been granted access to, for building the
-- Users-tab toggle state. Does not filter by is_admin — callers should skip
-- the check entirely for admins (they bypass the ACL).
SELECT library_id
FROM library_access
WHERE user_id = $1;

-- name: HasLibraryAccess :one
-- Cheap single-row check used by handlers that take a library_id or
-- item_id. Callers should skip for admins.
--
-- v2.1 semantics: a user can access a library when *either* the
-- library is public (is_private = false, the v2.0 default) *or* the
-- user has an explicit grant in library_access. Public-by-default
-- preserves backward compatibility on existing installs where no
-- libraries were marked private.
SELECT EXISTS(
    SELECT 1 FROM libraries l
    LEFT JOIN library_access la
        ON la.library_id = l.id AND la.user_id = sqlc.arg('user_id')
    WHERE l.id = sqlc.arg('library_id')
      AND l.deleted_at IS NULL
      AND (l.is_private = false OR la.library_id IS NOT NULL)
) AS allowed;

-- name: GrantLibraryAccess :exec
-- Idempotent; safe to call repeatedly.
INSERT INTO library_access (user_id, library_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: RevokeLibraryAccess :exec
DELETE FROM library_access
WHERE user_id = $1 AND library_id = $2;

-- name: RevokeAllLibraryAccessForUser :exec
-- Used before ReplaceLibraryAccessForUser to produce a clean set-replace.
DELETE FROM library_access WHERE user_id = $1;

-- name: ListAllowedLibraryIDsForUser :many
-- Returns library IDs the user can see — the v2.1 model is the union
-- of (public libraries) and (libraries the user has been explicitly
-- granted). Public-by-default preserves the v2.0 behaviour where any
-- user with auth could see any library; private libraries require an
-- explicit grant in library_access.
--
-- DISTINCT because a library could match both branches when an admin
-- toggles is_private=false on a library that previously had grants
-- recorded — we don't want duplicate IDs in the caller's set.
SELECT DISTINCT l.id AS library_id
FROM libraries l
LEFT JOIN library_access la
    ON la.library_id = l.id AND la.user_id = $1
WHERE l.deleted_at IS NULL
  AND (l.is_private = false OR la.library_id IS NOT NULL);
