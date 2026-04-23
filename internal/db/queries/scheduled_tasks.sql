-- name: ListScheduledTasks :many
-- Returns all scheduled tasks, newest first. Used by the admin UI.
SELECT id, name, task_type, config, cron_expr, enabled,
       last_run_at, next_run_at, last_status, last_error,
       created_at, updated_at
FROM scheduled_tasks
ORDER BY created_at DESC;

-- name: GetScheduledTask :one
-- Returns a single task by id.
SELECT id, name, task_type, config, cron_expr, enabled,
       last_run_at, next_run_at, last_status, last_error,
       created_at, updated_at
FROM scheduled_tasks
WHERE id = $1;

-- name: CreateScheduledTask :one
-- Inserts a new task. next_run_at is supplied by the caller after parsing
-- cron_expr; the DB never does cron arithmetic.
INSERT INTO scheduled_tasks (name, task_type, config, cron_expr, enabled, next_run_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, name, task_type, config, cron_expr, enabled,
          last_run_at, next_run_at, last_status, last_error,
          created_at, updated_at;

-- name: EnsureSystemTask :exec
-- Idempotent insert for a system task: creates the row only if no task of
-- that task_type already exists. Called at server startup to guarantee the
-- handlers the server knows how to run (dvr_match, epg_refresh, …) are
-- actually scheduled — without this, a fresh install has every handler
-- registered in memory but no row to trigger it, and features like DVR
-- silently no-op.
--
-- Existing rows are never touched: admins can retune cron_expr, disable a
-- row, or rename it without the seeder fighting back on the next restart.
INSERT INTO scheduled_tasks (name, task_type, cron_expr, enabled, next_run_at)
SELECT $1, $2, $3, $4, $5
WHERE NOT EXISTS (SELECT 1 FROM scheduled_tasks WHERE task_type = $2);

-- name: UpdateScheduledTask :one
-- Full update of mutable fields. Caller recomputes next_run_at from the
-- new cron_expr and passes it in.
UPDATE scheduled_tasks
SET name        = $2,
    task_type   = $3,
    config      = $4,
    cron_expr   = $5,
    enabled     = $6,
    next_run_at = $7,
    updated_at  = NOW()
WHERE id = $1
RETURNING id, name, task_type, config, cron_expr, enabled,
          last_run_at, next_run_at, last_status, last_error,
          created_at, updated_at;

-- name: DeleteScheduledTask :exec
DELETE FROM scheduled_tasks WHERE id = $1;

-- name: LeaseDueScheduledTasks :many
-- Returns enabled tasks whose next_run_at has elapsed, locking them so
-- other scheduler instances skip them. The row stays locked until the
-- transaction commits; the scheduler updates next_run_at inside the same
-- transaction so the lease auto-releases with a future run time set.
SELECT id, name, task_type, config, cron_expr, enabled,
       last_run_at, next_run_at, last_status, last_error,
       created_at, updated_at
FROM scheduled_tasks
WHERE enabled
  AND next_run_at <= NOW()
ORDER BY next_run_at
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: SetScheduledTaskNextRun :exec
-- Called inside the lease transaction to advance next_run_at to the real
-- cron-computed next fire time. Separated from result-recording because a
-- handler can run for minutes — we don't want to hold the lease tx open
-- that long.
UPDATE scheduled_tasks
SET next_run_at = $2,
    updated_at  = NOW()
WHERE id = $1;

-- name: RecordScheduledTaskResult :exec
-- Called after the handler returns to record success/failure. next_run_at
-- is NOT touched here — it was set at lease time.
UPDATE scheduled_tasks
SET last_run_at = NOW(),
    last_status = $2,
    last_error  = $3,
    updated_at  = NOW()
WHERE id = $1;

-- name: CreateTaskRun :one
-- Inserts a 'running' row at the start of an execution; the scheduler
-- updates it to success/failed on completion.
INSERT INTO task_runs (task_id)
VALUES ($1)
RETURNING id, task_id, started_at, ended_at, status, output, error;

-- name: FinishTaskRun :exec
-- Marks a task_runs row complete with its final status, output, and error.
UPDATE task_runs
SET ended_at = NOW(),
    status   = $2,
    output   = $3,
    error    = $4
WHERE id = $1;

-- name: ListTaskRuns :many
-- Returns the most recent runs for a task, newest first. Used by the
-- run-history drawer in the admin UI.
SELECT id, task_id, started_at, ended_at, status, output, error
FROM task_runs
WHERE task_id = $1
ORDER BY started_at DESC
LIMIT $2;
