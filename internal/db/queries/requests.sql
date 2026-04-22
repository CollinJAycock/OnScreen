-- name: CreateMediaRequest :one
INSERT INTO media_requests (
    user_id, type, tmdb_id, title, year, poster_url, overview,
    status, seasons,
    requested_service_id, quality_profile_id, root_folder
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetMediaRequest :one
SELECT * FROM media_requests WHERE id = $1;

-- name: ListMediaRequestsForUser :many
SELECT *
FROM media_requests
WHERE user_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountMediaRequestsForUser :one
SELECT COUNT(*)
FROM media_requests
WHERE user_id = $1
  AND (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);

-- name: ListAllMediaRequests :many
SELECT *
FROM media_requests
WHERE (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text)
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountAllMediaRequests :one
SELECT COUNT(*)
FROM media_requests
WHERE (sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status')::text);

-- name: ListActiveMediaRequestsForTMDB :many
-- Used by the arr webhook to find pending/approved/downloading requests for
-- a given TMDB title so they can be marked fulfilled when the file lands.
SELECT * FROM media_requests
WHERE type = $1 AND tmdb_id = $2
  AND status IN ('approved', 'downloading');

-- name: FindActiveRequestForUser :one
-- Used by Discover to surface "you already requested this" without scanning
-- the full request history.
SELECT * FROM media_requests
WHERE user_id = $1 AND type = $2 AND tmdb_id = $3
  AND status IN ('pending', 'approved', 'downloading')
LIMIT 1;

-- name: ApproveMediaRequest :one
UPDATE media_requests
SET status      = 'approved',
    service_id  = $2,
    quality_profile_id = COALESCE($3, quality_profile_id),
    root_folder        = COALESCE($4, root_folder),
    decided_by  = $5,
    decided_at  = NOW(),
    updated_at  = NOW()
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: DeclineMediaRequest :one
UPDATE media_requests
SET status         = 'declined',
    decline_reason = $2,
    decided_by     = $3,
    decided_at     = NOW(),
    updated_at     = NOW()
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: MarkMediaRequestDownloading :exec
UPDATE media_requests
SET status = 'downloading', updated_at = NOW()
WHERE id = $1 AND status = 'approved';

-- name: MarkMediaRequestAvailable :exec
UPDATE media_requests
SET status            = 'available',
    fulfilled_item_id = $2,
    fulfilled_at      = NOW(),
    updated_at        = NOW()
WHERE id = $1 AND status IN ('approved', 'downloading');

-- name: MarkMediaRequestFailed :exec
UPDATE media_requests
SET status = 'failed', updated_at = NOW()
WHERE id = $1 AND status IN ('approved', 'downloading');

-- name: CancelMediaRequest :exec
-- A user may cancel their own request only while it's still pending. Admins
-- can also cancel via DeleteMediaRequest.
UPDATE media_requests
SET status = 'declined', decline_reason = 'cancelled by user', decided_at = NOW(), updated_at = NOW()
WHERE id = $1 AND user_id = $2 AND status = 'pending';

-- name: DeleteMediaRequest :exec
DELETE FROM media_requests WHERE id = $1;
