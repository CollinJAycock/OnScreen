-- Converted downloads (migration 00051): per-user download_jobs pointing at
-- shared download_conversions, one live encode per (file, quality).

-- name: GetOrCreateDownloadConversion :one
-- Returns the live (queued / running / ready) encode of this file at this
-- quality, creating a queued one when there is none. A reused ready file is
-- touched so the cache keeps it: last_used_at moves to now and its expiry
-- moves out to at least now + ttl.
INSERT INTO download_conversions (media_item_id, media_file_id, quality)
VALUES (@media_item_id, @media_file_id, @quality)
ON CONFLICT (media_file_id, quality)
    WHERE status IN ('queued', 'running', 'ready')
DO UPDATE SET
    last_used_at = NOW(),
    expires_at   = CASE
        WHEN download_conversions.status = 'ready'
            THEN GREATEST(download_conversions.expires_at, NOW() + (sqlc.arg(ttl_seconds)::bigint * INTERVAL '1 second'))
        ELSE download_conversions.expires_at
    END
RETURNING *;

-- name: GetDownloadConversion :one
SELECT * FROM download_conversions WHERE id = $1;

-- name: ClaimQueuedDownloadConversion :one
-- Moves the oldest queued encode to running for this runner. SKIP LOCKED so
-- two servers sharing the database never claim the same row.
UPDATE download_conversions
SET status = 'running', started_at = NOW(), updated_at = NOW(),
    progress = 0, error = NULL
