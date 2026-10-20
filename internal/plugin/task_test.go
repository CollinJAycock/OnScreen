package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
)

type fakeTaskInfo struct {
	libs []TaskLibrary
	err  error
}

func (f fakeTaskInfo) ServerInfo() TaskServerInfo {
	return TaskServerInfo{Name: "OnScreen", Version: "2.7.0-test"}
}

func (f fakeTaskInfo) Libraries(context.Context) ([]TaskLibrary, error) { return f.libs, f.err }

func newTaskFixture(t *testing.T, stub *toolStub, enabled bool) (*TaskDispatcher, Plugin, *mockRegistryDB) {
	t.Helper()
	db := newMockRegistryDB()
	reg := NewRegistry(db)
	p, err := reg.Create(context.Background(), CreateInput{
		Name: "nightly", Role: RoleTask, EndpointURL: stub.endpoint(), Enabled: enabled,
	})
	if err != nil {
		t.Fatalf("create plugin: %v", err)
	}
	d := NewTaskDispatcher(reg, fakeTaskInfo{libs: []TaskLibrary{{ID: "l1", Name: "Movies", Type: "movie"}}},
		discardLogger(), nil)
	t.Cleanup(d.Close)
	return d, p, db
}

func TestTaskDispatcher_RunTaskReturnsOutputAndSendsContext(t *testing.T) {
	allowPrivateIPs(t)
	stub := newToolStub(t, RunTaskToolName, func(map[string]any) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("pruned 12 entries"), nil
	})
	d, p, _ := newTaskFixture(t, stub, true)

	out, err := d.RunTask(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if out != "pruned 12 entries" {
		t.Errorf("output = %q", out)
	}
	var sent TaskContext
	stub.args(t, &sent)
	if sent.PluginID != p.ID.String() || sent.CorrelationID == "" || sent.StartedAt == "" {
		t.Errorf("ids not sent: %+v", sent)
	}
	if sent.Server.Version != "2.7.0-test" || len(sent.Libraries) != 1 || sent.Libraries[0].Type != "movie" {
		t.Errorf("context not sent: %+v", sent)
	}
}

func TestTaskDispatcher_IsErrorFailsRunWithPluginText(t *testing.T) {
	allowPrivateIPs(t)
	stub := newToolStub(t, RunTaskToolName, func(map[string]any) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("upstream quota exceeded"), nil
	})
	d, p, _ := newTaskFixture(t, stub, true)

	_, err := d.RunTask(context.Background(), p.ID)
	if err == nil || !strings.Contains(err.Error(), "upstream quota exceeded") {
		t.Fatalf("expected the plugin's error text, got %v", err)
	}
	for _, e := range d.pool.entries {
		if fails, _ := e.breaker.state(); fails != 1 {
			t.Errorf("an isError run should count against the breaker, fails=%d", fails)
		}
	}
}

func TestTaskDispatcher_BreakerOpenFailsWithoutCalling(t *testing.T) {
	allowPrivateIPs(t)
	stub := newToolStub(t, RunTaskToolName, func(map[string]any) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("nope"), nil
	})
	d, p, _ := newTaskFixture(t, stub, true)
	for i := 0; i < breakerThreshold; i++ {
		_, _ = d.RunTask(context.Background(), p.ID)
	}
	before := stub.calls.Load()
	_, err := d.RunTask(context.Background(), p.ID)
	var open *BreakerOpenError
	if !errors.As(err, &open) {
		t.Fatalf("expected BreakerOpenError, got %v", err)
	}
	if stub.calls.Load() != before {
		t.Error("open breaker still called the plugin")
	}
}

