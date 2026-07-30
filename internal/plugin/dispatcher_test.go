package plugin

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// stubMCP wraps a stateless Streamable-HTTP MCP server with a configurable
// `notify` tool. Created per-test; the test gets a baseURL it can paste into
// a Plugin record.
type stubMCP struct {
	server       *httptest.Server
	calls        atomic.Int64
	registerTool bool
	failTool     atomic.Bool
	gotEvents    chan NotificationEvent
}

func newStubMCP(t *testing.T, registerTool bool) *stubMCP {
	t.Helper()
	s := &stubMCP{registerTool: registerTool, gotEvents: make(chan NotificationEvent, 16)}

	mcpSrv := mcpserver.NewMCPServer("stub", "1.0.0", mcpserver.WithToolCapabilities(false))
	if registerTool {
		tool := mcp.NewTool(NotifyToolName,
			mcp.WithDescription("test notify"),
			mcp.WithString("event", mcp.Required()),
		)
		mcpSrv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			s.calls.Add(1)
			if s.failTool.Load() {
				return nil, errors.New("synthetic plugin error")
			}
			var evt NotificationEvent
			_ = req.BindArguments(&evt)
			select {
			case s.gotEvents <- evt:
			default:
			}
			return mcp.NewToolResultText("ok"), nil
		})
	}

	httpSrv := mcpserver.NewStreamableHTTPServer(mcpSrv, mcpserver.WithStateLess(true))
	s.server = httptest.NewServer(httpSrv)
	t.Cleanup(s.server.Close)
	return s
}

func (s *stubMCP) endpoint() string { return s.server.URL + "/mcp" }

// ---- in-memory audit + slog helpers --------------------------------------

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// ---- tests ---------------------------------------------------------------

func TestDispatcher_DeliversToPlugin(t *testing.T) {
	allowPrivateIPs(t)
	stub := newStubMCP(t, true)

	db := newMockRegistryDB()
	reg := NewRegistry(db)
	if _, err := reg.Create(context.Background(), CreateInput{
		Name: "stub", Role: RoleNotification,
		EndpointURL: stub.endpoint(), Enabled: true,
	}); err != nil {
		t.Fatalf("create plugin: %v", err)
	}

	d := NewNotificationDispatcher(reg, discardLogger(), nil)
	defer d.Close()

	d.Dispatch(NotificationEvent{Event: "media.play", Title: "hi"})

	select {
	case got := <-stub.gotEvents:
		if got.Event != "media.play" {
			t.Errorf("expected event media.play, got %q", got.Event)
		}
		if got.CorrelationID == "" {
			t.Errorf("dispatcher should auto-fill CorrelationID")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("plugin never received event")
	}
}

func TestDispatcher_MissingNotifyTool_NoBreaker(t *testing.T) {
	allowPrivateIPs(t)
	stub := newStubMCP(t, false) // no notify tool registered

	db := newMockRegistryDB()
	reg := NewRegistry(db)
	if _, err := reg.Create(context.Background(), CreateInput{
		Name: "stub", Role: RoleNotification,
		EndpointURL: stub.endpoint(), Enabled: true,
	}); err != nil {
		t.Fatalf("create plugin: %v", err)
	}

	d := NewNotificationDispatcher(reg, discardLogger(), nil)
	defer d.Close()

	for i := 0; i < breakerThreshold+1; i++ {
		d.Dispatch(NotificationEvent{Event: "media.play"})
	}

	// Allow workers to drain.
	waitForDrain(t, d)

	d.mu.Lock()
	defer d.mu.Unlock()
	for _, w := range d.workers {
		if !w.openUntil.IsZero() {
			t.Errorf("breaker tripped on tool-not-advertised — should stay closed")
		}
		if w.consecFails != 0 {
			t.Errorf("consecFails should stay at 0 for tool-not-advertised, got %d", w.consecFails)
		}
	}
}

func TestDispatcher_BreakerTripsAfterConsecutiveFailures(t *testing.T) {
	allowPrivateIPs(t)
	stub := newStubMCP(t, true)
	stub.failTool.Store(true) // every call returns an error

	db := newMockRegistryDB()
	reg := NewRegistry(db)
	if _, err := reg.Create(context.Background(), CreateInput{
		Name: "stub", Role: RoleNotification,
		EndpointURL: stub.endpoint(), Enabled: true,
	}); err != nil {
		t.Fatalf("create plugin: %v", err)
	}

	d := NewNotificationDispatcher(reg, discardLogger(), nil)
	defer d.Close()

	for i := 0; i < breakerThreshold; i++ {
		d.Dispatch(NotificationEvent{Event: "media.play"})
	}
	waitForDrain(t, d)

	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(d.workers))
	}
	for _, w := range d.workers {
		if w.openUntil.IsZero() {
			t.Errorf("expected breaker open, got zero")
		}
		if w.consecFails < breakerThreshold {
			t.Errorf("expected consecFails >= %d, got %d", breakerThreshold, w.consecFails)
		}
	}
}

