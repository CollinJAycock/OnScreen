package v1

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/transcode"
	"github.com/onscreen/onscreen/internal/valkey"
)

// newABRLockHandler builds a handler with the Valkey rung lock enabled and
// returns the shared Valkey client so a test can play "the other instance".
func newABRLockHandler(t *testing.T, v *valkey.Client) (*NativeTranscodeHandler, *transcode.SessionStore) {
	t.Helper()
	store := transcode.NewSessionStore(v)
	h := NewNativeTranscodeHandler(store, transcode.NewSegmentTokenManager(v), &mockTranscodeMedia{
		item: &media.Item{ID: uuid.New(), Type: "movie", Title: "Test"},
	}, &config.Config{TranscodeMaxHeight: 2160}, slog.Default()).
		WithVerifySource(fakeSourceOK).
		WithABRLock(v)
	return h, store
}

// countJobs drains the job queue and returns how many jobs were enqueued.
func countJobs(t *testing.T, store *transcode.SessionStore) int {
	t.Helper()
	n := 0
	for {
		job, _ := store.DequeueJob(context.Background(), "", 200*time.Millisecond)
		if job == nil {
			return n
		}
		n++
	}
}

// TestEnsureRungChild_ValkeyLockHeldElsewhereSkips plays a second API instance
// holding the rung's Valkey lock: this instance must not start its own child
// (that is the duplicate ffmpeg the per-process mutex could not prevent), and
// once the lock is free the child starts normally.
func TestEnsureRungChild_ValkeyLockHeldElsewhereSkips(t *testing.T) {
	old := abrLockWait
	abrLockWait = 150 * time.Millisecond
	t.Cleanup(func() { abrLockWait = old })

	v := testvalkey.New(t)
	h, store := newABRLockHandler(t, v)
	ctx := context.Background()
	parent, rung := abrTestParent(t, store)
	childID := abrChildID(parent.ID, rung.Label)

	other, ok, err := v.TryLock(ctx, abrLockKey(childID), time.Minute)
	if err != nil || !ok {
		t.Fatalf("other instance TryLock: ok=%v err=%v", ok, err)
	}

	h.ensureRungChild(ctx, parent, rung, childID, 0, false)
	if _, err := store.Get(ctx, childID); err == nil {
		t.Fatal("started a rung child while another instance held its lock")
	}
	if n := countJobs(t, store); n != 0 {
		t.Fatalf("dispatched %d job(s) while another instance held the lock", n)
	}

	_ = other.Release(ctx)
	h.ensureRungChild(ctx, parent, rung, childID, 0, false)
	if _, err := store.Get(ctx, childID); err != nil {
		t.Fatalf("child not started after the lock was released: %v", err)
	}
	if n := countJobs(t, store); n != 1 {
		t.Fatalf("dispatched %d jobs after release, want 1", n)
	}
	if v.Raw().Exists(ctx, abrLockKey(childID)).Val() != 0 {
		t.Error("rung lock key left behind after ensureRungChild returned")
	}
}

// TestEnsureRungChild_ConcurrentInstancesStartOnce runs two handlers (two API
// instances sharing one Valkey) racing the same rung. Both use the shared
// in-process map in this test binary, so the Valkey lease is exercised by the
// lock-key test above; this one pins the end-to-end outcome — exactly one
// child, exactly one job.
func TestEnsureRungChild_ConcurrentInstancesStartOnce(t *testing.T) {
	v := testvalkey.New(t)
	a, store := newABRLockHandler(t, v)
	b, _ := newABRLockHandler(t, v)
	ctx := context.Background()
	parent, rung := abrTestParent(t, store)
	childID := abrChildID(parent.ID, rung.Label)

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		h := a
		if i%2 == 1 {
			h = b
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.ensureRungChild(ctx, parent, rung, childID, 0, false)
		}()
	}
	wg.Wait()
	if n := countJobs(t, store); n != 1 {
		t.Fatalf("concurrent segment requests dispatched %d jobs, want 1", n)
	}
}

// TestEnsureRungChild_RestartFloorSharedAcrossInstances: the forced-restart
// floor lives in Valkey, so a restart stamped by another instance blocks this
// one even though its in-process stamp map has never seen the child.
func TestEnsureRungChild_RestartFloorSharedAcrossInstances(t *testing.T) {
	v := testvalkey.New(t)
	h, store := newABRLockHandler(t, v)
	ctx := context.Background()
	parent, rung := abrTestParent(t, store)
	childID := abrChildID(parent.ID, rung.Label)

	h.ensureRungChild(ctx, parent, rung, childID, 0, false)
	if n := countJobs(t, store); n != 1 {
		t.Fatalf("initial start dispatched %d jobs, want 1", n)
	}

	// Another instance just force-restarted this child.
	if err := v.Set(ctx, abrRestartKey(childID), "1", time.Minute); err != nil {
		t.Fatal(err)
	}
	abrLastRestart.Delete(childID)

	h.ensureRungChild(ctx, parent, rung, childID, 300, true)
	child, err := store.Get(ctx, childID)
	if err != nil {
		t.Fatalf("child missing: %v", err)
	}
	if child.Incarnation != 0 {
		t.Errorf("forced restart went through inside another instance's restart floor (incarnation=%d)", child.Incarnation)
	}
	if n := countJobs(t, store); n != 0 {
		t.Errorf("dispatched %d jobs inside the shared restart floor", n)
	}

	// Floor clears → the restart goes through and stamps the shared key.
	_ = v.Del(ctx, abrRestartKey(childID))
	h.ensureRungChild(ctx, parent, rung, childID, 300, true)
	child, _ = store.Get(ctx, childID)
	if child == nil || child.Incarnation != 1 {
		t.Fatalf("restart after the floor did not bump the incarnation: %+v", child)
	}
	if v.Raw().Exists(ctx, abrRestartKey(childID)).Val() != 1 {
		t.Error("forced restart did not stamp the shared restart floor")
	}
}
