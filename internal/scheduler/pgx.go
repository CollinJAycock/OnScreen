package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// PgxQuerier is the production Querier implementation. It owns a pgx
// pool and uses short-lived transactions for the lease step; everything
// else runs outside a tx.
type PgxQuerier struct {
	pool *pgxpool.Pool
	q    *gen.Queries
}

// NewPgxQuerier wraps a pool with the scheduler Querier surface.
func NewPgxQuerier(pool *pgxpool.Pool) *PgxQuerier {
	return &PgxQuerier{pool: pool, q: gen.New(pool)}
}

// LeaseDueTasks runs the SELECT FOR UPDATE SKIP LOCKED + per-row
// SetScheduledTaskNextRun inside a single tx so the row is locked from
// the moment it's picked until next_run_at has been advanced. After
// commit, other scheduler ticks see the new next_run_at and skip the row.
func (p *PgxQuerier) LeaseDueTasks(ctx context.Context, limit int, nextRun func(Task) (time.Time, error)) ([]Task, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin lease tx: %w", err)
	}
	// Rollback is a no-op if Commit succeeded. Safe defer.
	defer func() { _ = tx.Rollback(ctx) }()

	qtx := p.q.WithTx(tx)
	rows, err := qtx.LeaseDueScheduledTasks(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("lease select: %w", err)
	}

	out := make([]Task, 0, len(rows))
	for _, r := range rows {
		t := rowToTask(r)
		next, err := nextRun(t)
		if err != nil {
			// Don't abort the whole batch for one bad row — the
			// callback's contract is to quarantine and return a valid
			// time. If it errored anyway, skip this task.
			continue
		}
		err = qtx.SetScheduledTaskNextRun(ctx, gen.SetScheduledTaskNextRunParams{
			ID:        r.ID,
			NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
		})
		if err != nil {
			return nil, fmt.Errorf("set next_run_at: %w", err)
		}
		out = append(out, t)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit lease: %w", err)
	}
	return out, nil
}

func (p *PgxQuerier) CreateRun(ctx context.Context, taskID uuid.UUID) (uuid.UUID, error) {
	r, err := p.q.CreateTaskRun(ctx, taskID)
	if err != nil {
		return uuid.Nil, err
	}
	return r.ID, nil
}

func (p *PgxQuerier) FinishRun(ctx context.Context, runID uuid.UUID, status, output, errMsg string) error {
	return p.q.FinishTaskRun(ctx, gen.FinishTaskRunParams{
		ID:     runID,
		Status: status,
		Output: output,
		Error:  errMsg,
	})
}

func (p *PgxQuerier) RecordResult(ctx context.Context, taskID uuid.UUID, status, errMsg string) error {
	return p.q.RecordScheduledTaskResult(ctx, gen.RecordScheduledTaskResultParams{
		ID:         taskID,
		LastStatus: status,
		LastError:  errMsg,
	})
}

// rowToTask converts a gen.ScheduledTask (with pgtype fields) into a
// plain-Go Task. Kept here because the gen import is internal to this
// file and doesn't leak into scheduler.go.
func rowToTask(r gen.ScheduledTask) Task {
	var nextRun time.Time
	if r.NextRunAt.Valid {
		nextRun = r.NextRunAt.Time
	}
	return Task{
		ID:        r.ID,
		Name:      r.Name,
		Type:      r.TaskType,
		Config:    r.Config,
		CronExpr:  r.CronExpr,
		Enabled:   r.Enabled,
		NextRunAt: nextRun,
	}
}

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("scheduler: task not found")
