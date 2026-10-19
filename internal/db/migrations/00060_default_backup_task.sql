-- +goose Up
-- +goose StatementBegin
-- A new install gets a daily database backup from the first night, instead of
-- none until an admin creates the task. Fresh installs ONLY: migrations run
-- before the setup wizard creates the first account, so an empty users table
-- means nobody has used this database yet. An existing server (users present)
-- is left exactly as its admin configured it — including one whose admin
-- deliberately has no backup task, or backs up some other way.
--
-- Daily at 03:11 (clear of the 03:17 DVR-retention / NFO jobs), keeping the
-- newest 7 dumps. /var/backups/onscreen is the Tasks form's default and is
-- pre-created owner-only in the Docker images. next_run_at is computed in the
-- database session's time zone; after the first run the scheduler recomputes
-- it from the cron in server local time.
INSERT INTO public.scheduled_tasks (name, task_type, config, cron_expr, enabled, next_run_at)
SELECT 'Database backup',
       'backup_database',
       '{"output_dir": "/var/backups/onscreen", "retain_count": 7}'::jsonb,
       '11 3 * * *',
       true,
       date_trunc('day', now()) + interval '3 hours 11 minutes'
         + CASE WHEN date_trunc('day', now()) + interval '3 hours 11 minutes' <= now()
                THEN interval '1 day' ELSE interval '0' END
WHERE NOT EXISTS (SELECT 1 FROM public.users)
  AND NOT EXISTS (SELECT 1 FROM public.scheduled_tasks WHERE task_type = 'backup_database');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Remove only the row Up seeded, and only while it still looks seeded: an
-- admin who renamed, retimed or re-pointed it has made it their own.
DELETE FROM public.scheduled_tasks
WHERE task_type = 'backup_database'
  AND name = 'Database backup'
  AND cron_expr = '11 3 * * *'
  AND config = '{"output_dir": "/var/backups/onscreen", "retain_count": 7}'::jsonb;
-- +goose StatementEnd
