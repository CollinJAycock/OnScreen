package audit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// fakeAuditDB captures the params passed to InsertAuditLog so tests can
// assert on the marshalled shape. Goroutine-safe because audit.Log spawns
// a goroutine and the test must wait for completion via the done channel.
type fakeAuditDB struct {
	mu     sync.Mutex
	calls  []gen.InsertAuditLogParams
	failWith error
	done   chan struct{}
}

func newFakeAuditDB() *fakeAuditDB {
	return &fakeAuditDB{done: make(chan struct{}, 16)}
}

func (f *fakeAuditDB) InsertAuditLog(_ context.Context, arg gen.InsertAuditLogParams) error {
	f.mu.Lock()
	f.calls = append(f.calls, arg)
	err := f.failWith
	f.mu.Unlock()
	f.done <- struct{}{}
	return err
}

func (f *fakeAuditDB) wait(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for audit insert")
	}
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestLog_PersistsAllFields(t *testing.T) {
	db := newFakeAuditDB()
	l := New(db, newTestLogger())

	uid := uuid.New()
	l.Log(context.Background(), &uid, ActionLoginSuccess, "192.0.2.1",
		map[string]any{"username": "alice", "method": "password"}, "203.0.113.5")
	db.wait(t)

	if len(db.calls) != 1 {
		t.Fatalf("want 1 insert, got %d", len(db.calls))
	}
	c := db.calls[0]
	if c.Action != ActionLoginSuccess {
		t.Errorf("Action: got %q, want %q", c.Action, ActionLoginSuccess)
	}
	if c.Target == nil || *c.Target != "192.0.2.1" {
		t.Errorf("Target: got %v, want %q", c.Target, "192.0.2.1")
	}
	if !c.UserID.Valid || uuid.UUID(c.UserID.Bytes) != uid {
		t.Errorf("UserID: got %v, want valid=%v", c.UserID, uid)
	}
	if c.IpAddr == nil || c.IpAddr.String() != "203.0.113.5" {
		t.Errorf("IpAddr: got %v, want 203.0.113.5", c.IpAddr)
	}

	var detail map[string]any
	if err := json.Unmarshal(c.Detail, &detail); err != nil {
		t.Fatalf("Detail not valid JSON: %v", err)
	}
	if detail["username"] != "alice" || detail["method"] != "password" {
		t.Errorf("Detail roundtrip: %v", detail)
	}
}

func TestLog_NilDetailLeavesDetailNil(t *testing.T) {
	db := newFakeAuditDB()
	l := New(db, newTestLogger())
	l.Log(context.Background(), nil, ActionLoginFailed, "alice", nil, "")
	db.wait(t)

	if got := db.calls[0].Detail; got != nil {
		t.Errorf("Detail: got %s, want nil", got)
	}
}

func TestLog_EmptyTargetSetsNil(t *testing.T) {
	db := newFakeAuditDB()
	l := New(db, newTestLogger())
	l.Log(context.Background(), nil, ActionSettingsUpdate, "", nil, "")
	db.wait(t)

	if got := db.calls[0].Target; got != nil {
		t.Errorf("Target: got %v, want nil", got)
	}
}

func TestLog_NilUserIDSetsInvalidUUID(t *testing.T) {
	db := newFakeAuditDB()
	l := New(db, newTestLogger())
	l.Log(context.Background(), nil, ActionLoginFailed, "alice", nil, "")
	db.wait(t)

	if db.calls[0].UserID.Valid {
		t.Errorf("UserID should be invalid (NULL) when nil userID passed")
	}
}

func TestLog_InvalidIPLeavesNil(t *testing.T) {
	db := newFakeAuditDB()
	l := New(db, newTestLogger())
	// "not-an-ip" or a remote-addr-style "host:port" won't ParseAddr — Logger
	// should silently drop the IP rather than reject the whole entry.
	l.Log(context.Background(), nil, ActionLoginFailed, "alice", nil, "203.0.113.5:55021")
	db.wait(t)

	if db.calls[0].IpAddr != nil {
		t.Errorf("IpAddr: got %v, want nil for unparseable address", db.calls[0].IpAddr)
	}
}

func TestLog_DBErrorIsSwallowed(t *testing.T) {
	// Audit is fire-and-forget — Log returns void. A failing DB write must
	// not panic the calling goroutine; it should be logged and dropped.
	db := newFakeAuditDB()
	db.failWith = errors.New("boom")
	l := New(db, newTestLogger())

	l.Log(context.Background(), nil, ActionLoginFailed, "alice", nil, "")
	db.wait(t)
	// If we reach this line without panic, the contract holds.
}

func TestLog_UsesBackgroundContextForWrite(t *testing.T) {
	// Even if the caller's request context is cancelled immediately, the
	// audit insert must complete using context.WithoutCancel-derived ctx.
	db := newFakeAuditDB()
	l := New(db, newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l.Log(ctx, nil, ActionLoginFailed, "alice", nil, "")
	db.wait(t)

	if len(db.calls) != 1 {
		t.Errorf("expected insert despite cancelled ctx, got %d calls", len(db.calls))
	}
}

func TestLog_UnmarshallableDetailDropsDetail(t *testing.T) {
	// json.Marshal will fail on cycles or non-encodable values like channels.
	// Logger should warn + drop detail, not abort the write.
	db := newFakeAuditDB()
	l := New(db, newTestLogger())
	bad := map[string]any{"ch": make(chan int)}
	l.Log(context.Background(), nil, ActionLoginFailed, "alice", bad, "")
	db.wait(t)

	if got := db.calls[0].Detail; got != nil {
		t.Errorf("Detail: got %s, want nil after marshal failure", got)
	}
	if db.calls[0].Action != ActionLoginFailed {
		t.Errorf("Action should still be written when detail marshal fails")
	}
}

func TestClientIP_PrefersXForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.5")
	if got := ClientIP(r); got != "203.0.113.5" {
		t.Errorf("ClientIP: got %q, want 203.0.113.5", got)
	}
}

func TestClientIP_FallsBackToRemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	if got := ClientIP(r); got != "10.0.0.1:1234" {
		t.Errorf("ClientIP: got %q, want 10.0.0.1:1234", got)
	}
}
