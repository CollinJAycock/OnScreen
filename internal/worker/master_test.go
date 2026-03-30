package worker

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/valkey"
)

// testInterval is the tick rate used in all tests so they don't have to wait
// the production 5 s refresh cycle.
const testInterval = 30 * time.Millisecond

func newTestLock(v *valkey.Client, id string) *MasterLock {
	m := NewMasterLock(v, id, slog.Default())
	m.refreshInterval = testInterval
	m.checkInterval = testInterval
	return m
}

// awaitMaster polls until IsMaster() == want or the timeout elapses.
func awaitMaster(t *testing.T, m *MasterLock, want bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.IsMaster() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("IsMaster() = %v after %v, want %v", m.IsMaster(), timeout, want)
}

// TestMasterLock_SingleInstance verifies a lone instance acquires the lock.
func TestMasterLock_SingleInstance(t *testing.T) {
	v := testvalkey.New(t)
	m := newTestLock(v, "a")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go m.Run(ctx)

	awaitMaster(t, m, true, time.Second)
}

// TestMasterLock_OnlyOneMaster verifies two concurrent instances do not both
// hold the lock at the same time.
func TestMasterLock_OnlyOneMaster(t *testing.T) {
	v := testvalkey.New(t)
	m1 := newTestLock(v, "a")
	m2 := newTestLock(v, "b")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go m1.Run(ctx)
	go m2.Run(ctx)

	// Allow both instances to stabilise.
	time.Sleep(200 * time.Millisecond)

	if m1.IsMaster() && m2.IsMaster() {
		t.Error("both instances report IsMaster — split-brain detected")
	}
	if !m1.IsMaster() && !m2.IsMaster() {
		t.Error("neither instance is master")
	}
}

// TestMasterLock_CleanFailover verifies that cancelling the master's context
// releases the lock immediately so the standby takes over without waiting for
// the TTL to expire.
func TestMasterLock_CleanFailover(t *testing.T) {
	v := testvalkey.New(t)
	m1 := newTestLock(v, "a")
	m2 := newTestLock(v, "b")

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()

	// m1 starts first and claims the lock.
	go m1.Run(ctx1)
	awaitMaster(t, m1, true, time.Second)

	// m2 starts but stays standby while m1 holds the lock.
	go m2.Run(ctx2)
	time.Sleep(100 * time.Millisecond)
	if m2.IsMaster() {
		t.Fatal("m2 should not be master while m1 holds the lock")
	}

	// Shut m1 down cleanly — it deletes the key immediately.
	cancel1()
	time.Sleep(50 * time.Millisecond) // let the goroutine exit

	// m2 should take over well within one refresh cycle.
	awaitMaster(t, m2, true, 500*time.Millisecond)
	if m1.IsMaster() {
		t.Error("m1 still reports IsMaster after context cancellation")
	}
}

// TestMasterLock_LockLossDetected verifies that deleting the Valkey key (which
// simulates TTL expiry or an external eviction) causes the holder to detect the
// loss on the next refresh tick and clear IsMaster — at least transiently.
// We continuously evict the key to prevent immediate re-acquisition so we can
// assert the lost state without a tight timing window.
func TestMasterLock_LockLossDetected(t *testing.T) {
	v := testvalkey.New(t)
	m := newTestLock(v, "a")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go m.Run(ctx)
	awaitMaster(t, m, true, time.Second)

	// Keep the key deleted to simulate a persistent TTL expiry — prevents
	// re-acquisition so we can assert the lost state without a race window.
	evictCtx, stopEvict := context.WithCancel(context.Background())
	defer stopEvict()
	go func() {
		for {
			select {
			case <-evictCtx.Done():
				return
			case <-time.After(testInterval / 2):
				_ = v.Del(evictCtx, masterKey)
			}
		}
	}()

	// Next refresh tick should detect the loss.
	awaitMaster(t, m, false, 500*time.Millisecond)
}

// TestMasterLock_RunIfMaster verifies that the managed fn starts when master
// status is gained and stops when the context is cancelled.
func TestMasterLock_RunIfMaster(t *testing.T) {
	v := testvalkey.New(t)
	m := newTestLock(v, "a")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	started := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)
	fn := func(fnCtx context.Context) {
		started <- struct{}{}
		<-fnCtx.Done()
		stopped <- struct{}{}
	}

	go m.Run(ctx)
	go m.RunIfMaster(ctx, fn)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fn was not started within 1 s of becoming master")
	}

	// Cancelling the outer context should stop fn.
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("fn was not stopped within 1 s of context cancellation")
	}
}

// TestMasterLock_RunIfMaster_StopsOnLoss verifies that fn is stopped when the
// Valkey key is evicted (master status lost) without cancelling the context.
func TestMasterLock_RunIfMaster_StopsOnLoss(t *testing.T) {
	v := testvalkey.New(t)
	m := newTestLock(v, "a")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	started := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)
	fn := func(fnCtx context.Context) {
		started <- struct{}{}
		<-fnCtx.Done()
		stopped <- struct{}{}
	}

	go m.Run(ctx)
	go m.RunIfMaster(ctx, fn)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fn not started")
	}

	// Evict the key — master status is lost.
	_ = v.Del(context.Background(), masterKey)

	select {
	case <-stopped:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("fn not stopped after lock loss")
	}
}
