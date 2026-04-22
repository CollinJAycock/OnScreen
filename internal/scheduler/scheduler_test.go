package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeQuerier is an in-memory Querier for tests. The lease method returns
// whatever tasks the test pre-loaded into `due`, calling nextRun on each so
// the test can verify the cron-advance contract.
type fakeQuerier struct {
	mu sync.Mutex

	due []Task

	createdRuns []uuid.UUID
	finished    []finishCall
	results     []resultCall

	createRunErr  error
	finishRunErr  error
	recordErr     error
	leaseErr      error
	leaseCalled   atomic.Int32
	nextRunCalled []Task
}

type finishCall struct {
	runID  uuid.UUID
	status string
	output string
	errMsg string
}

type resultCall struct {
	taskID uuid.UUID
	status string
	errMsg string
}

func (f *fakeQuerier) LeaseDueTasks(ctx context.Context, limit int, nextRun func(Task) (time.Time, error)) ([]Task, error) {
	f.leaseCalled.Add(1)
	if f.leaseErr != nil {
		return nil, f.leaseErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.due
	f.due = nil
	for _, t := range out {
		if _, err := nextRun(t); err != nil {
			return nil, err
		}
		f.nextRunCalled = append(f.nextRunCalled, t)
	}
	return out, nil
}

func (f *fakeQuerier) CreateRun(ctx context.Context, taskID uuid.UUID) (uuid.UUID, error) {
	if f.createRunErr != nil {
		return uuid.Nil, f.createRunErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	f.createdRuns = append(f.createdRuns, id)
	return id, nil
}

func (f *fakeQuerier) FinishRun(ctx context.Context, runID uuid.UUID, status, output, errMsg string) error {
	if f.finishRunErr != nil {
		return f.finishRunErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished = append(f.finished, finishCall{runID, status, output, errMsg})
	return nil
}

func (f *fakeQuerier) RecordResult(ctx context.Context, taskID uuid.UUID, status, errMsg string) error {
	if f.recordErr != nil {
		return f.recordErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, resultCall{taskID, status, errMsg})
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newScheduler(t *testing.T, q Querier, reg *Registry) *Scheduler {
	t.Helper()
	s := New(q, reg, discardLogger())
	s.interval = 10 * time.Millisecond
	return s
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("missing"); ok {
		t.Fatal("expected missing handler")
	}
	called := false
	r.Register("x", HandlerFunc(func(context.Context, json.RawMessage) (string, error) {
		called = true
		return "ok", nil
	}))
	h, ok := r.Get("x")
	if !ok {
		t.Fatal("expected x to be registered")
	}
	if _, err := h.Run(context.Background(), nil); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !called {
		t.Fatal("handler not invoked")
	}
	if got := r.Types(); len(got) != 1 || got[0] != "x" {
		t.Fatalf("Types: %v", got)
	}

	// Re-registering replaces the entry.
	r.Register("x", HandlerFunc(func(context.Context, json.RawMessage) (string, error) {
		return "replaced", nil
	}))
	h, _ = r.Get("x")
	out, _ := h.Run(context.Background(), nil)
	if out != "replaced" {
		t.Fatalf("expected replacement handler, got %q", out)
	}
}

func TestNextRun(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	next, err := NextRun("0 * * * *", now)
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	if !next.Equal(time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected next: %v", next)
	}
	if _, err := NextRun("not-a-cron", now); err == nil {
		t.Fatal("expected error for bad expression")
	}
}

func TestSchedulerExecutesHandlerAndRecordsResults(t *testing.T) {
	taskID := uuid.New()
	q := &fakeQuerier{
		due: []Task{{
			ID:       taskID,
			Name:     "nightly",
			Type:     "test_ok",
			Config:   json.RawMessage(`{"k":"v"}`),
			CronExpr: "0 0 * * *",
		}},
	}
	reg := NewRegistry()

	var seenCfg json.RawMessage
	reg.Register("test_ok", HandlerFunc(func(_ context.Context, cfg json.RawMessage) (string, error) {
		seenCfg = cfg
		return "produced output", nil
	}))

	s := newScheduler(t, q, reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.results) >= 1
	}, time.Second)
	cancel()

	q.mu.Lock()
	defer q.mu.Unlock()
	if string(seenCfg) != `{"k":"v"}` {
		t.Fatalf("handler did not receive config; got %q", string(seenCfg))
	}
	if len(q.createdRuns) != 1 {
		t.Fatalf("expected 1 created run, got %d", len(q.createdRuns))
	}
	if len(q.finished) != 1 || q.finished[0].status != "success" || q.finished[0].output != "produced output" {
		t.Fatalf("unexpected finish call: %+v", q.finished)
	}
	if len(q.results) != 1 || q.results[0].status != "success" || q.results[0].taskID != taskID {
		t.Fatalf("unexpected result: %+v", q.results)
	}
	if len(q.nextRunCalled) != 1 {
		t.Fatalf("expected nextRun callback invoked once, got %d", len(q.nextRunCalled))
	}
}

func TestSchedulerHandlerErrorRecordsFailure(t *testing.T) {
	q := &fakeQuerier{
		due: []Task{{
			ID:       uuid.New(),
			Type:     "test_err",
			CronExpr: "* * * * *",
		}},
	}
	reg := NewRegistry()
	reg.Register("test_err", HandlerFunc(func(context.Context, json.RawMessage) (string, error) {
		return "", errors.New("boom")
	}))

	s := newScheduler(t, q, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.results) >= 1
	}, time.Second)
	cancel()

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.results[0].status != "failed" || q.results[0].errMsg != "boom" {
		t.Fatalf("unexpected result: %+v", q.results[0])
	}
	if q.finished[0].status != "failed" || q.finished[0].errMsg != "boom" {
		t.Fatalf("unexpected finish: %+v", q.finished[0])
	}
}

