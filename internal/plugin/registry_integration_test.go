//go:build integration

package plugin_test

import (
	"context"
	"testing"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/plugin"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestPluginTasks_Integration covers migration 00058 and the plugin_task
// queries against Postgres: a task plugin gets exactly one scheduled task,
// several task plugins can each have an ENABLED one (the per-type unique
// index leaves plugin_task out), other types still allow one enabled task,
// and deleting a plugin or syncing after a stray delete leaves no orphans.
//
// Run with: go test -tags 'dev integration' -count=1 -p 1 -run Integration ./internal/plugin/...
func TestPluginTasks_Integration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := gen.New(pool)
	reg := plugin.NewRegistry(q)

	count := func(where string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM scheduled_tasks WHERE `+where, args...).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	a, err := reg.Create(ctx, plugin.CreateInput{Name: "A", Role: plugin.RoleTask, EndpointURL: "https://a.example.com/mcp", Enabled: true})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	b, err := reg.Create(ctx, plugin.CreateInput{Name: "B", Role: plugin.RoleTask, EndpointURL: "https://b.example.com/mcp", Enabled: true})
	if err != nil {
		t.Fatalf("create B (second enabled plugin task): %v", err)
	}
	if _, err := reg.Create(ctx, plugin.CreateInput{Name: "N", Role: plugin.RoleNotification, EndpointURL: "https://n.example.com/mcp", Enabled: true}); err != nil {
		t.Fatalf("create notification plugin: %v", err)
	}
	if n := count(`task_type = 'plugin_task' AND enabled`); n != 2 {
		t.Fatalf("expected 2 enabled plugin tasks, got %d", n)
	}

	var name, cron, cfgID string
	if err := pool.QueryRow(ctx, `SELECT name, cron_expr, config->>'plugin_id' FROM scheduled_tasks
		WHERE task_type = 'plugin_task' AND config->>'plugin_id' = $1`, a.ID.String()).Scan(&name, &cron, &cfgID); err != nil {
		t.Fatalf("read A's task: %v", err)
	}
	if name != "Plugin: A" || cron != plugin.DefaultTaskCron || cfgID != a.ID.String() {
		t.Errorf("A's task = %q %q %q", name, cron, cfgID)
	}

	// Ensure is idempotent and keeps an admin's schedule.
	if _, err := pool.Exec(ctx, `UPDATE scheduled_tasks SET cron_expr = '*/5 * * * *'
		WHERE config->>'plugin_id' = $1`, a.ID.String()); err != nil {
		t.Fatal(err)
	}
	if err := reg.SyncTasks(ctx); err != nil {
		t.Fatalf("SyncTasks: %v", err)
	}
	if n := count(`config->>'plugin_id' = $1`, a.ID.String()); n != 1 {
		t.Errorf("sync duplicated A's task: %d rows", n)
	}
	if n := count(`config->>'plugin_id' = $1 AND cron_expr = '*/5 * * * *'`, a.ID.String()); n != 1 {
		t.Error("sync reset the admin's schedule")
	}

	// Built-in types still allow one enabled task each.
	if _, err := pool.Exec(ctx, `INSERT INTO scheduled_tasks (name, task_type, cron_expr, next_run_at)
		VALUES ('x1', 'test_type', '0 * * * *', now()), ('x2', 'test_type', '0 * * * *', now())`); err == nil {
		t.Error("two enabled tasks of one built-in type were accepted")
	}
	// And one row per plugin, enabled or not.
	if _, err := pool.Exec(ctx, `INSERT INTO scheduled_tasks (name, task_type, config, cron_expr, enabled, next_run_at)
		VALUES ('dup', 'plugin_task', jsonb_build_object('plugin_id', $1::text), '0 * * * *', false, now())`, a.ID.String()); err == nil {
		t.Error("a second task row for one plugin was accepted")
	}

	// Delete takes the task with the plugin.
	if err := reg.Delete(ctx, a.ID); err != nil {
		t.Fatalf("delete A: %v", err)
	}
	if n := count(`config->>'plugin_id' = $1`, a.ID.String()); n != 0 {
		t.Errorf("A's task survived the delete")
	}

	// A plugin removed behind the registry's back leaves an orphan the
	// boot-time sync cleans up; a pre-v2.7 task plugin gets its row back.
	if _, err := pool.Exec(ctx, `DELETE FROM plugins WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	c, err := reg.Create(ctx, plugin.CreateInput{Name: "C", Role: plugin.RoleTask, EndpointURL: "https://c.example.com/mcp", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.DeletePluginTask(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := reg.SyncTasks(ctx); err != nil {
		t.Fatalf("SyncTasks: %v", err)
	}
	if n := count(`config->>'plugin_id' = $1`, b.ID.String()); n != 0 {
		t.Error("orphan task not removed")
	}
	if n := count(`config->>'plugin_id' = $1`, c.ID.String()); n != 1 {
		t.Error("missing task not recreated")
	}
}
