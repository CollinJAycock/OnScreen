-- +goose Up
-- +goose StatementBegin
-- The external_ratings task moves from daily to hourly. Each run now uses
-- only what's left of the OMDb key's daily limit over the last 24 hours, so
-- a once-a-day run at a fixed time would find yesterday's run still inside
-- that window and do almost nothing. A schedule an admin changed is left
-- alone.
UPDATE public.scheduled_tasks
SET cron_expr = '41 * * * *',
    next_run_at = date_trunc('hour', now()) + interval '41 minutes'
                  + CASE WHEN date_trunc('hour', now()) + interval '41 minutes' <= now()
                         THEN interval '1 hour' ELSE interval '0' END,
    updated_at = now()
WHERE task_type = 'external_ratings' AND cron_expr = '41 5 * * *';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE public.scheduled_tasks
SET cron_expr = '41 5 * * *',
    next_run_at = date_trunc('day', now()) + interval '5 hours 41 minutes'
                  + CASE WHEN date_trunc('day', now()) + interval '5 hours 41 minutes' <= now()
                         THEN interval '1 day' ELSE interval '0' END,
    updated_at = now()
WHERE task_type = 'external_ratings' AND cron_expr = '41 * * * *';
-- +goose StatementEnd
