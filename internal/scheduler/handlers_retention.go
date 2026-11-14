package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// Task-run history retention: a run is kept while it is among its task's
// TaskRunKeepPerTask newest runs OR younger than TaskRunKeepDays, whichever
// keeps more. The per-task floor matches the run-history drawer's default
// page, so a weekly task never shows an empty history; the age bound keeps
// a month of a per-minute task (~43k rows) instead of letting the table grow
// by ~2,000 rows a day forever.
const (
	TaskRunKeepPerTask = 50
	TaskRunKeepDays    = 30
)

// RetentionStore is the DB surface of the retention_purge task. *gen.Queries
// satisfies it. Each delete is idempotent, so a second scheduler (the
// standalone cmd/worker still clears sessions hourly) running the same one
// is harmless.
type RetentionStore interface {
	DeleteExpiredSessions(ctx context.Context) (int64, error)
	DeleteExpiredPasswordResetTokens(ctx context.Context) (int64, error)
	DeleteOldNotifications(ctx context.Context) (int64, error)
	PruneTaskRuns(ctx context.Context, arg gen.PruneTaskRunsParams) (int64, error)
}

// NewRetentionHandler returns the retention_purge handler: it deletes expired
// refresh sessions, spent or expired password-reset links, notifications
// older than 30 days, and task-run history past its retention. Every step
// runs even if an earlier one failed; the run fails if any did, naming them.
func NewRetentionHandler(store RetentionStore) HandlerFunc {
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		steps := []struct {
			name string
			run  func(context.Context) (int64, error)
		}{
			{"sessions", store.DeleteExpiredSessions},
			{"password_reset_tokens", store.DeleteExpiredPasswordResetTokens},
			{"notifications", store.DeleteOldNotifications},
			{"task_runs", func(ctx context.Context) (int64, error) {
				return store.PruneTaskRuns(ctx, gen.PruneTaskRunsParams{
					KeepPerTask: TaskRunKeepPerTask,
					KeepDays:    TaskRunKeepDays,
				})
			}},
		}
		var (
			parts []string
			errs  []error
		)
		for _, st := range steps {
			n, err := st.run(ctx)
			if err != nil {
				errs = append(errs, fmt.Errorf("purge %s: %w", st.name, err))
				parts = append(parts, st.name+"=error")
				continue
			}
			parts = append(parts, fmt.Sprintf("%s=%d", st.name, n))
		}
		return "deleted " + strings.Join(parts, " "), errors.Join(errs...)
	}
}
