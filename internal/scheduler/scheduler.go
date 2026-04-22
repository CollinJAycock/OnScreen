// Package scheduler runs cron-driven admin tasks (backup, rescan, future
// plugin-provided handlers).
//
// The scheduler ticks every 30 s. On each tick it atomically leases due
// tasks from scheduled_tasks via FOR UPDATE SKIP LOCKED, advances their
// next_run_at to the next cron-computed fire time, then dispatches one
// goroutine per leased task to run the registered handler. After the
// handler returns, the scheduler records a task_runs row and updates
// last_run_at / last_status / last_error on scheduled_tasks.
//
// Safety: because the lease advances next_run_at before the handler runs,
// a crashed scheduler instance loses at most one iteration per task — the
// row is naturally skipped until the new next_run_at. If the row-advance
// races with an aggressive cron (e.g. "* * * * *"), the handler may be
// skipped for one minute; this is considered acceptable for admin tasks.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// Task is the plain-Go representation of a scheduled_tasks row.
type Task struct {
	ID        uuid.UUID
	Name      string
	Type      string
	Config    json.RawMessage
	CronExpr  string
	Enabled   bool
	NextRunAt time.Time
}

// Handler executes a single invocation of a task.
//
// Handlers receive the task's raw config (opaque JSON agreed upon between
// the handler and the creator of the task). A handler must be safe to
// call concurrently — the scheduler dispatches one goroutine per leased
// task and does not serialize across tasks of the same type.
type Handler interface {
	Run(ctx context.Context, config json.RawMessage) (output string, err error)
}

// HandlerFunc adapts a function into a Handler.
type HandlerFunc func(ctx context.Context, config json.RawMessage) (string, error)

func (f HandlerFunc) Run(ctx context.Context, cfg json.RawMessage) (string, error) {
	return f(ctx, cfg)
}

// Registry maps task types (e.g. "backup_database") to their handlers.
// Plugins register additional types at startup — outbound-MCP plugins
// register via an adapter that forwards the config to the plugin server.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

// Register adds a handler under the given type name. Registering a name
// twice replaces the earlier entry — this lets tests swap handlers in.
func (r *Registry) Register(taskType string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[taskType] = h
}

// Get returns the handler for a type, or ok=false if none is registered.
func (r *Registry) Get(taskType string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[taskType]
	return h, ok
}

// Types returns all registered task type names in undefined order. Used
// by the admin UI to populate the "task type" dropdown.
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.handlers))
	for t := range r.handlers {
		out = append(out, t)
	}
	return out
}

// Querier is the DB surface the scheduler uses. The production
// implementation (PgxQuerier) holds a pgx pool; the test fake is in-memory.
type Querier interface {
	// LeaseDueTasks atomically picks enabled tasks whose next_run_at has
	// elapsed and advances each to the next cron-computed fire time, as
	// returned by the nextRun callback. Returns the leased tasks for
	// dispatch. Implementations must use SELECT FOR UPDATE SKIP LOCKED so
	// multiple scheduler instances cannot double-fire a task.
	LeaseDueTasks(ctx context.Context, limit int, nextRun func(Task) (time.Time, error)) ([]Task, error)

	// CreateRun inserts a task_runs row at the start of an execution and
	// returns its id. FinishRun updates that row on completion.
	CreateRun(ctx context.Context, taskID uuid.UUID) (uuid.UUID, error)
	FinishRun(ctx context.Context, runID uuid.UUID, status, output, errMsg string) error

	// RecordResult updates last_run_at / last_status / last_error on the
	// scheduled_tasks row. Does not touch next_run_at (set at lease time).
	RecordResult(ctx context.Context, taskID uuid.UUID, status, errMsg string) error
}

// Scheduler runs the tick loop. Construct with New, then launch Run in a
// goroutine. Run returns when ctx is cancelled.
type Scheduler struct {
	q        Querier
	registry *Registry
	logger   *slog.Logger

	interval  time.Duration
	batchSize int
}

// New constructs a Scheduler with default interval (30 s) and batch size (16).
func New(q Querier, registry *Registry, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		q:         q,
		registry:  registry,
		logger:    logger,
		interval:  30 * time.Second,
		batchSize: 16,
	}
}

