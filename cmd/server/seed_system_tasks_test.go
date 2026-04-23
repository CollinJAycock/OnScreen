//go:build integration

// Integration test for the startup seeder. Guarantees a fresh install
// ends up with the required scheduled_tasks rows (DVR match, EPG
// refresh) AND that re-running doesn't trample admin edits.
//
// Run with: go test -tags=integration ./cmd/server/...
package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestSeedSystemTasks_Integration_CreatesMissingRows(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	seedSystemTasks(ctx, q, slog.Default())

	rows, err := q.ListScheduledTasks(ctx)
	if err != nil {
		t.Fatalf("ListScheduledTasks: %v", err)
	}
	seen := make(map[string]gen.ScheduledTask, len(rows))
	for _, r := range rows {
		seen[r.TaskType] = r
	}
	for _, req := range requiredSystemTasks {
		got, ok := seen[req.taskType]
		if !ok {
			t.Errorf("seed did not create %q", req.taskType)
			continue
		}
		if got.CronExpr != req.cronExpr {
			t.Errorf("%s cron_expr = %q, want %q", req.taskType, got.CronExpr, req.cronExpr)
		}
		if got.Enabled != req.enabled {
			t.Errorf("%s enabled = %v, want %v", req.taskType, got.Enabled, req.enabled)
		}
	}
}

func TestSeedSystemTasks_Integration_IdempotentOnRerun(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	seedSystemTasks(ctx, q, slog.Default())
	seedSystemTasks(ctx, q, slog.Default())
	seedSystemTasks(ctx, q, slog.Default())

	rows, _ := q.ListScheduledTasks(ctx)
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.TaskType]++
	}
	for _, req := range requiredSystemTasks {
		if counts[req.taskType] != 1 {
			t.Errorf("%s row count after triple-seed = %d, want 1 — seeder is not idempotent",
				req.taskType, counts[req.taskType])
		}
	}
}

// TestSeedSystemTasks_Integration_PreservesAdminEdits is the key
// property for operators: if an admin disables dvr_match (say, to pause
// recordings during maintenance) or tunes the cron, a restart must not
// silently re-enable or re-tune. EnsureSystemTask's WHERE NOT EXISTS
// clause is what makes this safe; if someone swaps it to ON CONFLICT
// DO UPDATE by accident, this test catches it.
func TestSeedSystemTasks_Integration_PreservesAdminEdits(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	seedSystemTasks(ctx, q, slog.Default())

	// Admin disables dvr_match and changes its cron.
	if _, err := pool.Exec(ctx,
		`UPDATE scheduled_tasks SET enabled = false, cron_expr = '*/5 * * * *' WHERE task_type = 'dvr_match'`,
	); err != nil {
		t.Fatalf("update: %v", err)
	}

	// Simulate a restart.
	seedSystemTasks(ctx, q, slog.Default())

	var enabled bool
	var cron string
	if err := pool.QueryRow(ctx,
		`SELECT enabled, cron_expr FROM scheduled_tasks WHERE task_type = 'dvr_match'`,
	).Scan(&enabled, &cron); err != nil {
		t.Fatalf("select: %v", err)
	}
	if enabled {
		t.Error("admin-disabled dvr_match was re-enabled by the seeder")
	}
	if cron != "*/5 * * * *" {
		t.Errorf("admin-tuned cron_expr was reset: got %q, want %q", cron, "*/5 * * * *")
	}
}
