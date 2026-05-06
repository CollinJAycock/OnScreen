-- name: CreatePasswordResetToken :exec
INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3);

-- name: GetPasswordResetToken :one
SELECT id, user_id, token_hash, expires_at, used_at, created_at
FROM password_reset_tokens
WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW();

-- name: MarkPasswordResetTokenUsed :execrows
-- Conditional UPDATE so two concurrent submissions of the same reset
-- token can't both pass the GetPasswordResetToken check and run
-- UpdatePassword last-write-wins. The first request wins (rows=1);
-- the second sees rows=0 and the handler bails before mutating
-- credentials. Run as the FIRST step of reset, before UpdatePassword,
-- so a race between two attackers (or a buggy retry) doesn't leave
-- the password in a half-changed state.
UPDATE password_reset_tokens
   SET used_at = NOW()
 WHERE id = $1 AND used_at IS NULL;

-- name: DeleteExpiredPasswordResetTokens :exec
DELETE FROM password_reset_tokens WHERE expires_at < NOW() OR used_at IS NOT NULL;