func TestTaskDispatcher_DisabledPluginSkipped(t *testing.T) {
	allowPrivateIPs(t)
	stub := newToolStub(t, RunTaskToolName, func(map[string]any) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ran"), nil
	})
	d, p, _ := newTaskFixture(t, stub, false)
	out, err := d.RunTask(context.Background(), p.ID)
	if err != nil || !strings.HasPrefix(out, "skipped") {
		t.Fatalf("disabled plugin: out=%q err=%v", out, err)
	}
	if stub.calls.Load() != 0 {
		t.Error("disabled plugin was called")
	}
}

func TestTaskDispatcher_WrongRoleUnknownAndMissingTool(t *testing.T) {
	allowPrivateIPs(t)
	stub := newToolStub(t, "", nil)
	d, p, db := newTaskFixture(t, stub, true)

	if _, err := d.RunTask(context.Background(), uuid.New()); err == nil {
		t.Error("unknown plugin should fail the run")
	}

	_, err := d.RunTask(context.Background(), p.ID)
	var notAdvertised *ToolNotAdvertisedError
	if !errors.As(err, &notAdvertised) {
		t.Errorf("expected ToolNotAdvertisedError, got %v", err)
	}

	reg := NewRegistry(db)
	other, err := reg.Create(context.Background(), CreateInput{
		Name: "notify", Role: RoleNotification, EndpointURL: stub.endpoint(), Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.RunTask(context.Background(), other.ID); err == nil || !strings.Contains(err.Error(), "not task") {
		t.Errorf("notification plugin run as task: %v", err)
	}
}

func TestRegistry_TaskPluginOwnsScheduledTask(t *testing.T) {
	db := newMockRegistryDB()
	reg := NewRegistry(db)
	ctx := context.Background()

	task, err := reg.Create(ctx, CreateInput{Name: "Nightly", Role: RoleTask, EndpointURL: "https://p.example.com/mcp", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	row, ok := db.tasks[task.ID]
	if !ok {
		t.Fatal("task plugin got no scheduled task")
	}
	if row.Name != "Plugin: Nightly" || row.CronExpr != DefaultTaskCron || !row.Enabled {
		t.Errorf("task row = %+v", row)
	}
	if !row.NextRunAt.Valid || !row.NextRunAt.Time.After(time.Now()) {
		t.Errorf("next_run_at not in the future: %v", row.NextRunAt)
	}

	notify, err := reg.Create(ctx, CreateInput{Name: "N", Role: RoleNotification, EndpointURL: "https://p.example.com/mcp", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := db.tasks[notify.ID]; ok {
		t.Error("notification plugin got a scheduled task")
	}

	if err := reg.Delete(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := db.tasks[task.ID]; ok {
		t.Error("scheduled task survived its plugin")
	}
}

func TestRegistry_TaskCreateRolledBackWhenScheduleFails(t *testing.T) {
	db := newMockRegistryDB()
	db.ensureTaskErr = errors.New("db down")
	reg := NewRegistry(db)
	if _, err := reg.Create(context.Background(), CreateInput{Name: "T", Role: RoleTask, EndpointURL: "https://p.example.com/mcp", Enabled: true}); err == nil {
		t.Fatal("expected an error")
	}
	if len(db.rows) != 0 {
		t.Error("plugin row left behind without its task")
	}
}

func TestRegistry_SyncTasks(t *testing.T) {
	db := newMockRegistryDB()
	reg := NewRegistry(db)
	ctx := context.Background()

	// A task plugin from before v2.7 (no row) and an orphan row.
	p, err := reg.Create(ctx, CreateInput{Name: "Old", Role: RoleTask, EndpointURL: "https://p.example.com/mcp", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	delete(db.tasks, p.ID)
	orphan := uuid.New()
	db.tasks[orphan] = db.tasks[p.ID]

	if err := reg.SyncTasks(ctx); err != nil {
		t.Fatalf("SyncTasks: %v", err)
	}
	if _, ok := db.tasks[p.ID]; !ok {
		t.Error("SyncTasks did not create the missing task")
	}
	if _, ok := db.tasks[orphan]; ok {
		t.Error("SyncTasks left the orphan task")
	}
}
