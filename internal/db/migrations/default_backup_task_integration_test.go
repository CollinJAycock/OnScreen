//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/testdb"
)

const defaultBackupVersion = 60

// gooseBefore60 opens dsn for goose and steps the schema down to just before 00060.
func gooseBefore60(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	if err := goose.DownToContext(context.Background(), db, ".", defaultBackupVersion-1); err != nil {
		t.Fatalf("down to %d: %v", defaultBackupVersion-1, err)
	}
	return db
}

// TestDefaultBackupTask_FreshInstall_Integration: a database nobody has signed
// up to yet gets the daily backup row on Up; Down removes it again.
//
// Run with: go test -tags 'dev integration' -run DefaultBackupTask ./internal/db/migrations/
func TestDefaultBackupTask_FreshInstall_Integration(t *testing.T) {
	pool, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()
	db := gooseBefore60(t, dsn)

	if _, err := pool.Exec(ctx, `DELETE FROM scheduled_tasks WHERE task_type = 'backup_database'`); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, ".", defaultBackupVersion); err != nil {
		t.Fatalf("up to %d: %v", defaultBackupVersion, err)
	}

	var (
		name, cron, outDir string
		retain             int
		enabled            bool
		next               time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT name, cron_expr, enabled, next_run_at,
		       config->>'output_dir', (config->>'retain_count')::int
		FROM scheduled_tasks WHERE task_type = 'backup_database'`).
		Scan(&name, &cron, &enabled, &next, &outDir, &retain)
	if err != nil {
		t.Fatalf("fresh install has no backup task: %v", err)
	}
	if cron != "11 3 * * *" || !enabled || outDir != "/var/backups/onscreen" || retain != 7 {
		t.Errorf("seeded task = %q cron=%q enabled=%v dir=%q retain=%d", name, cron, enabled, outDir, retain)
	}
	if until := time.Until(next); until <= 0 || until > 24*time.Hour {
		t.Errorf("next run %v is not within the coming day", next)
	}

	if err := goose.DownToContext(ctx, db, ".", defaultBackupVersion-1); err != nil {
		t.Fatalf("down: %v", err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM scheduled_tasks WHERE task_type = 'backup_database'`).Scan(&n)
	if n != 0 {
		t.Errorf("Down left %d backup task(s)", n)
	}
}

// TestDefaultBackupTask_UpgradeUntouched_Integration: an existing server (it
// has accounts) gets no backup task from the upgrade, and an admin's own
// backup task is never duplicated or removed.
func TestDefaultBackupTask_UpgradeUntouched_Integration(t *testing.T) {
	pool, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()
	db := gooseBefore60(t, dsn)

	if _, err := pool.Exec(ctx, `DELETE FROM scheduled_tasks WHERE task_type = 'backup_database'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (username, email, password_hash, is_admin)
		VALUES ('admin', 'admin@example.com', 'x', true)`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := goose.UpToContext(ctx, db, ".", defaultBackupVersion); err != nil {
		t.Fatalf("up: %v", err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM scheduled_tasks WHERE task_type = 'backup_database'`).Scan(&n)
	if n != 0 {
		t.Fatalf("upgrade of an existing server seeded %d backup task(s); want none", n)
	}

	// An admin-created backup task survives Down and isn't duplicated by Up.
	if err := goose.DownToContext(ctx, db, ".", defaultBackupVersion-1); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO scheduled_tasks (name, task_type, config, cron_expr, enabled, next_run_at)
		VALUES ('Nightly dump', 'backup_database', '{"output_dir":"/srv/dumps","retain_count":30}', '0 2 * * *', true, now() + interval '1 day')`); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, ".", defaultBackupVersion); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM scheduled_tasks WHERE task_type = 'backup_database'`).Scan(&n)
	if n != 1 {
		t.Errorf("with an existing backup task, Up left %d rows; want the admin's one", n)
	}
	if err := goose.DownToContext(ctx, db, ".", defaultBackupVersion-1); err != nil {
		t.Fatal(err)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM scheduled_tasks WHERE name = 'Nightly dump'`).Scan(&n)
	if n != 1 {
		t.Errorf("Down removed the admin's own backup task")
	}
}
