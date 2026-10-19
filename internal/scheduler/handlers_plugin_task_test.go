package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakePluginRunner struct {
	got    uuid.UUID
	output string
	err    error
}

func (f *fakePluginRunner) RunTask(_ context.Context, id uuid.UUID) (string, error) {
	f.got = id
	return f.output, f.err
}

func TestPluginTaskHandler_RunsConfiguredPlugin(t *testing.T) {
	id := uuid.New()
	r := &fakePluginRunner{output: "ok"}
	out, err := NewPluginTaskHandler(r).Run(context.Background(),
		json.RawMessage(`{"plugin_id":"`+id.String()+`"}`))
	if err != nil || out != "ok" || r.got != id {
		t.Fatalf("out=%q err=%v got=%s", out, err, r.got)
	}
}

func TestPluginTaskHandler_BadConfig(t *testing.T) {
	r := &fakePluginRunner{}
	for _, cfg := range []string{``, `{}`, `{"plugin_id":"nope"}`, `[1]`} {
		if _, err := NewPluginTaskHandler(r).Run(context.Background(), json.RawMessage(cfg)); err == nil {
			t.Errorf("config %q: expected an error", cfg)
		}
	}
	if r.got != uuid.Nil {
		t.Error("runner called despite bad config")
	}
}

// TestScheduler_PluginTaskRecordsResult runs a plugin_task row through the
// real execute path: the plugin's output lands in the run row on success and
// its error in last_error on failure, like any other task.
func TestScheduler_PluginTaskRecordsResult(t *testing.T) {
	id := uuid.New()
	cfg := json.RawMessage(`{"plugin_id":"` + id.String() + `"}`)
	for _, tc := range []struct {
		name       string
		runner     *fakePluginRunner
		wantStatus string
		wantErr    string
		wantOutput string
	}{
		{"success", &fakePluginRunner{output: "synced 3 lists"}, "success", "", "synced 3 lists"},
		{"failure", &fakePluginRunner{err: errors.New(`plugin "x": quota exceeded`)}, "failed", `plugin "x": quota exceeded`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			reg.Register("plugin_task", NewPluginTaskHandler(tc.runner))
			taskID := uuid.New()
			q := &fakeQuerier{due: []Task{{
				ID: taskID, Name: "Plugin: x", Type: "plugin_task", Config: cfg,
				CronExpr: "30 4 * * *", Enabled: true,
			}}}
			newScheduler(t, q, reg).tick(context.Background())
			waitFor(t, func() bool {
				q.mu.Lock()
				defer q.mu.Unlock()
				return len(q.results) >= 1
			}, time.Second)

			q.mu.Lock()
			defer q.mu.Unlock()
			res := q.results[0]
			if res.taskID != taskID || res.status != tc.wantStatus || res.errMsg != tc.wantErr {
				t.Errorf("result = %+v, want status %q err %q", res, tc.wantStatus, tc.wantErr)
			}
			if len(q.finished) != 1 || q.finished[0].output != tc.wantOutput {
				t.Errorf("run rows = %+v, want output %q", q.finished, tc.wantOutput)
			}
			if tc.runner.got != id {
				t.Errorf("runner got plugin %s, want %s", tc.runner.got, id)
			}
		})
	}
}
