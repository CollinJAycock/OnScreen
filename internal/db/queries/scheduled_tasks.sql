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
--
-- A task that still has a run in flight is not leased: not the ones this
-- instance is running (exclude_ids, which also covers the moment between the
-- lease and the task_runs insert), and not one with a live run anywhere
-- (a 'running' row whose heartbeat is fresher than stale_after_secs). Its
-- next_run_at stays in the past, so a fire that came due mid-run is taken
-- once, on the first tick after the run ends, instead of starting a second
-- copy beside it. A heartbeat older than the window means the instance that
-- owned the run is gone, so the row no longer blocks.
SELECT st.id, st.name, st.task_type, st.config, st.cron_expr, st.enabled,
       st.last_run_at, st.next_run_at, st.last_status, st.last_error,
       st.created_at, st.updated_at
FROM scheduled_tasks st
WHERE st.enabled
  AND st.next_run_at <= NOW()
  AND NOT (st.id = ANY(sqlc.arg(exclude_ids)::uuid[]))
  AND NOT EXISTS (
        SELECT 1 FROM task_runs tr
         WHERE tr.task_id = st.id
           AND tr.status = 'running'
           AND tr.heartbeat_at > NOW() - make_interval(secs => sqlc.arg(stale_after_secs)::float8))
ORDER BY st.next_run_at
LIMIT sqlc.arg(lim)
FOR UPDATE OF st SKIP LOCKED;

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
RETURNING id, task_id, started_at, ended_at, status, output, error, heartbeat_at;

-- name: FinishTaskRun :exec
-- Marks a task_runs row complete with its final status, output, and error.
UPDATE task_runs
SET ended_at = NOW(),
    status   = $2,
    output   = $3,
    error    = $4
WHERE id = $1;

-- name: HeartbeatTaskRuns :exec
-- Stamps the runs this scheduler instance still has in flight, once per tick,
-- so the lease and the stale sweep can tell them from runs whose instance died.
UPDATE task_runs
SET heartbeat_at = NOW()
WHERE id = ANY(sqlc.arg(run_ids)::uuid[])
  AND status = 'running';

-- name: AbandonStaleTaskRuns :execrows
-- Closes 'running' rows whose heartbeat stopped: the instance that owned
-- them crashed or was killed before it could record a result. Without this
-- they read 'running' in the run history forever. keep_ids are the runs the
-- calling instance is itself executing, never touched even if a heartbeat
-- write was missed.
UPDATE task_runs
SET status   = 'failed',
    ended_at = NOW(),
    error    = sqlc.arg(error_message)::text
WHERE status = 'running'
  AND heartbeat_at < NOW() - make_interval(secs => sqlc.arg(stale_after_secs)::float8)
  AND NOT (id = ANY(sqlc.arg(keep_ids)::uuid[]));

-- name: PruneTaskRuns :execrows
-- Run-history retention: a finished run is deleted once it is older than
-- keep_days AND is not among its task's keep_per_task newest runs, so a
-- task that fires every minute keeps a month of history and a weekly one
-- keeps its last keep_per_task runs however old they are.
DELETE FROM task_runs tr
USING (
    SELECT id,
           row_number() OVER (PARTITION BY task_id ORDER BY started_at DESC) AS rn
    FROM task_runs
) ranked
WHERE tr.id = ranked.id
  AND ranked.rn > sqlc.arg(keep_per_task)::int
  AND tr.started_at < NOW() - make_interval(days => sqlc.arg(keep_days)::int)
  AND tr.status <> 'running';

-- name: ListTaskRuns :many
-- Returns the most recent runs for a task, newest first. Used by the
-- run-history drawer in the admin UI.
SELECT id, task_id, started_at, ended_at, status, output, error, heartbeat_at
FROM task_runs
WHERE task_id = $1
ORDER BY started_at DESC
LIMIT $2;
