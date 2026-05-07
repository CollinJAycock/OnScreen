-- +goose Up
-- Round 2 of the FK-cascade-index sweep started in 00081. The audit
-- after that fix walked every REFERENCES clause and found these
-- additional offenders — all FKs whose referencing column had no
-- index, making cascade DELETE/UPDATE a sequential scan of each
-- child table per parent row touched.
--
-- Same shape as 00081: partial indexes WHERE col IS NOT NULL for
-- nullable FKs, plain CREATE INDEX (Goose wraps in tx, CONCURRENTLY
-- can't run inside one — fine pre-launch since the tables are still
-- small).
--
-- Severity per audit:
--   P0  — paths that already burn time today (user delete cascade,
--         nightly EPG trim).
--   P1  — paths that degrade as the table grows.
--   P2  — admin-scoped tables, low volume but still seq-scan.

-- P0: user delete cascade. media_requests is going to be the biggest
-- per-user table once requests are real.
CREATE INDEX IF NOT EXISTS idx_media_requests_user_id
    ON media_requests(user_id);

-- P0: EPG nightly trim runs DELETE FROM epg_programs WHERE ends_at <
-- NOW() - INTERVAL '1 day'. Both these FKs are SET NULL — every trim
-- seq-scans both child tables to clear them. The trim runs on the
-- DVR critical path so latency spikes the recording UI.
CREATE INDEX IF NOT EXISTS idx_recordings_program_id
    ON recordings(program_id)
    WHERE program_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_schedules_program_id
    ON schedules(program_id)
    WHERE program_id IS NOT NULL;

-- P1: more user-delete cascades.
CREATE INDEX IF NOT EXISTS idx_audit_log_user_id
    ON audit_log(user_id)
    WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_invite_tokens_created_by
    ON invite_tokens(created_by);
CREATE INDEX IF NOT EXISTS idx_invite_tokens_used_by
    ON invite_tokens(used_by)
    WHERE used_by IS NOT NULL;

-- P1: media_files delete cascade. trickplay_status is one row per
-- media_file via SET NULL — without this, file delete seq-scans the
-- whole trickplay_status table.
CREATE INDEX IF NOT EXISTS idx_trickplay_status_file_id
    ON trickplay_status(file_id)
    WHERE file_id IS NOT NULL;

-- P2: arr-services + admin-decided cascades on media_requests.
-- Tables are small but consistency matters.
CREATE INDEX IF NOT EXISTS idx_media_requests_requested_service_id
    ON media_requests(requested_service_id)
    WHERE requested_service_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_media_requests_service_id
    ON media_requests(service_id)
    WHERE service_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_media_requests_decided_by
    ON media_requests(decided_by)
    WHERE decided_by IS NOT NULL;

-- (NOTE: the audit also flagged idx_channels_tuner as needing
-- promotion from its partial-on-enabled form to a full index. On
-- closer look the existing UNIQUE(tuner_id, number) constraint on
-- channels already provides a (tuner_id, …) leading-column index
-- that the cascade DELETE on tuner_devices can use. Leaving the
-- partial in place — it covers the live-channel-list query without
-- duplicating the cascade index.)

-- Add a unique partial index on scheduled_tasks(task_type) WHERE
-- enabled — backs the EnsureSystemTask upsert pattern. Today
-- task_type uniqueness is enforced only by application code, so a
-- racy concurrent EnsureSystemTask call could create two enabled
-- rows of the same type.
CREATE UNIQUE INDEX IF NOT EXISTS uq_scheduled_tasks_enabled_type
    ON scheduled_tasks(task_type)
    WHERE enabled;

-- +goose Down
DROP INDEX IF EXISTS uq_scheduled_tasks_enabled_type;
DROP INDEX IF EXISTS idx_media_requests_decided_by;
DROP INDEX IF EXISTS idx_media_requests_service_id;
DROP INDEX IF EXISTS idx_media_requests_requested_service_id;
DROP INDEX IF EXISTS idx_trickplay_status_file_id;
DROP INDEX IF EXISTS idx_invite_tokens_used_by;
DROP INDEX IF EXISTS idx_invite_tokens_created_by;
DROP INDEX IF EXISTS idx_audit_log_user_id;
DROP INDEX IF EXISTS idx_schedules_program_id;
DROP INDEX IF EXISTS idx_recordings_program_id;
DROP INDEX IF EXISTS idx_media_requests_user_id;