// Run is the long-lived tick loop. Blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	s.logger.Info("scheduler starting", "interval", s.interval, "handlers", s.registry.Types())
	// Immediate first tick so a task with next_run_at in the past runs
	// right after boot instead of waiting for the first ticker fire.
	s.tick(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopping")
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick leases due tasks and dispatches them. Errors are logged and
// swallowed so a transient DB issue doesn't kill the scheduler.
func (s *Scheduler) tick(ctx context.Context) {
	tasks, err := s.q.LeaseDueTasks(ctx, s.batchSize, s.nextRunFor)
	if err != nil {
		s.logger.Warn("scheduler: lease failed", "err", err)
		return
	}
	if len(tasks) == 0 {
		return
	}
	s.logger.Debug("scheduler: leased tasks", "count", len(tasks))

	for _, t := range tasks {
		go s.execute(ctx, t)
	}
}

// nextRunFor is the callback handed to LeaseDueTasks — parses the row's
// cron_expr and returns the next fire after now. A parse failure returns
// a far-future time so the task self-quarantines until an admin fixes it;
// we log loudly.
func (s *Scheduler) nextRunFor(t Task) (time.Time, error) {
	next, err := NextRun(t.CronExpr, time.Now())
	if err != nil {
		s.logger.Error("scheduler: invalid cron expression — quarantining task",
			"task_id", t.ID, "task_type", t.Type, "cron_expr", t.CronExpr, "err", err)
		return time.Now().Add(100 * 365 * 24 * time.Hour), nil
	}
	return next, nil
}

// execute runs a single task's handler, records the run, and writes the
// result back to scheduled_tasks. Panics are recovered so one bad handler
// can't take down the scheduler goroutine pool.
func (s *Scheduler) execute(ctx context.Context, t Task) {
	logger := s.logger.With("task_id", t.ID, "task_type", t.Type, "task_name", t.Name)

	handler, ok := s.registry.Get(t.Type)
	if !ok {
		logger.Warn("scheduler: unknown task type — marking failed")
		s.recordFailure(ctx, t.ID, uuid.Nil, "", "unknown task type: "+t.Type)
		return
	}

	runID, err := s.q.CreateRun(ctx, t.ID)
	if err != nil {
		logger.Warn("scheduler: create task_run failed", "err", err)
		return
	}

	output, runErr := safeRun(ctx, handler, t.Config)
	if runErr != nil {
		logger.Warn("scheduler: handler failed", "err", runErr)
		s.recordFailure(ctx, t.ID, runID, output, runErr.Error())
		return
	}
	logger.Info("scheduler: handler succeeded", "output_bytes", len(output))
	s.recordSuccess(ctx, t.ID, runID, output)
}

// safeRun wraps the handler in panic recovery, capturing the panic value
// and stack in the returned error.
func safeRun(ctx context.Context, h Handler, cfg json.RawMessage) (output string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v\n%s", r, debug.Stack())
		}
	}()
	return h.Run(ctx, cfg)
}

func (s *Scheduler) recordSuccess(ctx context.Context, taskID, runID uuid.UUID, output string) {
	if runID != uuid.Nil {
		if err := s.q.FinishRun(ctx, runID, "success", output, ""); err != nil {
			s.logger.Warn("scheduler: finish run failed", "task_id", taskID, "run_id", runID, "err", err)
		}
	}
	if err := s.q.RecordResult(ctx, taskID, "success", ""); err != nil {
		s.logger.Warn("scheduler: record result failed", "task_id", taskID, "err", err)
	}
}

func (s *Scheduler) recordFailure(ctx context.Context, taskID, runID uuid.UUID, output, errMsg string) {
	if runID != uuid.Nil {
		if err := s.q.FinishRun(ctx, runID, "failed", output, errMsg); err != nil {
			s.logger.Warn("scheduler: finish run failed", "task_id", taskID, "run_id", runID, "err", err)
		}
	}
	if err := s.q.RecordResult(ctx, taskID, "failed", errMsg); err != nil {
		s.logger.Warn("scheduler: record result failed", "task_id", taskID, "err", err)
	}
}

// NextRun parses a 5-field cron expression and returns the next fire time
// strictly after `after`. Used by the scheduler tick and by the API
// handler when creating/updating a task.
func NextRun(expr string, after time.Time) (time.Time, error) {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron %q: %w", expr, err)
	}
	return sched.Next(after), nil
}
