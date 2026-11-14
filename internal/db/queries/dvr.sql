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

-- name: CountLiveSchedulesForUser :one
-- The per-user schedule cap counts only rules that can still produce a
-- recording: enabled series / channel_block rules, and enabled one-off
-- ('once') rules whose programme has not finished airing. Every guide
-- "Record" click creates a 'once' row and nothing deletes it after it airs
-- (EPG trimming only NULLs program_id, and deleting it would drop the
-- retention its recordings JOIN on), so counting every row locked a user
-- out of scheduling after 100 lifetime one-offs. A NULL program_id or an
-- ended programme makes p.ends_at > NOW() false/NULL, so spent one-offs
-- drop out without any row being touched.
SELECT COUNT(*)
FROM schedules s
LEFT JOIN epg_programs p ON p.id = s.program_id
WHERE s.user_id = $1
  AND s.enabled = TRUE
  AND (s.type <> 'once' OR p.ends_at > NOW());

-- name: ScheduleChannelsEnabled :one
-- Whether every channel a new schedule would record from exists and is
-- enabled: the channel it names, and the channel of the programme a one-off
-- names. A NULL argument isn't checked. Non-admins can't schedule on a
-- disabled channel (they can't tune one live either); an unknown channel or
-- programme counts as not enabled.
SELECT (
    (sqlc.narg('channel_id')::uuid IS NULL
     OR EXISTS (SELECT 1 FROM channels c
                WHERE c.id = sqlc.narg('channel_id')::uuid AND c.enabled))
    AND
    (sqlc.narg('program_id')::uuid IS NULL
     OR EXISTS (SELECT 1 FROM epg_programs p
                JOIN channels c ON c.id = p.channel_id
                WHERE p.id = sqlc.narg('program_id')::uuid AND c.enabled))
)::boolean AS enabled;

-- name: ListEnabledSchedules :many
-- The matcher iterates this every minute. Disabled schedules are
-- ignored (but their existing scheduled recordings continue normally).
-- Hard-capped at 5000 — generous ceiling so a ridiculous fleet of
-- title-match rules can't blow up the matcher's memory each tick.
-- Spent one-offs (programme finished, or trimmed from the EPG) are skipped
-- with the same rule CountLiveSchedulesForUser uses: the matcher has nothing
-- to do for them, and since nothing deletes them they would otherwise pile up
-- until they pushed live rules past the cap and recordings silently stopped.
SELECT id, user_id, type, program_id, channel_id, title_match, new_only,
       time_start, time_end, padding_pre_sec, padding_post_sec,
       priority, retention_days, enabled, created_at, updated_at
FROM schedules
WHERE enabled = TRUE
  AND (type <> 'once'
       OR EXISTS (SELECT 1 FROM epg_programs p
                  WHERE p.id = schedules.program_id AND p.ends_at > NOW()))
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
       c.number AS channel_number, c.name AS channel_name, c.logo_url,
       j.status AS postprocess_status, j.message AS postprocess_message
FROM recordings r
JOIN channels c ON c.id = r.channel_id
LEFT JOIN dvr_postprocess_jobs j ON j.recording_id = r.id
WHERE r.user_id = $1
  AND (sqlc.narg('status')::text IS NULL OR r.status = sqlc.narg('status'))
ORDER BY r.starts_at DESC
LIMIT $2 OFFSET $3;

-- name: ListDueRecordings :many
-- Worker picks up scheduled recordings whose starts_at is within the
-- next window (typically 60s) so ffmpeg can be spun up in time to
-- capture the padding_pre window. Excludes already-running ones.
--
-- Also excludes a recording on a channel an admin has disabled since it was
-- scheduled, unless its owner is an admin: a non-admin can't tune a disabled
-- channel live, so the DVR must not tune it for them either. The matcher stops
-- creating new recordings there (ListEPGProgramsInWindow skips disabled
-- channels) and fails the ones left behind
-- (FailDueRecordingsOnDisabledChannels); this is the gate at capture start.
SELECT id, schedule_id, user_id, channel_id, program_id,
       title, subtitle, season_num, episode_num, status,
       starts_at, ends_at, file_path, item_id, error,
       created_at, updated_at
FROM recordings
WHERE status = 'scheduled'
  AND starts_at <= $1
  AND (EXISTS (SELECT 1 FROM channels c WHERE c.id = recordings.channel_id AND c.enabled)
       OR EXISTS (SELECT 1 FROM users u WHERE u.id = recordings.user_id AND u.is_admin))
ORDER BY starts_at;

-- name: FailDueRecordingsOnDisabledChannels :execrows
-- The matcher's sweep for the recordings ListDueRecordings now holds back: a
-- non-admin's recording whose start time has come while its channel is
-- disabled is marked failed with a reason, so the recordings page says why it
-- wasn't recorded instead of listing it as upcoming forever. A recording still
-- in the future is left scheduled — it records if the channel is re-enabled
-- before it starts.
UPDATE recordings r
SET status = 'failed', error = 'channel disabled', updated_at = NOW()
FROM channels c, users u
WHERE r.status = 'scheduled'
  AND r.starts_at <= NOW()
  AND c.id = r.channel_id AND NOT c.enabled
  AND u.id = r.user_id AND NOT u.is_admin;

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

-- name: SetRecordingStartedFile :execrows
-- Called when the worker starts capturing — records the on-disk path
-- and flips status to 'recording' in one shot.
--
-- Guarded on status: only a 'scheduled' recording may transition to
-- 'recording'. Without the guard, a user cancelling in the window between
-- the worker picking the row up and this write had their cancellation
-- silently overwritten — the row flipped back to 'recording' and the
-- capture ran to completion. Zero rows affected tells the worker it lost
-- that race and must stand its capture down.
UPDATE recordings
SET status = 'recording', file_path = $2, updated_at = NOW()
WHERE id = $1 AND status = 'scheduled';

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
-- them + their media_items rows. original_file_path is the capture kept
-- aside by post-processing (delete-original off), removed alongside.
SELECT r.id, r.schedule_id, r.item_id, r.file_path, j.original_file_path
FROM recordings r
JOIN schedules s ON s.id = r.schedule_id
LEFT JOIN dvr_postprocess_jobs j ON j.recording_id = r.id
WHERE r.status = 'completed'
  AND s.retention_days IS NOT NULL
  AND r.ends_at < NOW() - (s.retention_days || ' days')::interval;
