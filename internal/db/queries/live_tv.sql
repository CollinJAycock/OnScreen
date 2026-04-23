-- ── tuner_devices ─────────────────────────────────────────────────────────────

-- name: CreateTunerDevice :one
-- Inserts a tuner with its connection config blob. tune_count is filled in
-- by discovery for HDHomeRun (e.g. 4 or 5); M3U sources should pass a large
-- number to mean "effectively unlimited."
INSERT INTO tuner_devices (type, name, config, tune_count)
VALUES ($1, $2, $3, $4)
RETURNING id, type, name, config, tune_count, enabled, last_seen_at,
          created_at, updated_at;

-- name: GetTunerDevice :one
SELECT id, type, name, config, tune_count, enabled, last_seen_at,
       created_at, updated_at
FROM tuner_devices
WHERE id = $1;

-- name: ListTunerDevices :many
-- All tuners regardless of enabled state — settings UI needs to show
-- disabled ones so they can be re-enabled. Live-TV runtime should filter
-- on `enabled = TRUE` itself.
SELECT id, type, name, config, tune_count, enabled, last_seen_at,
       created_at, updated_at
FROM tuner_devices
ORDER BY name;

-- name: UpdateTunerDevice :one
-- Settings-driven edits (rename, swap config, change tune_count). enabled
-- is a separate query to keep "device is reachable" probes from
-- accidentally re-enabling a device the user disabled.
UPDATE tuner_devices
SET name = $2, config = $3, tune_count = $4, updated_at = NOW()
WHERE id = $1
RETURNING id, type, name, config, tune_count, enabled, last_seen_at,
          created_at, updated_at;

-- name: SetTunerEnabled :exec
UPDATE tuner_devices
SET enabled = $2, updated_at = NOW()
WHERE id = $1;

-- name: TouchTunerLastSeen :exec
-- Discovery / health-check pings call this when a tuner responds.
UPDATE tuner_devices
SET last_seen_at = NOW()
WHERE id = $1;

-- name: DeleteTunerDevice :exec
-- ON DELETE CASCADE wipes the channels, which cascades to epg_programs.
DELETE FROM tuner_devices WHERE id = $1;

-- ── channels ─────────────────────────────────────────────────────────────────

-- name: UpsertChannel :one
-- Discovery upserts every channel by (tuner_id, number) so re-running
-- discovery is safe and just refreshes name/callsign/logo. enabled and
-- sort_order are preserved on conflict so user customizations survive.
INSERT INTO channels (tuner_id, number, callsign, name, logo_url)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tuner_id, number) DO UPDATE SET
    callsign = EXCLUDED.callsign,
    name = EXCLUDED.name,
    logo_url = EXCLUDED.logo_url,
    updated_at = NOW()
RETURNING id, tuner_id, number, callsign, name, logo_url, enabled,
          sort_order, created_at, updated_at;

-- name: GetChannel :one
SELECT id, tuner_id, number, callsign, name, logo_url, enabled,
       sort_order, created_at, updated_at
FROM channels
WHERE id = $1;

-- name: ListChannels :many
-- All channels across all enabled tuners, with an optional enabled filter
-- (NULL = include both). Settings UI passes NULL; the public /tv channels
-- page passes TRUE.
SELECT c.id, c.tuner_id, c.number, c.callsign, c.name, c.logo_url,
       c.enabled, c.sort_order, c.created_at, c.updated_at,
       t.name AS tuner_name, t.type AS tuner_type
FROM channels c
JOIN tuner_devices t ON t.id = c.tuner_id
WHERE t.enabled = TRUE
  AND (sqlc.narg('enabled')::boolean IS NULL OR c.enabled = sqlc.narg('enabled'))
ORDER BY c.sort_order, c.number;

-- name: ListChannelsByTuner :many
SELECT id, tuner_id, number, callsign, name, logo_url, enabled,
       sort_order, created_at, updated_at
FROM channels
WHERE tuner_id = $1
ORDER BY sort_order, number;

-- name: SetChannelEnabled :exec
UPDATE channels SET enabled = $2, updated_at = NOW() WHERE id = $1;

-- name: SetChannelSortOrder :exec
UPDATE channels SET sort_order = $2, updated_at = NOW() WHERE id = $1;

-- name: DeleteChannel :exec
DELETE FROM channels WHERE id = $1;

-- ── epg_sources ───────────────────────────────────────────────────────────────