func TestDispatcher_QueueFull_DropsEvents(t *testing.T) {
	allowPrivateIPs(t)
	stub := newStubMCP(t, true)

	db := newMockRegistryDB()
	reg := NewRegistry(db)
	created, err := reg.Create(context.Background(), CreateInput{
		Name: "stub", Role: RoleNotification,
		EndpointURL: stub.endpoint(), Enabled: true,
	})
	if err != nil {
		t.Fatalf("create plugin: %v", err)
	}

	d := NewNotificationDispatcher(reg, discardLogger(), nil)
	defer d.Close()

	// Build the worker, then block the consumer by holding its mutex while
	// we shove queueDepth+over events through enqueue. Whatever doesn't fit
	// gets dropped — never blocks.
	w, err := d.workerFor(created)
	if err != nil {
		t.Fatalf("worker: %v", err)
	}

	// Pin the worker's run goroutine off-CPU by issuing a slow call. Easier:
	// directly enqueue queueDepth + 5 without giving the consumer time. Use
	// a synthetic plugin worker with a stopped run loop.
	w.stop() // stop the goroutine so queue doesn't drain
	time.Sleep(20 * time.Millisecond)
	w.queue = make(chan NotificationEvent, 2)

	w.enqueue(NotificationEvent{Event: "a"})
	w.enqueue(NotificationEvent{Event: "b"})
	// Third must drop (capacity is 2).
	dropped := false
	done := make(chan struct{})
	go func() {
		w.enqueue(NotificationEvent{Event: "c"})
		close(done)
	}()
	select {
	case <-done:
		dropped = true // returned without blocking
	case <-time.After(500 * time.Millisecond):
		t.Fatal("enqueue blocked when queue was full")
	}
	if !dropped {
		t.Fatal("third event should have been dropped")
	}
}

