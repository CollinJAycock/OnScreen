-- name: CreateInviteToken :one
INSERT INTO invite_tokens (created_by, token_hash, email, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: GetInviteToken :one
SELECT id, created_by, email
FROM invite_tokens
WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW();

-- name: MarkInviteTokenUsed :exec
UPDATE invite_tokens SET used_at = NOW(), used_by = $2 WHERE id = $1;

-- name: ListInviteTokens :many
SELECT id, created_by, email, expires_at, used_at, created_at
FROM invite_tokens
ORDER BY created_at DESC;

-- name: DeleteInviteToken :exec
DELETE FROM invite_tokens WHERE id = $1;
