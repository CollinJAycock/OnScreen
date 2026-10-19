//go:build integration

package migrations_test

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestPluginTasks_DownUp_Integration: 00058 lets several plugin_task rows be
// enabled at once (one per plugin); its Down drops those rows and restores
// the one-enabled-task-per-type index; Up again re-allows them.
//
// Run with: go test -tags 'dev integration' -run PluginTasks ./internal/db/migrations/
func TestPluginTasks_DownUp_Integration(t *testing.T) {
	pool, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()

	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}

	insertTwo := func() error {
		_, err := pool.Exec(ctx, `INSERT INTO scheduled_tasks (name, task_type, config, cron_expr, next_run_at) VALUES
			('p1', 'plugin_task', '{"plugin_id":"11111111-1111-1111-1111-111111111111"}', '0 * * * *', now()),
			('p2', 'plugin_task', '{"plugin_id":"22222222-2222-2222-2222-222222222222"}', '0 * * * *', now())`)
		return err
	}
	if err := insertTwo(); err != nil {
		t.Fatalf("two enabled plugin tasks rejected after 00058: %v", err)
	}

	if err := goose.DownToContext(ctx, db, ".", 57); err != nil {
		t.Fatalf("down to 57: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM scheduled_tasks WHERE task_type = 'plugin_task'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("Down left %d plugin_task rows", n)
	}
	if err := insertTwo(); err == nil {
		t.Error("the pre-00058 index should allow only one enabled task per type")
	}

	if err := goose.UpToContext(ctx, db, ".", 58); err != nil {
		t.Fatalf("up to 58: %v", err)
	}
	if err := insertTwo(); err != nil {
		t.Errorf("re-up: %v", err)
	}
}
