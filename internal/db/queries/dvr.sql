-- ── schedules ────────────────────────────────────────────────────────────────

-- name: CreateSchedule :one
INSERT INTO schedules (
    user_id, type, program_id, channel_id, title_match, new_only,
    time_start, time_end, padding_pre_sec, padding_post_sec,
    priority, retention_days
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING id, user_id, type, program_id, channel_id, title_match, new_only,
          time_start, time_end, padding_pre_sec, padding_post_sec,
          priority, retention_days, enabled, created_at, updated_at;

-- name: GetSchedule :one
SELECT id, user_id, type, program_id, channel_id, title_match, new_only,
       time_start, time_end, padding_pre_sec, padding_post_sec,
       priority, retention_days, enabled, created_at, updated_at
FROM schedules
WHERE id = $1;

-- name: ListSchedulesForUser :many
-- Hard-capped at 500 — DVR rules per user; nobody legitimately has
-- hundreds of active recording rules and an unbounded list ships a
-- big payload to the schedule UI.
SELECT id, user_id, type, program_id, channel_id, title_match, new_only,
       time_start, time_end, padding_pre_sec, padding_post_sec,
       priority, retention_days, enabled, created_at, updated_at
FROM schedules
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT 500;

-- name: ListEnabledSchedules :many
-- The matcher iterates this every minute. Disabled schedules are
-- ignored (but their existing scheduled recordings continue normally).
-- Hard-capped at 5000 — generous ceiling so a ridiculous fleet of
-- title-match rules can't blow up the matcher's memory each tick.
SELECT id, user_id, type, program_id, channel_id, title_match, new_only,
       time_start, time_end, padding_pre_sec, padding_post_sec,
       priority, retention_days, enabled, created_at, updated_at
FROM schedules
WHERE enabled = TRUE
ORDER BY priority DESC, created_at
LIMIT 5000;

-- name: DeleteSchedule :exec
DELETE FROM schedules WHERE id = $1;

-- name: SetScheduleEnabled :exec
UPDATE schedules SET enabled = $2, updated_at = NOW() WHERE id = $1;

-- ── recordings ───────────────────────────────────────────────────────────────

-- name: UpsertRecording :one
-- Matcher calls this for every program it wants to record. (user_id,
-- program_id) is the unique key — re-running the matcher is safe and
-- idempotent. Existing rows in status='scheduled' get their metadata
-- refreshed; recordings in other statuses are left alone (the matcher
-- never re-arms something it already captured).
INSERT INTO recordings (
    schedule_id, user_id, channel_id, program_id,
    title, subtitle, season_num, episode_num,
    status, starts_at, ends_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'scheduled', $9, $10)
ON CONFLICT (user_id, program_id) DO UPDATE SET
    schedule_id = EXCLUDED.schedule_id,
    title = EXCLUDED.title,
    subtitle = EXCLUDED.subtitle,
    season_num = EXCLUDED.season_num,
    episode_num = EXCLUDED.episode_num,
    starts_at = EXCLUDED.starts_at,
    ends_at = EXCLUDED.ends_at,
    updated_at = NOW()
WHERE recordings.status = 'scheduled'
RETURNING id, schedule_id, user_id, channel_id, program_id,
          title, subtitle, season_num, episode_num, status,
          starts_at, ends_at, file_path, item_id, error, created_at, updated_at;

-- name: GetRecording :one
SELECT id, schedule_id, user_id, channel_id, program_id,
       title, subtitle, season_num, episode_num, status,
       starts_at, ends_at, file_path, item_id, error,
       created_at, updated_at
FROM recordings
WHERE id = $1;

-- name: ListRecordingsForUser :many
-- Recordings page query. Status filter is optional — pass NULL to get
-- everything. Ordered newest-first by start time.
SELECT r.id, r.schedule_id, r.user_id, r.channel_id, r.program_id,
       r.title, r.subtitle, r.season_num, r.episode_num, r.status,
       r.starts_at, r.ends_at, r.file_path, r.item_id, r.error,
       r.created_at, r.updated_at,
       c.number AS channel_number, c.name AS channel_name, c.logo_url
FROM recordings r
JOIN channels c ON c.id = r.channel_id
WHERE r.user_id = $1
  AND (sqlc.narg('status')::text IS NULL OR r.status = sqlc.narg('status'))
ORDER BY r.starts_at DESC
LIMIT $2 OFFSET $3;

-- name: ListDueRecordings :many
-- Worker picks up scheduled recordings whose starts_at is within the
-- next window (typically 60s) so ffmpeg can be spun up in time to
-- capture the padding_pre window. Excludes already-running ones.
SELECT id, schedule_id, user_id, channel_id, program_id,
       title, subtitle, season_num, episode_num, status,
       starts_at, ends_at, file_path, item_id, error,
       created_at, updated_at
FROM recordings
WHERE status = 'scheduled'
  AND starts_at <= $1
ORDER BY starts_at;

-- name: ListActiveRecordings :many
-- Recordings currently being captured. Used by the conflict resolver so
-- it doesn't try to tune beyond the device's tune_count while a
-- recording is in flight.
SELECT id, schedule_id, user_id, channel_id, program_id,
       title, subtitle, season_num, episode_num, status,
       starts_at, ends_at, file_path, item_id, error,
       created_at, updated_at
FROM recordings
WHERE status = 'recording';

-- name: SetRecordingStatus :exec
UPDATE recordings
SET status = $2, updated_at = NOW()
WHERE id = $1;

-- name: SetRecordingStartedFile :exec
-- Called when the worker starts capturing — records the on-disk path
-- and flips status to 'recording' in one shot.
UPDATE recordings
SET status = 'recording', file_path = $2, updated_at = NOW()
WHERE id = $1;

-- name: SetRecordingCompleted :exec
-- Worker finalize path: link media_items row, transition to completed.
UPDATE recordings
SET status = 'completed', item_id = $2, updated_at = NOW()
WHERE id = $1;

-- name: SetRecordingFailed :exec
UPDATE recordings
SET status = 'failed', error = $2, updated_at = NOW()
WHERE id = $1;

-- name: DeleteRecording :exec
-- Hard delete. UI uses this for scheduled/failed rows; completed rows
-- should go through the media-items delete path to remove the on-disk
-- file.
DELETE FROM recordings WHERE id = $1;

-- name: ListExpiredRecordings :many
-- Recordings past their schedule's retention window. Worker deletes
-- them + their media_items rows.
SELECT r.id, r.schedule_id, r.item_id, r.file_path
FROM recordings r
JOIN schedules s ON s.id = r.schedule_id
WHERE r.status = 'completed'
  AND s.retention_days IS NOT NULL
  AND r.ends_at < NOW() - (s.retention_days || ' days')::interval;
