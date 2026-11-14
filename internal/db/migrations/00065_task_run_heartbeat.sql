-- +goose Up
-- +goose StatementBegin
-- A heartbeat per in-flight task run, so a 'running' row can be told apart
-- from one whose scheduler died. The scheduler stamps heartbeat_at on every
-- tick (30 s) for the runs it owns; the lease skips a task while it has a run
-- with a fresh heartbeat (no second copy of a run that outlasts its period,
-- across instances too), and rows whose heartbeat has gone stale are closed
-- as failed instead of reading 'running' forever.
--
-- Existing rows take their last known activity, so rows left 'running' by a
-- crash before this migration are stale straight away and get swept.
ALTER TABLE task_runs ADD COLUMN heartbeat_at TIMESTAMPTZ;
UPDATE task_runs SET heartbeat_at = COALESCE(ended_at, started_at);
ALTER TABLE task_runs
    ALTER COLUMN heartbeat_at SET DEFAULT NOW(),
    ALTER COLUMN heartbeat_at SET NOT NULL;

-- The lease's live-run check and the stale sweep both look only at running
-- rows: a handful at any time out of tens of thousands of finished ones.
CREATE INDEX idx_task_runs_running ON task_runs (task_id, heartbeat_at) WHERE status = 'running';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_task_runs_running;
ALTER TABLE task_runs DROP COLUMN heartbeat_at;
-- +goose StatementEnd
