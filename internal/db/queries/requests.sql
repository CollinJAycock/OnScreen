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

-- name: CountRecentMediaRequestsForUser :one
-- Request-quota usage: how many requests of one type the user created after
-- @since (now minus the quota window). Declined requests don't count, and
-- neither do cancelled ones — CancelMediaRequest stores a user's own cancel as
-- 'declined'. Served by media_requests_user_created (user_id, created_at).
SELECT COUNT(*)
FROM media_requests
WHERE user_id = @user_id
  AND type = @type
  AND created_at > @since
  AND status <> 'declined';

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
-- Used by the arr webhook to find approved/downloading requests for a given
-- TMDB title so they can be marked fulfilled when the file lands. Failed ones
-- are included: a file landing (a manual import, another user's request, a
-- later grab) fulfils a request whose download had failed.
SELECT * FROM media_requests
WHERE type = $1 AND tmdb_id = $2
  AND status IN ('approved', 'downloading', 'failed');

-- name: FindActiveRequestForUser :one
-- Used by Discover to surface "you already requested this" without scanning
-- the full request history.
SELECT * FROM media_requests
WHERE user_id = $1 AND type = $2 AND tmdb_id = $3
  AND status IN ('pending', 'approved', 'downloading')
LIMIT 1;

-- name: FindActiveRequestsForUserByTMDB :many
-- Batch form of FindActiveRequestForUser for the Discover grid: one query for
-- all result rows instead of one per row (the N+1). The caller maps the returned
-- rows by (type, tmdb_id). tmdb_id can collide across types (a movie and a show
-- with the same id), so the type is part of the caller-side key.
SELECT * FROM media_requests
WHERE user_id = $1
  AND tmdb_id = ANY(@tmdb_ids::int[])
  AND status IN ('pending', 'approved', 'downloading');

-- name: ApproveMediaRequest :one
-- auto_approved is set in the same UPDATE as the status flip, so no reader
-- sees an approved row with decided_by NULL and the flag not yet set: an
-- automatic approval passes decided_by NULL and auto_approved true, an admin
-- approval the admin's id and false.
UPDATE media_requests
SET status      = 'approved',
    service_id  = $2,
    quality_profile_id = COALESCE($3, quality_profile_id),
    root_folder        = COALESCE($4, root_folder),
    decided_by  = $5,
    auto_approved = $6,
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
-- Also clears the live download status (migration 00025): an available
-- request has nothing left in flight. Accepts failed rows for the same reason
-- ListActiveMediaRequestsForTMDB returns them.
UPDATE media_requests
SET status              = 'available',
    fulfilled_item_id   = $2,
    fulfilled_at        = NOW(),
    updated_at          = NOW(),
    download_state      = NULL,
    download_progress   = NULL,
    download_eta        = NULL,
    download_size_bytes = NULL,
    download_message    = NULL,
    download_updated_at = NULL
WHERE id = $1 AND status IN ('approved', 'downloading', 'failed');

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
