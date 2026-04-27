package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/scheduler"
)

// systemTask is one row to guarantee exists in scheduled_tasks at boot.
// Kept minimal — task_type is the match key (uniqueness enforced by the
// EnsureSystemTask query's WHERE NOT EXISTS clause, not a DB constraint).
type systemTask struct {
	name     string
	taskType string
	cronExpr string
	enabled  bool
}

// requiredSystemTasks lists the scheduler rows the server itself depends
// on. A missing row here isn't a user choice — the corresponding feature
// silently fails (DVR never matches schedules → never records; EPG goes
// stale → matcher has nothing to match against). Keep this list narrow:
// operator-discretion tasks (backup, ocr) stay out so admins can opt in
// rather than finding surprise daily jobs in their UI.
var requiredSystemTasks = []systemTask{
	{
		name:     "DVR matcher",
		taskType: "dvr_match",
		// Every minute: a user scheduling a one-off recording expects
		// it to land before the show starts, not up to 15 minutes late.
		cronExpr: "* * * * *",
		enabled:  true,
	},
	{
		name:     "EPG refresh",
		taskType: "epg_refresh",
		// Every 15 min: upstream XMLTV / Schedules Direct sources
		// publish hourly at best, so tighter polling just burns HTTP.
		cronExpr: "*/15 * * * *",
		enabled:  true,
	},
	{
		name:     "DVR retention purge",
		taskType: "dvr_retention",
		// Daily at 3:17am local — off-peak hours, slight prime-number
		// offset to avoid synchronizing with other hourly jobs if the
		// operator schedules custom backups later. Each run is a DB
		// read plus a file-system walk; runtime is negligible even for
		// hundreds of retained recordings.
		cronExpr: "17 3 * * *",
		enabled:  true,
	},
	{
		name:     "Update item cooccurrence",
		taskType: "update_item_cooccurrence",
		// Daily at 3:31am local — same off-peak window as DVR retention,
		// staggered slightly so the two heaviest nightly jobs don't run
		// concurrently. The rebuild is a single aggregation query whose
		// runtime scales with watch_events × user count; for a homelab-
		// scale install (a few users, thousands of events) it's well
		// under a minute.
		cronExpr: "31 3 * * *",
		enabled:  true,
	},
}

// seedSystemTasks inserts any missing required task rows. Idempotent —
// repeated calls are a no-op because EnsureSystemTask only writes when
// no row of that task_type already exists. Safe to run on every boot.
//
// Errors are logged but not returned: a transient DB hiccup here
// shouldn't prevent the server from starting. The next reboot will
// retry, and the admin's Tasks UI exposes manual recreation.
func seedSystemTasks(ctx context.Context, q *gen.Queries, logger *slog.Logger) {
	now := time.Now().UTC()
	for _, t := range requiredSystemTasks {
		next, err := scheduler.NextRun(t.cronExpr, now)
		if err != nil {
			logger.ErrorContext(ctx, "seed system task: cron parse",
				"task_type", t.taskType, "cron_expr", t.cronExpr, "err", err)
			continue
		}
		err = q.EnsureSystemTask(ctx, gen.EnsureSystemTaskParams{
			Name:      t.name,
			TaskType:  t.taskType,
			CronExpr:  t.cronExpr,
			Enabled:   t.enabled,
			NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
		})
		if err != nil {
			logger.WarnContext(ctx, "seed system task: ensure",
				"task_type", t.taskType, "err", err)
			continue
		}
	}
}