func TestSchedulerHandlerPanicIsRecovered(t *testing.T) {
	q := &fakeQuerier{
		due: []Task{{
			ID:       uuid.New(),
			Type:     "test_panic",
			CronExpr: "* * * * *",
		}},
	}
	reg := NewRegistry()
	reg.Register("test_panic", HandlerFunc(func(context.Context, json.RawMessage) (string, error) {
		panic("kaboom")
	}))

	s := newScheduler(t, q, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.results) >= 1
	}, time.Second)
	cancel()

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.results[0].status != "failed" {
		t.Fatalf("expected failed status after panic, got %q", q.results[0].status)
	}
	if q.results[0].errMsg == "" {
		t.Fatalf("expected non-empty error message after panic")
	}
}

func TestSchedulerUnknownTaskTypeRecordsFailure(t *testing.T) {
	q := &fakeQuerier{
		due: []Task{{
			ID:       uuid.New(),
			Type:     "no_such_handler",
			CronExpr: "* * * * *",
		}},
	}
	reg := NewRegistry()
	s := newScheduler(t, q, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.results) >= 1
	}, time.Second)
	cancel()

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.results[0].status != "failed" {
		t.Fatalf("expected failed for unknown task type, got %+v", q.results[0])
	}
	if len(q.createdRuns) != 0 {
		t.Fatalf("expected no run created for unknown task type, got %d", len(q.createdRuns))
	}
}

func TestSchedulerLeaseErrorDoesNotKillLoop(t *testing.T) {
	q := &fakeQuerier{leaseErr: errors.New("db down")}
	reg := NewRegistry()
	s := newScheduler(t, q, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	// The loop should keep ticking despite lease errors. Wait for >=2 calls.
	waitFor(t, func() bool { return q.leaseCalled.Load() >= 2 }, time.Second)
}

func TestSchedulerNextRunForBadCronQuarantines(t *testing.T) {
	s := newScheduler(t, &fakeQuerier{}, NewRegistry())
	t0 := time.Now()
	next, err := s.nextRunFor(Task{CronExpr: "this is not cron"})
	if err != nil {
		t.Fatalf("expected no error (quarantine path), got %v", err)
	}
	// Quarantine pushes ~100 years into the future.
	if next.Sub(t0) < 50*365*24*time.Hour {
		t.Fatalf("expected far-future quarantine, got %v", next)
	}
}

// CreateRun failing must not invoke the handler and must not leave any
// run/result rows — the task is effectively skipped for this tick and will
// reappear at next_run_at.
func TestSchedulerCreateRunErrorSkipsExecution(t *testing.T) {
	q := &fakeQuerier{
		due:          []Task{{ID: uuid.New(), Type: "ok", CronExpr: "* * * * *"}},
		createRunErr: errors.New("create fail"),
	}
	reg := NewRegistry()
	var called atomic.Int32
	reg.Register("ok", HandlerFunc(func(context.Context, json.RawMessage) (string, error) {
		called.Add(1)
		return "", nil
	}))

	s := newScheduler(t, q, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	// We can't "wait for nothing," so give the loop enough ticks to have
	// run execute() at least once, then assert negatives.
	waitFor(t, func() bool { return q.leaseCalled.Load() >= 2 }, time.Second)
	cancel()
	// Give goroutines a moment to settle after cancel.
	time.Sleep(20 * time.Millisecond)

	q.mu.Lock()
	defer q.mu.Unlock()
	if called.Load() != 0 {
		t.Fatalf("handler should not run when CreateRun fails, called=%d", called.Load())
	}
	if len(q.finished) != 0 {
		t.Fatalf("no FinishRun when CreateRun failed, got %d", len(q.finished))
	}
	if len(q.results) != 0 {
		t.Fatalf("no RecordResult when CreateRun failed, got %d", len(q.results))
	}
}

// FinishRun returning an error is logged but must not prevent RecordResult.
func TestSchedulerFinishRunErrorStillRecordsResult(t *testing.T) {
	q := &fakeQuerier{
		due:          []Task{{ID: uuid.New(), Type: "ok", CronExpr: "* * * * *"}},
		finishRunErr: errors.New("finish fail"),
	}
	reg := NewRegistry()
	reg.Register("ok", HandlerFunc(func(context.Context, json.RawMessage) (string, error) {
		return "out", nil
	}))

	s := newScheduler(t, q, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.results) >= 1
	}, time.Second)
	cancel()

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.results[0].status != "success" {
		t.Fatalf("expected success result even when FinishRun fails, got %+v", q.results[0])
	}
}

