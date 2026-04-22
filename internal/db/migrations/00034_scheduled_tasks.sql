-- +goose Up
-- Scheduled tasks: cron-driven admin operations (backup, rescan, enrichment,
-- future plugin-provided handlers). Each task names a handler registered in
-- internal/scheduler; the handler resolves config JSON into its own params.
--
-- The row itself is the lease: the scheduler tick runs
--   SELECT ... FOR UPDATE SKIP LOCKED
-- against rows whose next_run_at has elapsed, so multiple OnScreen nodes
-- running the scheduler can't double-fire a task. Completed work advances
-- next_run_at by parsing cron_expr at run time — the DB never stores the
-- cron schedule object, only its expression.
CREATE TABLE scheduled_tasks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT NOT NULL,
    task_type     TEXT NOT NULL,                -- handler name, e.g. 'backup_database'
    config        JSONB NOT NULL DEFAULT '{}'::jsonb,
    cron_expr     TEXT NOT NULL,                -- standard 5-field cron
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at   TIMESTAMPTZ,
    next_run_at   TIMESTAMPTZ NOT NULL,
    last_status   TEXT NOT NULL DEFAULT '',     -- '' | 'success' | 'failed'
    last_error    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The scheduler tick filters on (enabled, next_run_at) so an index there
-- keeps the tick cheap even with many disabled or future-dated rows.
CREATE INDEX idx_scheduled_tasks_due ON scheduled_tasks (next_run_at)
    WHERE enabled;

CREATE INDEX idx_scheduled_tasks_type ON scheduled_tasks (task_type);

-- task_runs is the execution history. Capped externally (we keep the last
-- N per task via a scheduled task of our own, eventually) — for now the
-- table grows unbounded and we rely on it being small (one row per tick
-- per task).
CREATE TABLE task_runs (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id    UUID NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at   TIMESTAMPTZ,
    status     TEXT NOT NULL DEFAULT 'running', -- 'running' | 'success' | 'failed'
    output     TEXT NOT NULL DEFAULT '',        -- handler-provided human summary
    error      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_task_runs_task ON task_runs (task_id, started_at DESC);

-- +goose Down
DROP TABLE task_runs;
DROP TABLE scheduled_tasks;
