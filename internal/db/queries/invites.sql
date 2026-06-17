-- name: CreateInviteToken :one
INSERT INTO invite_tokens (created_by, token_hash, email, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: GetInviteToken :one
SELECT id, created_by, email
FROM invite_tokens
WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW();

-- name: ClaimInviteToken :execrows
-- Atomically consume a single-use invite BEFORE the account is created: only the
-- first concurrent caller to flip used_at from NULL wins (returns 1 row); a
-- racing/duplicate accept sees 0 and is rejected, so one invite mints at most one
-- account. used_by is backfilled by SetInviteTokenUsedBy after creation; a failed
-- creation releases the claim (ReleaseInviteToken) so a username collision can retry.
UPDATE invite_tokens SET used_at = NOW()
WHERE id = $1 AND used_at IS NULL AND expires_at > NOW();

-- name: ReleaseInviteToken :exec
UPDATE invite_tokens SET used_at = NULL WHERE id = $1;

-- name: SetInviteTokenUsedBy :exec
UPDATE invite_tokens SET used_by = $2 WHERE id = $1;

-- name: ListInviteTokens :many
SELECT id, created_by, email, expires_at, used_at, created_at
FROM invite_tokens
ORDER BY created_at DESC;

-- name: DeleteInviteToken :exec
DELETE FROM invite_tokens WHERE id = $1;
