-- Automatic trickplay generation: the work-queue reads behind the post-scan
-- enqueue, the nightly trickplay_backfill task and the per-library admin
-- endpoints. "Eligible" everywhere below means a live video leaf item
-- (movie / episode / home video) with at least one active, non-damaged video
-- file that has a known duration — the same things the generator needs to
-- produce sprites. Items the generator can't use never enter the queue, so
-- they can't retry-loop.

-- name: ListTrickplayCandidates :many
-- Eligible items that still need sprites, oldest first. "Need" means no
-- trickplay_status row at all, or a 'pending' row whose run was orphaned (the
-- process died mid-generation; a live run finishes long before 6 hours).
-- 'failed' rows are only returned when include_failed is set — the automatic
-- paths never pass it, so a file ffmpeg can't read is tried once, not every
-- night. library_id NULL spans every library; only_enabled restricts to
-- libraries with trickplay_enabled (the automatic paths) while the admin
-- "Generate now" endpoint passes false to honour an explicit request.
SELECT mi.id
FROM media_items mi
JOIN libraries l ON l.id = mi.library_id
LEFT JOIN trickplay_status ts ON ts.item_id = mi.id
WHERE mi.deleted_at IS NULL
  AND l.deleted_at IS NULL
  AND mi.type IN ('movie', 'episode', 'home_video')
  AND (sqlc.narg('library_id')::uuid IS NULL OR mi.library_id = sqlc.narg('library_id')::uuid)
  AND (NOT sqlc.arg('only_enabled')::bool OR l.trickplay_enabled)
  AND EXISTS (
      SELECT 1 FROM media_files mf
      WHERE mf.media_item_id = mi.id
        AND mf.status = 'active'
        AND mf.video_codec IS NOT NULL
        AND mf.duration_ms > 0
        AND mf.integrity_status <> 'damaged'
  )
  AND (
      ts.item_id IS NULL
      OR (ts.status = 'pending'
          AND (ts.last_attempted_at IS NULL OR ts.last_attempted_at < NOW() - INTERVAL '6 hours'))
      OR (sqlc.arg('include_failed')::bool AND ts.status = 'failed')
  )
ORDER BY mi.created_at, mi.id
LIMIT sqlc.arg('lim')::int;

-- name: CountTrickplayCandidates :one
-- Same filter as ListTrickplayCandidates (without failed retries); backs the
-- backfill task's "remaining" progress figure.
SELECT COUNT(*)::bigint
FROM media_items mi
JOIN libraries l ON l.id = mi.library_id
LEFT JOIN trickplay_status ts ON ts.item_id = mi.id
WHERE mi.deleted_at IS NULL
  AND l.deleted_at IS NULL
  AND mi.type IN ('movie', 'episode', 'home_video')
  AND (sqlc.narg('library_id')::uuid IS NULL OR mi.library_id = sqlc.narg('library_id')::uuid)
  AND (NOT sqlc.arg('only_enabled')::bool OR l.trickplay_enabled)
  AND EXISTS (
      SELECT 1 FROM media_files mf
      WHERE mf.media_item_id = mi.id
        AND mf.status = 'active'
        AND mf.video_codec IS NOT NULL
        AND mf.duration_ms > 0
        AND mf.integrity_status <> 'damaged'
  )
  AND (
      ts.item_id IS NULL
      OR (ts.status = 'pending'
          AND (ts.last_attempted_at IS NULL OR ts.last_attempted_at < NOW() - INTERVAL '6 hours'))
  );

-- name: GetLibraryTrickplayCounts :one
-- Progress counts for the library settings page, over the eligible items of
-- one library. 'skipped' (no usable file at generation time) is reported with
-- 'failed' — both mean "won't get thumbnails without intervention". pending
-- is derived by the caller as total - done - failed, so it covers both
-- not-yet-started and in-progress items.
SELECT COUNT(*)::bigint                                                    AS total,
       COUNT(*) FILTER (WHERE ts.status = 'done')::bigint                  AS done,
       COUNT(*) FILTER (WHERE ts.status IN ('failed', 'skipped'))::bigint  AS failed
FROM media_items mi
LEFT JOIN trickplay_status ts ON ts.item_id = mi.id
WHERE mi.library_id = $1
  AND mi.deleted_at IS NULL
  AND mi.type IN ('movie', 'episode', 'home_video')
  AND EXISTS (
      SELECT 1 FROM media_files mf
      WHERE mf.media_item_id = mi.id
        AND mf.status = 'active'
        AND mf.video_codec IS NOT NULL
        AND mf.duration_ms > 0
        AND mf.integrity_status <> 'damaged'
  );

-- name: MarkTrickplaySkipped :exec
-- Records that an item can't get sprites (no playable file / no duration at
-- generation time) so the automatic paths stop re-listing it. A later
-- per-item admin regenerate overwrites the row via UpsertTrickplayPending.
INSERT INTO trickplay_status (item_id, status, last_attempted_at, last_error)
VALUES ($1, 'skipped', NOW(), sqlc.arg('reason')::text)
ON CONFLICT (item_id) DO UPDATE SET
    status            = 'skipped',
    last_attempted_at = NOW(),
    last_error        = EXCLUDED.last_error;

-- name: ResetInterruptedTrickplay :exec
-- Clears a 'pending' row left by a generation that was cancelled (server
-- shutdown killed ffmpeg before the failure could be recorded), returning the
-- item to "not started" so the next scan or backfill picks it up again.
DELETE FROM trickplay_status
WHERE item_id = $1 AND status = 'pending';
