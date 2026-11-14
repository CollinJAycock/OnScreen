//go:build integration

package scheduler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func seedDueTask(ctx context.Context, t *testing.T, q *gen.Queries, name string) uuid.UUID {
	t.Helper()
	row, err := q.CreateScheduledTask(ctx, gen.CreateScheduledTaskParams{
		Name:      name,
		TaskType:  "test_" + name,
		Config:    json.RawMessage(`{}`),
		CronExpr:  "* * * * *",
		Enabled:   true,
		NextRunAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateScheduledTask %s: %v", name, err)
	}
	return row.ID
}

func leasedIDs(tasks []Task) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(tasks))
	for _, t := range tasks {
		out[t.ID] = true
	}
	return out
}

func farFuture(Task) (time.Time, error) { return time.Now().Add(time.Hour), nil }

// The lease skips a task with a live run (fresh heartbeat) and the ids the
// caller excludes, leaving their next_run_at due; once the run's heartbeat
// goes stale it no longer blocks, and the sweep closes the row.
func TestPgxQuerier_Integration_LeaseSkipsLiveRunsAndSweepsStale(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := gen.New(pool)
	pq := NewPgxQuerier(pool)

	busy := seedDueTask(ctx, t, q, "busy")
	excluded := seedDueTask(ctx, t, q, "excluded")

	runID, err := pq.CreateRun(ctx, busy)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	got, err := pq.LeaseDueTasks(ctx, 16, []uuid.UUID{excluded}, farFuture)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("leased %v; want nothing (one live run, one excluded)", got)
	}
	for _, id := range []uuid.UUID{busy, excluded} {
		row, err := q.GetScheduledTask(ctx, id)
		if err != nil {
			t.Fatalf("GetScheduledTask: %v", err)
		}
		if row.NextRunAt.Time.After(time.Now()) {
			t.Fatalf("skipped task %s had next_run_at advanced to %v", id, row.NextRunAt.Time)
		}
	}

	// A fresh heartbeat keeps the run out of the sweep.
	if err := pq.HeartbeatRuns(ctx, []uuid.UUID{runID}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if n, err := pq.AbandonStaleRuns(ctx, nil, errMsgAbandoned); err != nil || n != 0 {
		t.Fatalf("sweep of a live run: n=%d err=%v", n, err)
	}

	// The owner dies: its heartbeat goes stale.
	if _, err := pool.Exec(ctx, `UPDATE task_runs SET heartbeat_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`, runID); err != nil {
		t.Fatalf("age heartbeat: %v", err)
	}
	// keep ids are never swept, even when stale.
	if n, err := pq.AbandonStaleRuns(ctx, []uuid.UUID{runID}, errMsgAbandoned); err != nil || n != 0 {
		t.Fatalf("sweep with the run kept: n=%d err=%v", n, err)
	}
	got, err = pq.LeaseDueTasks(ctx, 16, nil, farFuture)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if ids := leasedIDs(got); !ids[busy] || !ids[excluded] {
		t.Fatalf("leased %v; want both tasks once the run is stale and nothing excluded", got)
	}
	if n, err := pq.AbandonStaleRuns(ctx, nil, errMsgAbandoned); err != nil || n != 1 {
		t.Fatalf("sweep: n=%d err=%v; want 1", n, err)
	}
	runs, err := q.ListTaskRuns(ctx, gen.ListTaskRunsParams{TaskID: busy, Limit: 10})
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListTaskRuns: %v %v", runs, err)
	}
	if runs[0].Status != "failed" || runs[0].Error != errMsgAbandoned || !runs[0].EndedAt.Valid {
		t.Fatalf("stale run not closed: %+v", runs[0])
	}
	// A finished run is not 'running' any more, so heartbeats leave it alone.
	if err := pq.HeartbeatRuns(ctx, []uuid.UUID{runID}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
}

func insertRuns(ctx context.Context, t *testing.T, pool *pgxpool.Pool, taskID uuid.UUID, n int, age time.Duration) {
	t.Helper()
	for i := 0; i < n; i++ {
		started := time.Now().Add(-age - time.Duration(i)*time.Minute)
		if _, err := pool.Exec(ctx, `INSERT INTO task_runs (task_id, started_at, ended_at, status, heartbeat_at)
			VALUES ($1, $2, $2, 'success', $2)`, taskID, started); err != nil {
			t.Fatalf("insert run: %v", err)
		}
	}
}

func countRuns(ctx context.Context, t *testing.T, pool *pgxpool.Pool, taskID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM task_runs WHERE task_id = $1`, taskID).Scan(&n); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	return n
}

// Run history keeps the newest TaskRunKeepPerTask runs of each task or the
// last TaskRunKeepDays days, whichever is more; the other purges drop only
// what can never be used again.
func TestRetention_Integration_Purges(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := gen.New(pool)

	frequent := seedDueTask(ctx, t, q, "frequent")
	weekly := seedDueTask(ctx, t, q, "weekly")
	insertRuns(ctx, t, pool, frequent, 20, time.Hour)       // recent: kept by age
	insertRuns(ctx, t, pool, frequent, 60, 40*24*time.Hour) // old: kept only up to the per-task floor
	insertRuns(ctx, t, pool, weekly, 5, 200*24*time.Hour)   // old but few: kept
	runningOld := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO task_runs (id, task_id, started_at, status, heartbeat_at)
		VALUES ($1, $2, NOW() - INTERVAL '90 days', 'running', NOW())`, runningOld, weekly); err != nil {
		t.Fatalf("insert running run: %v", err)
	}

	hash := "placeholder"
	user, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "retention-" + uuid.NewString()[:8], PasswordHash: &hash})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO notifications (user_id, type, title, created_at) VALUES
		($1, 'system', 'old', NOW() - INTERVAL '31 days'), ($1, 'system', 'new', NOW())`, user.ID); err != nil {
		t.Fatalf("insert notifications: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO password_reset_tokens (user_id, token_hash, expires_at, used_at) VALUES
		($1, 'expired', NOW() - INTERVAL '1 hour', NULL),
		($1, 'spent', NOW() + INTERVAL '1 hour', NOW()),
		($1, 'live', NOW() + INTERVAL '1 hour', NULL)`, user.ID); err != nil {
		t.Fatalf("insert reset tokens: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sessions (user_id, token_hash, expires_at) VALUES
		($1, 'expired', NOW() - INTERVAL '1 minute'), ($1, 'live', NOW() + INTERVAL '1 day')`, user.ID); err != nil {
		t.Fatalf("insert sessions: %v", err)
	}

	out, err := NewRetentionHandler(q).Run(ctx, nil)
	if err != nil {
		t.Fatalf("retention: %v (%s)", err, out)
	}
	// frequent: 80 runs, newest 50 kept by the floor (20 recent + 30 old);
	// the other 30 old ones go.
	if want := "deleted sessions=1 password_reset_tokens=2 notifications=1 task_runs=30"; out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
	if n := countRuns(ctx, t, pool, frequent); n != TaskRunKeepPerTask {
		t.Fatalf("frequent task keeps %d runs, want %d", n, TaskRunKeepPerTask)
	}
	if n := countRuns(ctx, t, pool, weekly); n != 6 {
		t.Fatalf("weekly task keeps %d runs, want 6 (5 old + 1 running)", n)
	}

	// Second run: nothing left to delete.
	out, err = NewRetentionHandler(q).Run(ctx, nil)
	if err != nil || out != "deleted sessions=0 password_reset_tokens=0 notifications=0 task_runs=0" {
		t.Fatalf("second run: %q %v", out, err)
	}
}
