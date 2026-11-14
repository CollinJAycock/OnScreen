package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// A task whose period is shorter than its run must not be started again
// beside itself: while its run is in flight the scheduler passes its id to
// the lease as excluded (so next_run_at isn't advanced and the fire is taken
// once the run ends), and it never has two runs going.
func TestScheduler_RunThatOutlastsItsPeriodIsNotStartedTwice(t *testing.T) {
	task := Task{ID: uuid.New(), Type: "slow", CronExpr: "* * * * *"}
	q := &fakeQuerier{always: &task}

	var running, maxRunning, started atomic.Int32
	release := make(chan struct{})
	reg := NewRegistry()
	reg.Register("slow", HandlerFunc(func(context.Context, json.RawMessage) (string, error) {
		n := running.Add(1)
		for {
			m := maxRunning.Load()
			if n <= m || maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		started.Add(1)
		<-release
		running.Add(-1)
		return "", nil
	}))

	s := newScheduler(t, q, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	// Let many ticks pass while the first run is blocked.
	waitFor(t, func() bool { return started.Load() == 1 }, time.Second)
	waitFor(t, func() bool { return q.leaseCalled.Load() >= 10 }, time.Second)
	if got := maxRunning.Load(); got != 1 {
		t.Fatalf("task ran %d copies at once; want 1", got)
	}
	q.mu.Lock()
	last := q.excluded[len(q.excluded)-1]
	q.mu.Unlock()
	if !slices.Contains(last, task.ID) {
		t.Fatalf("in-flight task not excluded from the lease: %v", last)
	}

	// Once it finishes, the next tick may run it again.
	close(release)
	waitFor(t, func() bool { return started.Load() >= 2 }, time.Second)
	if got := maxRunning.Load(); got != 1 {
		t.Fatalf("task ran %d copies at once; want 1", got)
	}
}

// The ids of in-flight runs are heartbeated each tick and kept out of the
// stale sweep.
func TestScheduler_HeartbeatsInFlightRuns(t *testing.T) {
	task := Task{ID: uuid.New(), Type: "slow", CronExpr: "* * * * *"}
	q := &fakeQuerier{due: []Task{task}}
	release := make(chan struct{})
	defer close(release)
	reg := NewRegistry()
	reg.Register("slow", HandlerFunc(func(context.Context, json.RawMessage) (string, error) {
		<-release
		return "", nil
	}))

	s := newScheduler(t, q, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		if len(q.createdRuns) == 0 {
			return false
		}
		runID := q.createdRuns[0]
		return slices.Contains(q.heartbeats[len(q.heartbeats)-1], runID) &&
			slices.Contains(q.abandoned[len(q.abandoned)-1], runID)
	}, time.Second)
}

// The very first tick sweeps stale rows, so rows left 'running' by a crashed
// predecessor are closed at boot.
func TestScheduler_SweepsStaleRunsOnFirstTick(t *testing.T) {
	q := &fakeQuerier{}
	s := newScheduler(t, q, NewRegistry())
	s.interval = time.Hour // only the immediate first tick happens
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.abandoned) == 1
	}, time.Second)
}

// Shutdown (or losing the master lock) cancels the run; its result must still
// be written — on a context that isn't the cancelled one — and Run must not
// return before the run has.
func TestScheduler_CancelledRunIsRecordedAndWaitedFor(t *testing.T) {
	task := Task{ID: uuid.New(), Type: "honours_cancel", CronExpr: "* * * * *"}
	q := &fakeQuerier{due: []Task{task}}
	started := make(chan struct{})
	var handlerDone atomic.Bool
	reg := NewRegistry()
	reg.Register("honours_cancel", HandlerFunc(func(ctx context.Context, _ json.RawMessage) (string, error) {
		close(started)
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // cleanup after cancellation
		handlerDone.Store(true)
		return "partial", ctx.Err()
	}))

	s := newScheduler(t, q, reg)
	ctx, cancel := context.WithCancel(context.Background())
	runReturned := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(runReturned)
	}()
	<-started
	cancel()
	select {
	case <-runReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if !handlerDone.Load() {
		t.Fatal("Run returned before its in-flight run finished")
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.finished) != 1 {
		t.Fatalf("want 1 FinishRun, got %+v", q.finished)
	}
	f := q.finished[0]
	if f.ctxErr != nil {
		t.Fatalf("result written on a cancelled context: %v", f.ctxErr)
	}
	if f.status != "failed" || f.errMsg != errMsgInterrupted || f.output != "partial" {
		t.Fatalf("unexpected finish: %+v", f)
	}
	if len(q.results) != 1 || q.results[0].status != "failed" {
		t.Fatalf("unexpected results: %+v", q.results)
	}
}

