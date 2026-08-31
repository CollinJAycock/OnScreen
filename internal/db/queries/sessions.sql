-- name: CreateSession :one
-- absolute_expires_at anchors a hard ceiling to creation time. Rotation slides
-- expires_at forward on every refresh, so without this a continuously-refreshed
-- chain never ends.
INSERT INTO sessions (user_id, token_hash, client_id, client_name, device_id, platform, ip_addr, expires_at, absolute_expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW() + interval '90 days')
RETURNING id, user_id, token_hash, client_id, client_name, device_id, platform,
          ip_addr, created_at, expires_at, last_seen, prev_token_hash,
          absolute_expires_at;

-- name: GetSessionByTokenHash :one
SELECT id, user_id, token_hash, client_id, client_name, device_id, platform,
       ip_addr, created_at, expires_at, last_seen, prev_token_hash,
       absolute_expires_at
FROM sessions
WHERE token_hash = $1
  AND expires_at > NOW()
  AND (absolute_expires_at IS NULL OR absolute_expires_at > NOW());

-- name: GetSessionByAnyTokenHash :one
-- Resolves a refresh token to its session by EITHER the current hash or the
-- immediately-previous one, returning which matched.
--
-- This is what makes reuse detection reachable. Rotation rewrites token_hash in
-- place, so a superseded token used to match nothing and die as a plain
-- "session not found" — indistinguishable from garbage, and identical to what a
-- thief's victim sees minutes after the theft. Resolving it here lets the caller
-- take the theft branch (wipe the family, bump the epoch, log it) instead of
-- returning a bare 401 and letting the attacker's chain live on.
--
-- The absolute cap is deliberately NOT applied here: an expired-by-ceiling
-- session presenting a superseded hash is still a reuse signal worth acting on.
SELECT id, user_id, token_hash, client_id, client_name, device_id, platform,
       ip_addr, created_at, expires_at, last_seen,
       (token_hash = sqlc.arg('token_hash'))::boolean AS is_current
FROM sessions
WHERE (token_hash = sqlc.arg('token_hash') OR prev_token_hash = sqlc.arg('token_hash'))
  AND expires_at > NOW();

-- name: TouchSession :exec
UPDATE sessions SET last_seen = NOW() WHERE id = $1;

-- name: RotateSession :one
UPDATE sessions
SET token_hash = $2,
    expires_at = $3,
    last_seen  = NOW()
WHERE id = $1
RETURNING id, user_id, token_hash, client_id, client_name, device_id, platform,
          ip_addr, created_at, expires_at, last_seen, prev_token_hash,
          absolute_expires_at;

-- name: RotateSessionConditional :execrows
-- Compare-and-swap rotation: only rewrites token_hash when the row's
-- current token_hash matches `expected_token_hash`. Used by the
-- refresh-token reuse-detection path — if the previous hash doesn't
-- match, somebody else has already rotated the token (i.e. a thief
-- used it before the legitimate client could), and the row count is 0.
-- The caller then invalidates the entire session family for the user.
-- prev_token_hash records the hash being retired so a later presentation of it
-- is recognisable as reuse rather than as an unknown token.
UPDATE sessions
SET token_hash      = $2,
    prev_token_hash = token_hash,
    expires_at      = LEAST($3, COALESCE(absolute_expires_at, $3)),
    last_seen       = NOW()
WHERE id = $1 AND token_hash = $4;

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