// FinishRun failing during the failure-recording path must also be logged
// but still let RecordResult run.
func TestSchedulerFinishRunErrorOnFailurePath(t *testing.T) {
	q := &fakeQuerier{
		due:          []Task{{ID: uuid.New(), Type: "err", CronExpr: "* * * * *"}},
		finishRunErr: errors.New("finish fail"),
	}
	reg := NewRegistry()
	reg.Register("err", HandlerFunc(func(context.Context, json.RawMessage) (string, error) {
		return "", errors.New("handler boom")
	}))

	s := newScheduler(t, q, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.results) >= 1
	}, time.Second)

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.results[0].status != "failed" {
		t.Fatalf("expected failed result: %+v", q.results[0])
	}
}

// RecordResult returning an error is logged but must not panic.
func TestSchedulerRecordResultErrorIsLogged(t *testing.T) {
	q := &fakeQuerier{
		due:       []Task{{ID: uuid.New(), Type: "ok", CronExpr: "* * * * *"}},
		recordErr: errors.New("record fail"),
	}
	reg := NewRegistry()
	reg.Register("ok", HandlerFunc(func(context.Context, json.RawMessage) (string, error) {
		return "", nil
	}))

	s := newScheduler(t, q, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	// The handler ran (FinishRun got called) even though RecordResult errored.
	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.finished) >= 1
	}, time.Second)
}

// Unknown task type must record failure WITHOUT creating a task_run row —
// there is no run to finish, and we don't want to pollute run history with
// handler-never-called rows.
func TestSchedulerUnknownTypeDoesNotCreateRun(t *testing.T) {
	q := &fakeQuerier{
		due: []Task{{ID: uuid.New(), Type: "nope", CronExpr: "* * * * *"}},
	}
	s := newScheduler(t, q, NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.results) >= 1
	}, time.Second)
	cancel()

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.createdRuns) != 0 {
		t.Fatalf("unknown type should not CreateRun; got %d", len(q.createdRuns))
	}
	if len(q.finished) != 0 {
		t.Fatalf("unknown type should not FinishRun; got %d", len(q.finished))
	}
	if q.results[0].errMsg == "" || !strings.Contains(q.results[0].errMsg, "unknown task type") {
		t.Fatalf("expected unknown-type error, got %q", q.results[0].errMsg)
	}
}

// Multiple due tasks in one tick must dispatch in parallel goroutines, not
// serially. We verify by blocking all handlers on a single channel: if
// dispatch were serial, only the first would start and the test would time
// out.
func TestSchedulerDispatchesTasksInParallel(t *testing.T) {
	const n = 4
	tasks := make([]Task, n)
	for i := range tasks {
		tasks[i] = Task{ID: uuid.New(), Type: "wait", CronExpr: "* * * * *"}
	}
	q := &fakeQuerier{due: tasks}

	reg := NewRegistry()
	started := make(chan struct{}, n)
	release := make(chan struct{})
	reg.Register("wait", HandlerFunc(func(ctx context.Context, _ json.RawMessage) (string, error) {
		started <- struct{}{}
		<-release
		return "", nil
	}))

	s := newScheduler(t, q, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	for i := 0; i < n; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("only %d of %d handlers started — dispatch is not parallel", i, n)
		}
	}
	close(release)
}

// Lease passes the configured batch size through verbatim so operators can
// tune throughput.
func TestSchedulerPassesBatchSizeToLease(t *testing.T) {
	q := &captureLimitQuerier{}
	s := newScheduler(t, q, NewRegistry())
	s.batchSize = 7
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	waitFor(t, func() bool { return q.limit.Load() == 7 }, time.Second)
}

type captureLimitQuerier struct {
	limit atomic.Int32
}

func (c *captureLimitQuerier) LeaseDueTasks(_ context.Context, limit int, _ func(Task) (time.Time, error)) ([]Task, error) {
	c.limit.Store(int32(limit))
	return nil, nil
}
func (*captureLimitQuerier) CreateRun(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}
func (*captureLimitQuerier) FinishRun(context.Context, uuid.UUID, string, string, string) error {
	return nil
}
func (*captureLimitQuerier) RecordResult(context.Context, uuid.UUID, string, string) error {
	return nil
}

// NextRun must be strictly after `after` — used at lease time to ensure a
// just-fired task doesn't immediately re-lease.
func TestNextRunStrictlyAfter(t *testing.T) {
	// 12:00:00 exactly — an hourly cron "0 * * * *" should advance to 13:00,
	// not to 12:00.
	noon := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	next, err := NextRun("0 * * * *", noon)
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	if !next.After(noon) {
		t.Fatalf("expected strictly after, got %v (input %v)", next, noon)
	}
}
