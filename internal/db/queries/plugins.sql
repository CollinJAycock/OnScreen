-- name: CreatePlugin :one
INSERT INTO plugins (name, role, transport, endpoint_url, allowed_hosts, enabled)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetPlugin :one
SELECT * FROM plugins WHERE id = $1;

-- name: ListPlugins :many
SELECT * FROM plugins ORDER BY name;

-- name: ListEnabledPluginsByRole :many
SELECT * FROM plugins WHERE role = $1 AND enabled = TRUE ORDER BY name;

-- name: UpdatePlugin :one
UPDATE plugins
SET name          = $2,
    endpoint_url  = $3,
    allowed_hosts = $4,
    enabled       = $5,
    updated_at    = NOW()
WHERE id = $1
RETURNING *;

-- name: DeletePlugin :exec
DELETE FROM plugins WHERE id = $1;

-- name: EnsurePluginTask :exec
-- Creates the scheduled_tasks row a task-role plugin runs from (migration
-- 00058): type plugin_task, config {"plugin_id": ...}. Idempotent on the
-- one-row-per-plugin index, so an existing row keeps the schedule, name and
-- enabled flag an admin gave it.
INSERT INTO scheduled_tasks (name, task_type, config, cron_expr, enabled, next_run_at)
VALUES (sqlc.arg(name), 'plugin_task',
        jsonb_build_object('plugin_id', sqlc.arg(plugin_id)::uuid::text),
        sqlc.arg(cron_expr), sqlc.arg(enabled), sqlc.arg(next_run_at))
ON CONFLICT ((config ->> 'plugin_id')) WHERE task_type = 'plugin_task' DO NOTHING;

-- name: DeletePluginTask :exec
-- Removes a plugin's scheduled task (its task_runs cascade).
DELETE FROM scheduled_tasks
WHERE task_type = 'plugin_task' AND config ->> 'plugin_id' = sqlc.arg(plugin_id)::uuid::text;

-- name: DeleteOrphanPluginTasks :execrows
-- Boot-time cleanup: plugin_task rows whose plugin is gone or is no longer a
-- task-role plugin.
DELETE FROM scheduled_tasks st
WHERE st.task_type = 'plugin_task'
  AND NOT EXISTS (
      SELECT 1 FROM plugins p
      WHERE p.role = 'task' AND p.id::text = st.config ->> 'plugin_id'
  );
