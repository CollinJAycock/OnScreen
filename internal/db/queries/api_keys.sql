-- Personal API keys (migration 00050). Only the SHA-256 of a key is stored.

-- name: CreateAPIKey :one
INSERT INTO api_keys (user_id, name, key_hash, key_prefix, scope, expires_at)
VALUES (@user_id, @name, @key_hash, @key_prefix, @scope, sqlc.narg('expires_at'))
RETURNING id, user_id, name, key_prefix, scope, created_at, last_used_at, expires_at;

-- name: CountAPIKeysForUser :one
SELECT count(*) FROM api_keys WHERE user_id = $1;

-- name: ListAPIKeysForUser :many
-- Expired keys are listed too (marked by expires_at in the past) so the owner
-- can see why a script stopped working and delete the row.
SELECT id, user_id, name, key_prefix, scope, created_at, last_used_at, expires_at
FROM api_keys
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: ListAllAPIKeys :many
-- Admin view: every user's keys, newest first, with the owner's username.
SELECT k.id, k.user_id, u.username, k.name, k.key_prefix, k.scope,
       k.created_at, k.last_used_at, k.expires_at
FROM api_keys k
JOIN users u ON u.id = k.user_id
ORDER BY k.created_at DESC;

-- name: DeleteAPIKeyForUser :one
-- Scoped by owner: another user's key id matches no row (404), so key ids
-- can't be probed across accounts.
DELETE FROM api_keys
WHERE id = @id AND user_id = @user_id
RETURNING id, user_id, name, key_prefix;

-- name: DeleteAPIKey :one
-- Admin revoke of any user's key.
DELETE FROM api_keys
WHERE id = $1
RETURNING id, user_id, name, key_prefix;

-- name: GetAPIKeyAuth :one
-- The per-request lookup behind `Authorization: Bearer osk_…` / X-Api-Key:
-- resolves an unexpired key's hash to the key and its owner's CURRENT policy
-- (admin flag, rating ceiling, label restriction, session epoch) in one
-- indexed probe. Read on the primary pool so a delete is honoured at once.
SELECT k.id, k.user_id, k.scope,
       u.username, u.is_admin, u.max_content_rating,
       u.blocked_labels, u.allowed_labels, u.session_epoch
FROM api_keys k
JOIN users u ON u.id = k.user_id
WHERE k.key_hash = $1
  AND (k.expires_at IS NULL OR k.expires_at > now());

-- name: GetAPIKeyState :one
-- Re-check for a credential minted under an API key (the stream tokens an
-- item fetch hands out, a long-lived SSE stream): the owner's epoch and
-- whether the key still exists and is unexpired. No row = user gone.
SELECT u.session_epoch,
       EXISTS (
           SELECT 1 FROM api_keys k
           WHERE k.id = sqlc.arg('key_id')::uuid
             AND k.user_id = u.id
             AND (k.expires_at IS NULL OR k.expires_at > now())
       )::boolean AS key_live
FROM users u
WHERE u.id = sqlc.arg('user_id')::uuid;

-- name: TouchAPIKeyLastUsed :exec
-- Throttled in SQL as well as in process: with several server instances each
-- holding its own in-memory throttle, the row is still written at most once
-- per five minutes.
UPDATE api_keys
SET last_used_at = now()
WHERE id = $1
  AND (last_used_at IS NULL OR last_used_at < now() - interval '5 minutes');
