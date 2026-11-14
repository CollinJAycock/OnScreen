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
//
// Lifecycle: a task never runs twice at once. The lease skips tasks this
// instance is already running, and tasks with a live run anywhere (a
// task_runs row whose heartbeat, stamped every tick, is fresher than
// StaleRunAfter). Runs are tracked; when Run's context ends (shutdown, or
// this instance losing the master lock) their contexts are cancelled and Run
// waits up to drainTimeout for them. Results are written on a detached
// context so a cancelled run still closes its task_runs row, and rows whose
// owner died without closing them are marked failed once their heartbeat
// goes stale.
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
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
// task and does not serialize across tasks of the same type (two rows of
// one type may run together; one row never overlaps itself). A handler
// should return promptly once ctx is cancelled: shutdown waits for it only
// up to drainTimeout.
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
	// multiple scheduler instances cannot double-fire a task, and must skip
	// (leaving next_run_at untouched) the exclude ids and any task with a
	// running task_runs row whose heartbeat is fresher than StaleRunAfter.
	LeaseDueTasks(ctx context.Context, limit int, exclude []uuid.UUID, nextRun func(Task) (time.Time, error)) ([]Task, error)

	// CreateRun inserts a task_runs row at the start of an execution and
	// returns its id. FinishRun updates that row on completion.
	CreateRun(ctx context.Context, taskID uuid.UUID) (uuid.UUID, error)
	FinishRun(ctx context.Context, runID uuid.UUID, status, output, errMsg string) error

	// RecordResult updates last_run_at / last_status / last_error on the
	// scheduled_tasks row. Does not touch next_run_at (set at lease time).
	RecordResult(ctx context.Context, taskID uuid.UUID, status, errMsg string) error

	// HeartbeatRuns marks the given in-flight runs as still alive.
	HeartbeatRuns(ctx context.Context, runIDs []uuid.UUID) error

	// AbandonStaleRuns closes, as failed with errMsg, every running row whose
	// heartbeat is older than StaleRunAfter, except the keep ids. Returns how
	// many rows it closed.
	AbandonStaleRuns(ctx context.Context, keep []uuid.UUID, errMsg string) (int64, error)
}

// StaleRunAfter is how long a running task_runs row may go without a
// heartbeat before it counts as orphaned: its scheduler crashed, was killed,
// or lost the database for that long. Ten missed 30 s ticks — long enough
// that a slow tick or a brief DB blip never frees a live run, short enough
// that a crash blocks a per-minute task like dvr_match for minutes, not
// forever.
const StaleRunAfter = 5 * time.Minute

// Bounds on the detached work that outlives a run's own context.
const (
	// defaultDrainTimeout is how long Run waits, once its context ends, for
	// in-flight runs to return. Their contexts are already cancelled, so a
	// well-behaved handler returns within it; the bound keeps a stuck one
	// from holding up shutdown or a master handover indefinitely.
	defaultDrainTimeout = 15 * time.Second
	// recordTimeout bounds each result write made on a detached context.
	recordTimeout = 10 * time.Second
)

// Messages written to task_runs.error for runs that did not finish normally.
const (
	errMsgAbandoned   = "abandoned: the server running this task stopped before it finished"
	errMsgInterrupted = "interrupted: the scheduler stopped (shutdown or master handover) before the run finished"
)

// Scheduler runs the tick loop. Construct with New, then launch Run in a
// goroutine. Run returns when ctx is cancelled.
type Scheduler struct {
	q        Querier
	registry *Registry
	logger   *slog.Logger

	interval     time.Duration
	batchSize    int
	drainTimeout time.Duration

	// onResult, when set, hears every finished run (runErr nil on success).
	// The outbound notification agents use it for task_failed /
	// backup_failed alerts.
	onResult func(ctx context.Context, t Task, runErr error)

	// inFlight maps the id of every task with a run in progress on this
	// instance to its task_runs id (uuid.Nil until CreateRun returns). It
	// outlives a single Run call: a run that ignored cancellation past the
	// drain window still blocks a second copy if this instance becomes
	// master again before it returns.
	mu       sync.Mutex
	inFlight map[uuid.UUID]uuid.UUID
	runs     sync.WaitGroup
}

// OnResult registers a hook called after every task run with the handler's
// error (nil on success). It runs on the task's goroutine, so it must not
// block for long. Call before Run. Returns s for chaining.
func (s *Scheduler) OnResult(fn func(ctx context.Context, t Task, runErr error)) *Scheduler {
	s.onResult = fn
	return s
}

// reportResult invokes the result hook, if any, shielding the scheduler from
// a panicking hook.
func (s *Scheduler) reportResult(ctx context.Context, t Task, runErr error) {
	if s.onResult == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("scheduler: result hook panicked", "task_id", t.ID, "panic", r)
		}
	}()
	s.onResult(ctx, t, runErr)
}

// New constructs a Scheduler with default interval (30 s) and batch size (16).
func New(q Querier, registry *Registry, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		q:            q,
		registry:     registry,
		logger:       logger,
		interval:     30 * time.Second,
		batchSize:    16,
		drainTimeout: defaultDrainTimeout,
		inFlight:     make(map[uuid.UUID]uuid.UUID),
	}
}

