package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

type hookCall struct {
	task Task
	err  error
}

// TestOnResult_ReportsSuccessFailureAndUnknownType: the result hook (used by
// the notification agents' task_failed / backup_failed alerts) hears every
// finished run — nil on success, the handler's error on failure, and a
// failure for an unregistered task type.
func TestOnResult_ReportsSuccessFailureAndUnknownType(t *testing.T) {
	reg := NewRegistry()
	boom := errors.New("pg_dump failed")
	reg.Register("ok", HandlerFunc(func(context.Context, json.RawMessage) (string, error) { return "done", nil }))
	reg.Register("bad", HandlerFunc(func(context.Context, json.RawMessage) (string, error) { return "", boom }))

	var mu sync.Mutex
	var calls []hookCall
	s := New(&fakeQuerier{}, reg, discardLogger()).OnResult(func(_ context.Context, task Task, err error) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, hookCall{task, err})
	})

	okTask := Task{ID: uuid.New(), Name: "fine", Type: "ok"}
	badTask := Task{ID: uuid.New(), Name: "nightly", Type: "bad"}
	unknown := Task{ID: uuid.New(), Name: "ghost", Type: "nope"}
	s.execute(context.Background(), okTask)
	s.execute(context.Background(), badTask)
	s.execute(context.Background(), unknown)

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("hook calls = %d, want 3", len(calls))
	}
	if calls[0].task.ID != okTask.ID || calls[0].err != nil {
		t.Errorf("success call = %+v", calls[0])
	}
	if calls[1].task.ID != badTask.ID || !errors.Is(calls[1].err, boom) {
		t.Errorf("failure call = %+v", calls[1])
	}
	if calls[2].task.ID != unknown.ID || calls[2].err == nil {
		t.Errorf("unknown-type call = %+v", calls[2])
	}
}

// TestOnResult_PanickingHookDoesNotEscape: a broken hook must not take the
// scheduler's run goroutine down with it.
func TestOnResult_PanickingHookDoesNotEscape(t *testing.T) {
	reg := NewRegistry()
	reg.Register("ok", HandlerFunc(func(context.Context, json.RawMessage) (string, error) { return "", nil }))
	q := &fakeQuerier{}
	s := New(q, reg, discardLogger()).OnResult(func(context.Context, Task, error) { panic("hook") })
	s.execute(context.Background(), Task{ID: uuid.New(), Type: "ok"})
	if len(q.results) != 1 || q.results[0].status != "success" {
		t.Fatalf("run result not recorded before the hook: %+v", q.results)
	}
}
