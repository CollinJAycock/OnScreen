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

// TestNotificationAgents_DownUp_Integration: 00029's Down drops the table and
// its Up recreates it with its defaults.
//
// Run with: go test -tags 'dev integration' -run NotificationAgents ./internal/db/migrations/
func TestNotificationAgents_DownUp_Integration(t *testing.T) {
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

	tableExists := func() bool {
		var ok bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass('public.notification_agents') IS NOT NULL`).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !tableExists() {
		t.Fatal("notification_agents missing after migrating up")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO notification_agents (kind, name, events) VALUES ('email', 'Admins', '{task_failed}')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := goose.DownToContext(ctx, db, ".", 28); err != nil {
		t.Fatalf("down to 28: %v", err)
	}
	if tableExists() {
		t.Fatal("notification_agents survived Down")
	}

	if err := goose.UpToContext(ctx, db, ".", 29); err != nil {
		t.Fatalf("up to 29: %v", err)
	}
	var enabled, allowPrivate bool
	var cfg string
	if err := pool.QueryRow(ctx, `INSERT INTO notification_agents (kind, name) VALUES ('discord', 'D')
		RETURNING enabled, allow_private_network, config::text`).Scan(&enabled, &allowPrivate, &cfg); err != nil {
		t.Fatalf("insert after re-up: %v", err)
	}
	if !enabled || allowPrivate || cfg != "{}" {
		t.Errorf("defaults: enabled=%v allow_private_network=%v config=%s", enabled, allowPrivate, cfg)
	}
}
