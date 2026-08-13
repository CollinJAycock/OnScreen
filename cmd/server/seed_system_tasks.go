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
// genuinely operator-discretion tasks (e.g. backup) stay out entirely. The
// one exception is OCR, seeded **disabled** below: a heavy sweep that must
// never run unprompted, but operators previously had to know it existed and
// hand-create it. A disabled, pre-configured (off-peak weekly) row makes it
// one-click discoverable in the Tasks UI without surprising anyone.
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
		name:     "Refresh missing artwork",
		taskType: "refresh_missing_art",
		// Every 2 hours: covers the "scanned before the API key was
		// configured" case, transient TMDB outages, and any other
		// path that leaves a top-level item with no poster. The
		// enrich loop is scoped (only items with poster_path IS
		// NULL), so its cost is proportional to the actual gap. Each
		// run also verifies that claimed artwork still exists on
		// disk — one stat per stored poster/fanart path across the
		// library's movies + shows, cheap enough at this cadence —
		// and clears confirmed-dangling references so they re-enter
		// the missing-art pool instead of 404ing on every client.
		cronExpr: "23 */2 * * *",
		enabled:  true,
	},
	{
		name:     "Refresh analytics (watch_plays)",
		taskType: "refresh_watch_plays",
		// Every 10 min: rebuilds the watch_plays materialized view that the
		// analytics dashboard reads. The refresh recomputes a lead() window
		// over all stop/scrobble history, so it's deliberately off-request and
		// not too frequent; analytics tolerates a few minutes' lag (the
		// endpoint also memoizes its response for 5 min on top of this).
		cronExpr: "*/10 * * * *",
		enabled:  true,
	},
	{
		name:     "OCR image subtitles",
		taskType: "ocr_subtitles",
		// Weekly, Sunday 4:00am local — a full-library OCR sweep of image-based
		// (PGS/VOBSUB/DVB) subtitle streams is CPU-heavy and can run for hours,
		// so it's off-peak and infrequent. SkipExisting defaults true, so after
		// the first pass each run only processes newly imported image subs.
		// Seeded DISABLED: appears in the Tasks UI ready to enable, never runs
		// on its own.
		cronExpr: "0 4 * * 0",
		enabled:  false,
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
	// Local time, NOT UTC: the running scheduler (nextRunFor) and the task API
	// both compute next_run_at from local time.Now(), so a cron like "0 4 * * *"
	// means 4 AM local. Seeding with .UTC() would make the very first fire land
	// at a different wall-clock than every subsequent one on a non-UTC host.
	now := time.Now()
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