WHERE id = (
    SELECT id FROM download_conversions
    WHERE status = 'queued'
    ORDER BY created_at
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: UpdateDownloadConversionProgress :execrows
-- Heartbeat + progress for a running encode. Zero rows means the encode was
-- cancelled (its row deleted or no longer running) and the runner stops it.
UPDATE download_conversions
SET progress = @progress, updated_at = NOW()
WHERE id = @id AND status = 'running';

-- name: MarkDownloadConversionReady :execrows
UPDATE download_conversions
SET status = 'ready', progress = 1, size_bytes = @size_bytes,
    completed_at = NOW(), updated_at = NOW(), last_used_at = NOW(),
    expires_at = NOW() + (sqlc.arg(ttl_seconds)::bigint * INTERVAL '1 second'), error = NULL
WHERE id = @id AND status = 'running';

-- name: MarkDownloadConversionFailed :exec
UPDATE download_conversions
SET status = 'failed', error = @error, completed_at = NOW(), updated_at = NOW()
WHERE id = @id AND status IN ('queued', 'running');

-- name: RequeueStaleDownloadConversions :execrows
-- A running encode whose heartbeat stopped (the server running it died) goes
-- back to the queue.
UPDATE download_conversions
SET status = 'queued', progress = 0, updated_at = NOW()
WHERE status = 'running' AND updated_at < @stale_before;

-- name: TouchDownloadConversion :exec
-- A ready file was fetched: keep it in the cache a while longer.
UPDATE download_conversions
SET last_used_at = NOW(),
    expires_at = GREATEST(expires_at, NOW() + (sqlc.arg(ttl_seconds)::bigint * INTERVAL '1 second'))
WHERE id = @id AND status = 'ready';

-- name: DeleteUnreferencedPendingConversion :execrows
-- Cancels a queued or running encode nobody is waiting for any more (the
-- last job pointing at it was deleted). A ready file stays for reuse.
DELETE FROM download_conversions dc
WHERE dc.id = @id
  AND dc.status IN ('queued', 'running')
  AND NOT EXISTS (SELECT 1 FROM download_jobs j WHERE j.conversion_id = dc.id);

-- name: ListExpiredDownloadConversions :many
-- Ready files past their expiry: the cleanup task evicts them.
SELECT * FROM download_conversions
WHERE status = 'ready' AND expires_at < NOW()
ORDER BY expires_at;

-- name: ListReadyDownloadConversionsLRU :many
-- Every ready file, least recently used first: the cleanup task walks this
-- to bring the cache back under its size limit.
SELECT * FROM download_conversions
WHERE status = 'ready'
ORDER BY last_used_at, completed_at;

-- name: ListLiveDownloadConversionIDs :many
-- Ids whose files belong in the cache (queued, running or ready); anything
-- else in the cache directory is an orphan.
SELECT id FROM download_conversions
WHERE status IN ('queued', 'running', 'ready');

-- name: MarkDownloadConversionExpired :exec
UPDATE download_conversions
SET status = 'expired', updated_at = NOW()
WHERE id = @id AND status = 'ready';

-- name: PurgeOldDownloadConversions :execrows
-- Forgets failed and expired encodes after the retention window. Jobs that
-- pointed at them keep reading as expired (conversion_id goes NULL).
DELETE FROM download_conversions
WHERE status IN ('failed', 'expired') AND updated_at < @before;

-- name: PurgeOldDownloadJobs :execrows
-- Drops jobs whose file is gone and that nobody has looked at in the
-- retention window.
DELETE FROM download_jobs j
WHERE j.created_at < @before
  AND NOT EXISTS (
      SELECT 1 FROM download_conversions dc
      WHERE dc.id = j.conversion_id AND dc.status IN ('queued', 'running', 'ready')
  );

-- name: CountActiveDownloadJobsForUser :one
-- The user's jobs still waiting on an encode (queued or running): capped so
-- one account can't fill the queue.
SELECT COUNT(*) FROM download_jobs j
JOIN download_conversions dc ON dc.id = j.conversion_id
WHERE j.user_id = $1 AND dc.status IN ('queued', 'running');

-- name: GetUserDownloadJobFor :one
-- The user's most recent job for this file at this quality, if any.
SELECT * FROM download_jobs
WHERE user_id = @user_id AND media_file_id = @media_file_id AND quality = @quality
ORDER BY created_at DESC
LIMIT 1;

-- name: CreateDownloadJob :one
INSERT INTO download_jobs (user_id, media_item_id, media_file_id, quality, conversion_id)
VALUES (@user_id, @media_item_id, @media_file_id, @quality, @conversion_id)
RETURNING *;

-- name: RepointDownloadJob :one
-- An earlier job for the same file and quality whose encode failed or
-- expired picks up the new encode instead of leaving a second row.
UPDATE download_jobs
SET conversion_id = @conversion_id, created_at = NOW()
WHERE id = @id
RETURNING *;

-- name: DeleteDownloadJob :one
-- Deletes one of the user's jobs, returning it (its conversion id tells the
-- caller what may now be cancellable).
DELETE FROM download_jobs
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: GetDownloadJobView :one
-- One job with its encode's state and the item's title.
SELECT j.id, j.user_id, j.media_item_id, j.media_file_id, j.quality,
       j.conversion_id, j.created_at,
       COALESCE(dc.status, 'expired')::text AS status,
       COALESCE(dc.progress, 0)::real AS progress,
       dc.error, dc.size_bytes, dc.expires_at, dc.completed_at,
       mi.title AS item_title, mi.type AS item_type
FROM download_jobs j
LEFT JOIN download_conversions dc ON dc.id = j.conversion_id
JOIN media_items mi ON mi.id = j.media_item_id
WHERE j.id = $1;

-- name: ListDownloadJobViewsForUser :many
SELECT j.id, j.user_id, j.media_item_id, j.media_file_id, j.quality,
       j.conversion_id, j.created_at,
       COALESCE(dc.status, 'expired')::text AS status,
       COALESCE(dc.progress, 0)::real AS progress,
       dc.error, dc.size_bytes, dc.expires_at, dc.completed_at,
       mi.title AS item_title, mi.type AS item_type
FROM download_jobs j
LEFT JOIN download_conversions dc ON dc.id = j.conversion_id
JOIN media_items mi ON mi.id = j.media_item_id
WHERE j.user_id = $1
ORDER BY j.created_at DESC
LIMIT 100;
