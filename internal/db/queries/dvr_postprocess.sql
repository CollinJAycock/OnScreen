-- ── DVR post-processing queue ────────────────────────────────────────────────

-- name: EnqueueDVRPostProcess :execrows
-- Queues (or re-queues) post-processing for a recording. A job that is
-- already queued or running is left alone, so a double click or a finalize
-- retry can't run the pipeline twice; zero rows affected tells the caller.
-- original_file_path survives a re-queue: it still names the kept capture.
INSERT INTO dvr_postprocess_jobs (recording_id, kind, status, queued_at, updated_at)
VALUES ($1, $2, 'queued', NOW(), NOW())
ON CONFLICT (recording_id) DO UPDATE
SET kind = EXCLUDED.kind,
    status = 'queued',
    message = NULL,
    queued_at = NOW(),
    updated_at = NOW()
WHERE dvr_postprocess_jobs.status NOT IN ('queued', 'processing');

-- name: ClaimNextDVRPostProcess :one
-- Takes the oldest queued job and marks it processing. SKIP LOCKED keeps two
-- servers sharing a database from claiming the same row.
UPDATE dvr_postprocess_jobs
SET status = 'processing', updated_at = NOW()
WHERE recording_id = (
    SELECT q.recording_id FROM dvr_postprocess_jobs q
    WHERE q.status = 'queued'
    ORDER BY q.queued_at
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING recording_id, kind, status, message, original_file_path, queued_at, updated_at;

-- name: GetDVRPostProcessJob :one
SELECT recording_id, kind, status, message, original_file_path, queued_at, updated_at
FROM dvr_postprocess_jobs
WHERE recording_id = $1;

-- name: SetDVRPostProcessMessage :exec
-- Progress note while a job runs ("Remuxing", "Detecting commercials").
UPDATE dvr_postprocess_jobs
SET message = $2, updated_at = NOW()
WHERE recording_id = $1;

-- name: FinishDVRPostProcess :exec
UPDATE dvr_postprocess_jobs
SET status = $2, message = $3, updated_at = NOW()
WHERE recording_id = $1;

-- name: FailInterruptedDVRPostProcess :execrows
-- Startup reconcile: a job left 'processing' belongs to a process that died
-- mid-run. Its temporary output is abandoned and the capture was never
-- touched (the swap is the last step), so failing it is safe.
UPDATE dvr_postprocess_jobs
SET status = 'failed', message = 'interrupted by server restart', updated_at = NOW()
WHERE status = 'processing';

-- name: SetDVRPostProcessOriginal :exec
UPDATE dvr_postprocess_jobs
SET original_file_path = $2, updated_at = NOW()
WHERE recording_id = $1;

-- name: SetRecordingFilePath :exec
UPDATE recordings
SET file_path = $2, updated_at = NOW()
WHERE id = $1;

-- name: RepointMediaFile :execrows
-- Points an existing media_files row at a post-processed copy of its file.
-- The row id is kept, so watch state, markers and the item link stay
-- attached. Codecs and duration come from the processor's own ffprobe so
-- playback decisions are right before the next scan; file_hash is cleared so
-- that scan re-probes the rest.
UPDATE media_files
SET file_path     = sqlc.arg('new_path'),
    file_size     = sqlc.arg('file_size'),
    container     = sqlc.narg('container'),
    video_codec   = COALESCE(sqlc.narg('video_codec'), video_codec),
    audio_codec   = COALESCE(sqlc.narg('audio_codec'), audio_codec),
    duration_ms   = COALESCE(sqlc.narg('duration_ms'), duration_ms),
    file_hash     = NULL,
    status        = 'active',
    missing_since = NULL,
    scanned_at    = NOW()
WHERE id = sqlc.arg('id');

-- ── Commercial markers ──────────────────────────────────────────────────────

-- name: DeleteCommercialMarkersByFile :exec
DELETE FROM commercial_markers WHERE media_file_id = $1;

-- name: InsertCommercialMarker :exec
INSERT INTO commercial_markers (media_file_id, start_ms, end_ms, source)
VALUES ($1, $2, $3, $4);

-- name: ListCommercialMarkersByItem :many
-- Every break on every file of an item; the item detail groups them per file.
SELECT cm.id, cm.media_file_id, cm.start_ms, cm.end_ms, cm.source, cm.created_at
FROM commercial_markers cm
JOIN media_files mf ON mf.id = cm.media_file_id
WHERE mf.media_item_id = $1
ORDER BY cm.media_file_id, cm.start_ms;
