-- +goose Up
-- +goose StatementBegin
-- Task-role plugins as scheduled tasks (v2.7).
--
-- Every task-role plugin gets one scheduled_tasks row of type 'plugin_task'
-- whose config is {"plugin_id": "<uuid>"}; the scheduler's plugin_task
-- handler calls that plugin's run_task tool. The rows are created and
-- deleted with the plugin (internal/plugin Registry) and reconciled at boot.
--
-- uq_scheduled_tasks_enabled_type allowed one ENABLED task per task_type,
-- which would cap a server at one enabled task plugin. Plugin tasks are keyed
-- by their plugin instead: the type index leaves them out, and a second
-- index allows one plugin_task row per plugin (enabled or not), which is also
-- what makes the create-with-the-plugin insert idempotent.
DROP INDEX IF EXISTS public.uq_scheduled_tasks_enabled_type;
CREATE UNIQUE INDEX uq_scheduled_tasks_enabled_type
    ON public.scheduled_tasks USING btree (task_type)
    WHERE enabled AND task_type <> 'plugin_task';

CREATE UNIQUE INDEX uq_scheduled_tasks_plugin
    ON public.scheduled_tasks USING btree ((config ->> 'plugin_id'))
    WHERE task_type = 'plugin_task';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The old index can't hold two enabled plugin tasks, and nothing before this
-- version runs them, so the rows go with it.
DROP INDEX IF EXISTS public.uq_scheduled_tasks_plugin;
DELETE FROM public.scheduled_tasks WHERE task_type = 'plugin_task';
DROP INDEX IF EXISTS public.uq_scheduled_tasks_enabled_type;
CREATE UNIQUE INDEX uq_scheduled_tasks_enabled_type
    ON public.scheduled_tasks USING btree (task_type)
    WHERE enabled;
-- +goose StatementEnd