-- name: CreateEPGSource :one
INSERT INTO epg_sources (type, name, config, refresh_interval_min)
VALUES ($1, $2, $3, $4)
RETURNING id, type, name, config, refresh_interval_min, enabled,
          last_pull_at, last_error, created_at, updated_at;

-- name: GetEPGSource :one
SELECT id, type, name, config, refresh_interval_min, enabled,
       last_pull_at, last_error, created_at, updated_at
FROM epg_sources
WHERE id = $1;

-- name: ListEPGSources :many
SELECT id, type, name, config, refresh_interval_min, enabled,
       last_pull_at, last_error, created_at, updated_at
FROM epg_sources
ORDER BY name;

-- name: UpdateEPGSource :exec
UPDATE epg_sources
SET name = $2, config = $3, refresh_interval_min = $4, updated_at = NOW()
WHERE id = $1;

-- name: SetEPGSourceEnabled :exec
UPDATE epg_sources SET enabled = $2, updated_at = NOW() WHERE id = $1;

-- name: RecordEPGPull :exec
-- Called after a successful or failed refresh. Pass NULL for last_error
-- on success so a previously-recorded error clears.
UPDATE epg_sources
SET last_pull_at = NOW(), last_error = $2, updated_at = NOW()
WHERE id = $1;

-- name: DeleteEPGSource :exec
DELETE FROM epg_sources WHERE id = $1;

-- ── epg_programs ──────────────────────────────────────────────────────────────

-- name: UpsertEPGProgram :exec
-- Idempotent on (channel_id, source_program_id) so refreshing the grid
-- doesn't duplicate rows. raw_data carries source-specific fields we
-- haven't promoted to columns yet.
INSERT INTO epg_programs (
    channel_id, source_program_id, title, subtitle, description,
    category, rating, season_num, episode_num, original_air_date,
    starts_at, ends_at, raw_data
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13
)
ON CONFLICT (channel_id, source_program_id) DO UPDATE SET
    title = EXCLUDED.title,
    subtitle = EXCLUDED.subtitle,
    description = EXCLUDED.description,
    category = EXCLUDED.category,
    rating = EXCLUDED.rating,
    season_num = EXCLUDED.season_num,
    episode_num = EXCLUDED.episode_num,
    original_air_date = EXCLUDED.original_air_date,
    starts_at = EXCLUDED.starts_at,
    ends_at = EXCLUDED.ends_at,
    raw_data = EXCLUDED.raw_data;

-- name: GetEPGProgram :one
SELECT id, channel_id, source_program_id, title, subtitle, description,
       category, rating, season_num, episode_num, original_air_date,
       starts_at, ends_at, raw_data, created_at
FROM epg_programs
WHERE id = $1;

-- name: ListEPGProgramsByChannel :many
-- Guide-grid query for one channel inside a time window. Used by the
-- channel-detail page and (with channel_id IN (...)) by the guide grid.
SELECT id, channel_id, source_program_id, title, subtitle, description,
       category, rating, season_num, episode_num, original_air_date,
       starts_at, ends_at, raw_data, created_at
FROM epg_programs
WHERE channel_id = $1
  AND ends_at > $2     -- starts after window-start (inclusive of currently airing)
  AND starts_at < $3   -- starts before window-end
ORDER BY starts_at;

-- name: GetNowAndNextForChannels :many
-- Channels page: for each (visible) channel return at most two programs —
-- the one airing at NOW() and the one immediately after. LATERAL keeps the
-- per-channel sub-pull cheap with the (channel_id, starts_at) index.
SELECT
    c.id            AS channel_id,
    c.number,
    c.name          AS channel_name,
    c.logo_url,
    p.id            AS program_id,
    p.title,
    p.subtitle,
    p.starts_at,
    p.ends_at,
    p.season_num,
    p.episode_num
FROM channels c
JOIN tuner_devices t ON t.id = c.tuner_id AND t.enabled = TRUE
LEFT JOIN LATERAL (
    SELECT id, title, subtitle, starts_at, ends_at, season_num, episode_num
    FROM epg_programs
    WHERE channel_id = c.id
      AND ends_at > NOW()
    ORDER BY starts_at
    LIMIT 2
) p ON TRUE
WHERE c.enabled = TRUE
ORDER BY c.sort_order, c.number, p.starts_at;

-- name: TrimOldEPGPrograms :exec
-- Run by the EPG refresh job to keep the table bounded. One day past the
-- current time is enough headroom for "what just aired" UI.
DELETE FROM epg_programs WHERE ends_at < NOW() - INTERVAL '1 day';
