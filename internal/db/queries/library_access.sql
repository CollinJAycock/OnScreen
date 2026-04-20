-- name: ListLibraryAccessByUser :many
-- Returns library IDs the user has been granted access to, for building the
-- Users-tab toggle state. Does not filter by is_admin — callers should skip
-- the check entirely for admins (they bypass the ACL).
SELECT library_id
FROM library_access
WHERE user_id = $1;

-- name: HasLibraryAccess :one
-- Cheap single-row existence check used by handlers that take a library_id
-- or item_id. Callers should skip for admins.
SELECT EXISTS(
    SELECT 1 FROM library_access
    WHERE user_id = $1 AND library_id = $2
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
-- Returns only library IDs the user can see, joined against libraries so
-- soft-deleted rows are excluded. Used by list endpoints that filter.
SELECT la.library_id
FROM library_access la
JOIN libraries l ON l.id = la.library_id
WHERE la.user_id = $1 AND l.deleted_at IS NULL;
