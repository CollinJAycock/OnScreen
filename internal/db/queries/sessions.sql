-- name: CreateSession :one
INSERT INTO sessions (user_id, token_hash, client_id, client_name, device_id, platform, ip_addr, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, user_id, token_hash, client_id, client_name, device_id, platform,
          ip_addr, created_at, expires_at, last_seen;

-- name: GetSessionByTokenHash :one
SELECT id, user_id, token_hash, client_id, client_name, device_id, platform,
       ip_addr, created_at, expires_at, last_seen
FROM sessions
WHERE token_hash = $1 AND expires_at > NOW();

-- name: TouchSession :exec
UPDATE sessions SET last_seen = NOW() WHERE id = $1;

-- name: RotateSession :one
UPDATE sessions
SET token_hash = $2,
    expires_at = $3,
    last_seen  = NOW()
WHERE id = $1
RETURNING id, user_id, token_hash, client_id, client_name, device_id, platform,
          ip_addr, created_at, expires_at, last_seen;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = $1;

-- name: DeleteSessionsForUser :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= NOW();

-- name: ListUserSessions :many
SELECT id, user_id, token_hash, client_id, client_name, device_id, platform,
       ip_addr, created_at, expires_at, last_seen
FROM sessions
WHERE user_id = $1 AND expires_at > NOW()
ORDER BY last_seen DESC
LIMIT 1000;