// A handler that ignores cancellation can't hold Run (and so shutdown or a
// master handover) past drainTimeout; its row is closed as interrupted, and
// its real result still lands when it eventually returns.
func TestScheduler_DrainTimeoutClosesStuckRuns(t *testing.T) {
	task := Task{ID: uuid.New(), Type: "stuck", CronExpr: "* * * * *"}
	q := &fakeQuerier{due: []Task{task}}
	started := make(chan struct{})
	release := make(chan struct{})
	reg := NewRegistry()
	reg.Register("stuck", HandlerFunc(func(context.Context, json.RawMessage) (string, error) {
		close(started)
		<-release
		return "late", nil
	}))

	s := newScheduler(t, q, reg)
	s.drainTimeout = 30 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	runReturned := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(runReturned)
	}()
	<-started
	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.createdRuns) == 1
	}, time.Second)
	cancel()
	select {
	case <-runReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the drain timeout")
	}

	q.mu.Lock()
	if len(q.finished) != 1 || q.finished[0].errMsg != errMsgInterrupted || q.finished[0].ctxErr != nil {
		q.mu.Unlock()
		t.Fatalf("stuck run not closed as interrupted: %+v", q.finished)
	}
	q.mu.Unlock()

	close(release)
	waitFor(t, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		return len(q.finished) == 2 && q.finished[1].status == "success"
	}, time.Second)
}

type fakeRetentionStore struct {
	failNotifications bool
	pruneArgs         []gen.PruneTaskRunsParams
	calls             []string
}

func (f *fakeRetentionStore) DeleteExpiredSessions(context.Context) (int64, error) {
	f.calls = append(f.calls, "sessions")
	return 3, nil
}
func (f *fakeRetentionStore) DeleteExpiredPasswordResetTokens(context.Context) (int64, error) {
	f.calls = append(f.calls, "password_reset_tokens")
	return 1, nil
}
func (f *fakeRetentionStore) DeleteOldNotifications(context.Context) (int64, error) {
	f.calls = append(f.calls, "notifications")
	if f.failNotifications {
		return 0, errors.New("db down")
	}
	return 7, nil
}
func (f *fakeRetentionStore) PruneTaskRuns(_ context.Context, arg gen.PruneTaskRunsParams) (int64, error) {
	f.calls = append(f.calls, "task_runs")
	f.pruneArgs = append(f.pruneArgs, arg)
	return 1200, nil
}

func TestRetentionHandler_RunsEveryPurge(t *testing.T) {
	store := &fakeRetentionStore{}
	out, err := NewRetentionHandler(store).Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "deleted sessions=3 password_reset_tokens=1 notifications=7 task_runs=1200"
	if out != want {
		t.Fatalf("output %q, want %q", out, want)
	}
	if len(store.pruneArgs) != 1 || store.pruneArgs[0].KeepPerTask != TaskRunKeepPerTask || store.pruneArgs[0].KeepDays != TaskRunKeepDays {
		t.Fatalf("prune args: %+v", store.pruneArgs)
	}
}

// One failing purge doesn't stop the others, and fails the run by name.
func TestRetentionHandler_FailureDoesNotSkipLaterSteps(t *testing.T) {
	store := &fakeRetentionStore{failNotifications: true}
	out, err := NewRetentionHandler(store).Run(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "purge notifications") {
		t.Fatalf("want a notifications error, got %v", err)
	}
	if !slices.Equal(store.calls, []string{"sessions", "password_reset_tokens", "notifications", "task_runs"}) {
		t.Fatalf("calls: %v", store.calls)
	}
	if !strings.Contains(out, "notifications=error") || !strings.Contains(out, "task_runs=1200") {
		t.Fatalf("output %q", out)
	}
}