// Run is the long-lived tick loop. Blocks until ctx is cancelled, then
// until in-flight runs have returned (at most drainTimeout).
func (s *Scheduler) Run(ctx context.Context) {
	s.logger.Info("scheduler starting", "interval", s.interval, "handlers", s.registry.Types())
	// Immediate first tick so a task with next_run_at in the past runs
	// right after boot instead of waiting for the first ticker fire. The
	// tick's stale sweep closes the rows a crashed predecessor left open.
	s.tick(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopping")
			s.drain(ctx)
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick heartbeats this instance's runs, closes orphaned ones, then leases due
// tasks and dispatches them. Errors are logged and swallowed so a transient
// DB issue doesn't kill the scheduler.
func (s *Scheduler) tick(ctx context.Context) {
	taskIDs, runIDs := s.snapshotInFlight()
	if err := s.q.HeartbeatRuns(ctx, runIDs); err != nil {
		s.logger.Warn("scheduler: heartbeat failed", "runs", len(runIDs), "err", err)
	}
	// Every tick, not only at boot: an instance that died moments before
	// this one became master still had fresh heartbeats when Run started.
	if n, err := s.q.AbandonStaleRuns(ctx, runIDs, errMsgAbandoned); err != nil {
		s.logger.Warn("scheduler: stale run sweep failed", "err", err)
	} else if n > 0 {
		s.logger.Warn("scheduler: closed runs orphaned by a stopped instance", "count", n)
	}

	tasks, err := s.q.LeaseDueTasks(ctx, s.batchSize, taskIDs, s.nextRunFor)
	if err != nil {
		s.logger.Warn("scheduler: lease failed", "err", err)
		return
	}
	if len(tasks) == 0 {
		return
	}
	s.logger.Debug("scheduler: leased tasks", "count", len(tasks))

	for _, t := range tasks {
		if !s.claim(t.ID) {
			// The lease excluded in-flight ids, so this only happens if a
			// Querier ignores exclude. Skipping is the safe answer.
			s.logger.Warn("scheduler: leased a task that is already running here — skipped", "task_id", t.ID)
			continue
		}
		s.runs.Add(1)
		go func(t Task) {
			defer s.runs.Done()
			defer s.release(t.ID)
			s.execute(ctx, t)
		}(t)
	}
}

// claim records a task as in flight; false if it already was.
func (s *Scheduler) claim(taskID uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.inFlight[taskID]; busy {
		return false
	}
	s.inFlight[taskID] = uuid.Nil
	return true
}

// setRunID attaches the task_runs id once CreateRun has returned it.
func (s *Scheduler) setRunID(taskID, runID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.inFlight[taskID]; ok {
		s.inFlight[taskID] = runID
	}
}

func (s *Scheduler) release(taskID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, taskID)
}

// snapshotInFlight returns the in-flight task ids and the run ids known so
// far. Both slices are non-nil.
func (s *Scheduler) snapshotInFlight() (taskIDs, runIDs []uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	taskIDs = make([]uuid.UUID, 0, len(s.inFlight))
	runIDs = make([]uuid.UUID, 0, len(s.inFlight))
	for taskID, runID := range s.inFlight {
		taskIDs = append(taskIDs, taskID)
		if runID != uuid.Nil {
			runIDs = append(runIDs, runID)
		}
	}
	return taskIDs, runIDs
}

// drain waits for in-flight runs, whose contexts the caller's cancellation has
// already cancelled. Runs still going after drainTimeout are closed as failed
// so their rows don't read 'running' while nobody heartbeats them; one that
// finishes later overwrites that with its real result.
func (s *Scheduler) drain(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		s.runs.Wait()
		close(done)
	}()
	timer := time.NewTimer(s.drainTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
	}
	_, runIDs := s.snapshotInFlight()
	s.logger.Warn("scheduler: runs still in flight after drain timeout — closing them as interrupted",
		"runs", len(runIDs), "timeout", s.drainTimeout)
	for _, runID := range runIDs {
		recCtx, cancel := detached(ctx)
		if err := s.q.FinishRun(recCtx, runID, "failed", "", errMsgInterrupted); err != nil {
			s.logger.Warn("scheduler: close interrupted run failed", "run_id", runID, "err", err)
		}
		cancel()
	}
}

// detached returns a context that survives ctx's cancellation (keeping its
// values) but is bounded by recordTimeout. Result writes use it: they matter
// most exactly when the run was cut short by shutdown or a master handover.
func detached(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
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
		recCtx, cancel := detached(ctx)
		defer cancel()
		s.recordFailure(recCtx, t.ID, uuid.Nil, "", "unknown task type: "+t.Type)
		s.reportResult(recCtx, t, fmt.Errorf("unknown task type: %s", t.Type))
		return
	}

	runID, err := s.q.CreateRun(ctx, t.ID)
	if err != nil {
		logger.Warn("scheduler: create task_run failed", "err", err)
		return
	}
	s.setRunID(t.ID, runID)

	output, runErr := safeRun(ctx, handler, t.Config)
	// The handler ran on ctx; the result is written on a detached one, or a
	// run cut short by shutdown would leave its row 'running' forever.
	recCtx, cancel := detached(ctx)
	defer cancel()
	if runErr != nil {
		errMsg := runErr.Error()
		if ctx.Err() != nil && errors.Is(runErr, ctx.Err()) {
			errMsg = errMsgInterrupted
		}
		logger.Warn("scheduler: handler failed", "err", runErr)
		s.recordFailure(recCtx, t.ID, runID, output, errMsg)
		s.reportResult(recCtx, t, runErr)
		return
	}
	logger.Info("scheduler: handler succeeded", "output_bytes", len(output))
	s.recordSuccess(recCtx, t.ID, runID, output)
	s.reportResult(recCtx, t, nil)
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
