//go:build integration

package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestExternalRatingsHourly_Integration runs 00042 against a server that
// seeded the external_ratings task at its old daily schedule: Up moves it to
// hourly (next run at the coming :41), Down moves it back, and a schedule an
// admin changed is left alone both ways.
//
// Run with: go test -tags 'dev integration' -run ExternalRatingsHourly ./internal/db/migrations/
func TestExternalRatingsHourly_Integration(t *testing.T) {
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
	if err := goose.DownToContext(ctx, db, ".", 41); err != nil {
		t.Fatalf("down to 41: %v", err)
	}

	cron := func(taskType string) (string, time.Time) {
		t.Helper()
		var c string
		var next time.Time
		if err := pool.QueryRow(ctx, `SELECT cron_expr, next_run_at FROM scheduled_tasks WHERE task_type = $1`, taskType).Scan(&c, &next); err != nil {
			t.Fatalf("read %s: %v", taskType, err)
		}
		return c, next
	}
	seed := func(taskType, expr string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO scheduled_tasks (name, task_type, cron_expr, enabled, next_run_at) VALUES ($1, $1, $2, true, now() + interval '1 day')`,
			taskType, expr); err != nil {
			t.Fatalf("seed %s: %v", taskType, err)
		}
	}
	seed("external_ratings", "41 5 * * *")

	if err := goose.UpByOneContext(ctx, db, "."); err != nil {
		t.Fatalf("up to 42: %v", err)
	}
	c, next := cron("external_ratings")
	if c != "41 * * * *" {
		t.Errorf("cron after up = %q, want hourly", c)
	}
	if until := time.Until(next); until <= 0 || until > time.Hour || next.Minute() != 41 {
		t.Errorf("next run %v (in %v), want the coming :41", next, until)
	}

	if err := goose.DownToContext(ctx, db, ".", 41); err != nil {
		t.Fatalf("down to 41 again: %v", err)
	}
	if c, next := cron("external_ratings"); c != "41 5 * * *" || time.Until(next) <= 0 {
		t.Errorf("after down: %q next %v, want daily and in the future", c, next)
	}

	// An admin's own schedule survives both directions.
	if _, err := pool.Exec(ctx, `UPDATE scheduled_tasks SET cron_expr = '0 */6 * * *' WHERE task_type = 'external_ratings'`); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("up: %v", err)
	}
	if c, _ := cron("external_ratings"); c != "0 */6 * * *" {
		t.Errorf("custom schedule changed to %q", c)
	}
}
