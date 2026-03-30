// Package worker contains background workers that run alongside the API server
// and the standalone worker process.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// validPartitionName matches partition table names like watch_events_2026_03.
var validPartitionName = regexp.MustCompile(`^watch_events_\d{4}_\d{2}$`)

// PartitionWorker ensures watch_events has partitions for the current and next
// 2 months, and detaches partitions older than retainMonths (ADR-002).
// No pg_partman extension required — keeps deployment dependencies minimal.
type PartitionWorker struct {
	pool         *pgxpool.Pool
	retainMonths int
	logger       *slog.Logger
}

// NewPartitionWorker creates a PartitionWorker.
func NewPartitionWorker(pool *pgxpool.Pool, retainMonths int, logger *slog.Logger) *PartitionWorker {
	return &PartitionWorker{pool: pool, retainMonths: retainMonths, logger: logger}
}

// Run calls EnsurePartitions on startup, then re-runs on the 1st of each month.
// Stops when ctx is cancelled.
func (w *PartitionWorker) Run(ctx context.Context) {
	// Run immediately on startup.
	if err := w.EnsurePartitions(ctx); err != nil {
		w.logger.ErrorContext(ctx, "partition worker: initial run failed", "err", err)
	}

	// Tick at the start of each new month.
	for {
		next := nextMonthStart()
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if err := w.EnsurePartitions(ctx); err != nil {
				w.logger.ErrorContext(ctx, "partition worker: monthly run failed", "err", err)
			}
		}
	}
}

// EnsurePartitions creates partitions for the current month + 2 future months
// and detaches partitions older than retainMonths. This function is idempotent.
func (w *PartitionWorker) EnsurePartitions(ctx context.Context) error {
	now := time.Now().UTC()

	// Create current + 2 future months.
	for i := range 3 {
		month := now.AddDate(0, i, 0)
		if err := w.createPartition(ctx, month); err != nil {
			return fmt.Errorf("create partition for %s: %w", month.Format("2006-01"), err)
		}
	}

	// Detach old partitions beyond retainMonths.
	cutoff := now.AddDate(0, -w.retainMonths, 0)
	if err := w.detachOldPartitions(ctx, cutoff); err != nil {
		return fmt.Errorf("detach old partitions: %w", err)
	}

	w.logger.InfoContext(ctx, "partition worker: partitions ensured",
		"retain_months", w.retainMonths)
	return nil
}

func (w *PartitionWorker) createPartition(ctx context.Context, month time.Time) error {
	// Truncate to first of month.
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)

	tableName := fmt.Sprintf("watch_events_%04d_%02d", start.Year(), start.Month())
	if !validPartitionName.MatchString(tableName) {
		return fmt.Errorf("invalid partition table name: %s", tableName)
	}
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s PARTITION OF watch_events
		FOR VALUES FROM ('%s') TO ('%s')`,
		tableName,
		start.Format("2006-01-02"),
		end.Format("2006-01-02"),
	)

	qCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := w.pool.Exec(qCtx, query)
	if err != nil {
		return err
	}

	w.logger.DebugContext(ctx, "partition ensured", "table", tableName)
	return nil
}

func (w *PartitionWorker) detachOldPartitions(ctx context.Context, cutoff time.Time) error {
	// List partitions older than cutoff and detach them.
	// DETACH PARTITION is instant (no row-level locking).
	qCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	rows, err := w.pool.Query(qCtx, `
		SELECT inhrelid::regclass::text
		FROM pg_inherits
		JOIN pg_class child ON inhrelid = child.oid
		JOIN pg_class parent ON inhparent = parent.oid
		WHERE parent.relname = 'watch_events'
		  AND child.relname < $1`,
		fmt.Sprintf("watch_events_%04d_%02d", cutoff.Year(), cutoff.Month()),
	)
	if err != nil {
		return fmt.Errorf("list old partitions: %w", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return err
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, table := range tables {
		if !validPartitionName.MatchString(table) {
			w.logger.WarnContext(ctx, "skipping partition with invalid name", "table", table)
			continue
		}
		detachCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := w.pool.Exec(detachCtx,
			fmt.Sprintf("ALTER TABLE watch_events DETACH PARTITION %s CONCURRENTLY", table))
		cancel()
		if err != nil {
			w.logger.WarnContext(ctx, "failed to detach old partition",
				"table", table, "err", err)
			continue
		}
		w.logger.InfoContext(ctx, "old partition detached", "table", table)

		// Drop the now-detached table to reclaim storage.
		dropCtx, dropCancel := context.WithTimeout(ctx, 10*time.Second)
		_, dropErr := w.pool.Exec(dropCtx,
			fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		dropCancel()
		if dropErr != nil {
			w.logger.WarnContext(ctx, "failed to drop detached partition",
				"table", table, "err", dropErr)
			continue
		}
		w.logger.InfoContext(ctx, "detached partition dropped", "table", table)
	}
	return nil
}

func nextMonthStart() time.Time {
	now := time.Now().UTC()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return first.AddDate(0, 1, 0)
}