func TestDispatcher_RecreatesWorkerOnEndpointChange(t *testing.T) {
	allowPrivateIPs(t)
	stub1 := newStubMCP(t, true)
	stub2 := newStubMCP(t, true)

	db := newMockRegistryDB()
	reg := NewRegistry(db)
	created, err := reg.Create(context.Background(), CreateInput{
		Name: "stub", Role: RoleNotification,
		EndpointURL: stub1.endpoint(), Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	d := NewNotificationDispatcher(reg, discardLogger(), nil)
	defer d.Close()

	d.Dispatch(NotificationEvent{Event: "first"})
	select {
	case <-stub1.gotEvents:
	case <-time.After(3 * time.Second):
		t.Fatal("first event never delivered")
	}

	if _, err := reg.Update(context.Background(), created.ID, UpdateInput{
		Name: "stub", EndpointURL: stub2.endpoint(), Enabled: true,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	d.Dispatch(NotificationEvent{Event: "second"})
	select {
	case got := <-stub2.gotEvents:
		if got.Event != "second" {
			t.Errorf("got %q on stub2, want second", got.Event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second event never delivered to new endpoint")
	}
}

// waitForDrain blocks until every worker queue is empty for two consecutive
// 10ms ticks, or t.Fatal on timeout. Sufficient guard for tests that need
// to inspect post-deliver state.
func waitForDrain(t *testing.T, d *NotificationDispatcher) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		empty := true
		d.mu.Lock()
		for _, w := range d.workers {
			if len(w.queue) > 0 {
				empty = false
				break
			}
		}
		d.mu.Unlock()
		if empty {
			time.Sleep(50 * time.Millisecond) // let in-flight call resolve
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("queues did not drain within timeout")
}

// stop must not wait on the plugin client. pluginClient.close takes the same
// mutex callTool holds for the whole of a call, and workerFor calls stop while
// holding d.mu — the dispatcher-wide lock every Dispatch needs for every
// plugin. So a blocking stop meant one plugin being reconfigured mid-delivery
// stalled dispatch to all the others for up to defaultCallTimeout.
func TestPluginWorker_StopDoesNotWaitOnInFlightCall(t *testing.T) {
	allowPrivateIPs(t)
	stub := newStubMCP(t, true)
	pc, err := newPluginClient(Plugin{
		Name: "stub", Role: RoleNotification, EndpointURL: stub.endpoint(), Enabled: true,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	w := &pluginWorker{client: pc, stopCh: make(chan struct{}), logger: discardLogger()}

	// Hold the client mutex exactly as an in-flight callTool would.
	pc.mu.Lock()
	defer pc.mu.Unlock()

	done := make(chan struct{})
	go func() { w.stop(); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() blocked on a client with a call in flight; workerFor " +
			"calls it under d.mu, so this stalls dispatch to every other plugin")
	}
}

// The client must still be closed, just by run rather than by stop — otherwise
// the non-blocking stop would trade a stall for a leaked transport per worker.
func TestPluginWorker_RunClosesClientOnExit(t *testing.T) {
	allowPrivateIPs(t)
	stub := newStubMCP(t, true)
	pc, err := newPluginClient(Plugin{
		Name: "stub", Role: RoleNotification, EndpointURL: stub.endpoint(), Enabled: true,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	w := &pluginWorker{
		client: pc,
		queue:  make(chan NotificationEvent, 1),
		stopCh: make(chan struct{}),
		logger: discardLogger(),
	}

	exited := make(chan struct{})
	go func() { w.run(context.Background()); close(exited) }()

	w.stop()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not exit after stop")
	}

	pc.mu.Lock()
	closed := pc.closed
	pc.mu.Unlock()
	if !closed {
		t.Error("run exited without closing its client — the transport and its " +
			"connection pool leak for the life of the process")
	}
}

// Dispatch checks d.closed, then releases d.mu for the registry query and the
// per-plugin loop. Close sets d.workers = nil. A Dispatch that passed the check
// just before Close ran therefore reaches workerFor's insert against a nil map
// — "assignment to entry in nil map" — in whatever goroutine called Dispatch,
// which on the playback path is a request handler, not necessarily under a
// recover.
func TestDispatcher_WorkerForAfterCloseDoesNotPanic(t *testing.T) {
	allowPrivateIPs(t)
	stub := newStubMCP(t, true)
	db := newMockRegistryDB()
	reg := NewRegistry(db)
	created, err := reg.Create(context.Background(), CreateInput{
		Name: "stub", Role: RoleNotification,
		EndpointURL: stub.endpoint(), Enabled: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	d := NewNotificationDispatcher(reg, discardLogger(), nil)
	d.Close() // nils the worker map

	// Exactly what an in-flight Dispatch does after Close lands.
	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("workerFor panicked after Close: %v", rec)
		}
	}()
	w, werr := d.workerFor(Plugin{
		ID: created.ID, Name: "stub", Role: RoleNotification,
		EndpointURL: stub.endpoint(), Enabled: true,
	})
	if werr == nil {
		t.Error("workerFor returned no error after Close; the event should be dropped")
	}
	if w != nil {
		t.Error("workerFor handed back a worker from a closed dispatcher")
	}
}

// Dispatch itself must stay safe when it races Close.
func TestDispatcher_DispatchAfterCloseIsSafe(t *testing.T) {
	allowPrivateIPs(t)
	stub := newStubMCP(t, true)
	db := newMockRegistryDB()
	reg := NewRegistry(db)
	if _, err := reg.Create(context.Background(), CreateInput{
		Name: "stub", Role: RoleNotification,
		EndpointURL: stub.endpoint(), Enabled: true,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	d := NewNotificationDispatcher(reg, discardLogger(), nil)
	d.Close()

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("Dispatch panicked after Close: %v", rec)
		}
	}()
	d.Dispatch(NotificationEvent{Event: "after-close"})
}
