package transcode

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/testvalkey"
)

// ── DeleteByMedia ────────────────────────────────────────────────────────────

func TestIntegration_SessionStore_DeleteByMedia(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	mediaID := uuid.New()
	otherMediaID := uuid.New()

	// Create 2 sessions for mediaID and 1 for otherMediaID.
	for i := 0; i < 2; i++ {
		sess := Session{
			ID:          NewSessionID(),
			UserID:      uuid.New(),
			MediaItemID: mediaID,
			FileID:      uuid.New(),
			Decision:    "transcode",
			CreatedAt:   time.Now().UTC(),
		}
		if err := store.Create(ctx, sess); err != nil {
			t.Fatalf("Create session %d: %v", i, err)
		}
	}
	otherSess := Session{
		ID:          NewSessionID(),
		UserID:      uuid.New(),
		MediaItemID: otherMediaID,
		FileID:      uuid.New(),
		Decision:    "directPlay",
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.Create(ctx, otherSess); err != nil {
		t.Fatalf("Create other session: %v", err)
	}

	// Delete sessions for mediaID.
	if err := store.DeleteByMedia(ctx, mediaID); err != nil {
		t.Fatalf("DeleteByMedia: %v", err)
	}

	// Only the other session should remain.
	sessions, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("want 1 session remaining, got %d", len(sessions))
	}
	if sessions[0].MediaItemID != otherMediaID {
		t.Errorf("remaining session should be for otherMediaID, got %s", sessions[0].MediaItemID)
	}
}

func TestIntegration_SessionStore_DeleteByMedia_NoMatch(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	sess := Session{
		ID:          NewSessionID(),
		UserID:      uuid.New(),
		MediaItemID: uuid.New(),
		FileID:      uuid.New(),
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Delete for a non-existent media ID — should be a no-op.
	if err := store.DeleteByMedia(ctx, uuid.New()); err != nil {
		t.Fatalf("DeleteByMedia: %v", err)
	}

	sessions, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("want 1 session (no-op delete), got %d", len(sessions))
	}
}

// ── UpdatePositionByMedia ────────────────────────────────────────────────────

func TestIntegration_SessionStore_UpdatePositionByMedia(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	mediaID := uuid.New()
	sess := Session{
		ID:          NewSessionID(),
		UserID:      uuid.New(),
		MediaItemID: mediaID,
		FileID:      uuid.New(),
		Decision:    "transcode",
		PositionMS:  0,
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update position.
	if err := store.UpdatePositionByMedia(ctx, mediaID, 42000); err != nil {
		t.Fatalf("UpdatePositionByMedia: %v", err)
	}

	got, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PositionMS != 42000 {
		t.Errorf("PositionMS: want 42000, got %d", got.PositionMS)
	}
	if got.LastActivityAt.IsZero() {
		t.Error("LastActivityAt should be set after position update")
	}
}

func TestIntegration_SessionStore_UpdatePositionByMedia_NoMatch(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	sess := Session{
		ID:          NewSessionID(),
		UserID:      uuid.New(),
		MediaItemID: uuid.New(),
		FileID:      uuid.New(),
		PositionMS:  1000,
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Update for non-matching media ID — original should be unchanged.
	if err := store.UpdatePositionByMedia(ctx, uuid.New(), 99999); err != nil {
		t.Fatalf("UpdatePositionByMedia: %v", err)
	}

	got, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PositionMS != 1000 {
		t.Errorf("PositionMS should be unchanged (1000), got %d", got.PositionMS)
	}
}

// ── SetWorkerInfo ────────────────────────────────────────────────────────────

func TestIntegration_SessionStore_SetWorkerInfo(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	sess := Session{
		ID:        NewSessionID(),
		UserID:    uuid.New(),
		FileID:    uuid.New(),
		CreatedAt: time.Now().UTC(),
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.SetWorkerInfo(ctx, sess.ID, "worker-1", "10.0.0.5:7073"); err != nil {
		t.Fatalf("SetWorkerInfo: %v", err)
	}

	got, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.WorkerID != "worker-1" {
		t.Errorf("WorkerID: want %q, got %q", "worker-1", got.WorkerID)
	}
	if got.WorkerAddr != "10.0.0.5:7073" {
		t.Errorf("WorkerAddr: want %q, got %q", "10.0.0.5:7073", got.WorkerAddr)
	}
}

// ── Index set consistency ────────────────────────────────────────────────────

func TestIntegration_SessionStore_IndexCleanup(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	sess := Session{
		ID:          NewSessionID(),
		UserID:      uuid.New(),
		MediaItemID: uuid.New(),
		FileID:      uuid.New(),
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Manually delete the session key (simulating TTL expiry) but leave the index.
	if err := v.Del(ctx, "transcode:session:"+sess.ID); err != nil {
		t.Fatalf("Del raw key: %v", err)
	}

	// List should self-heal: stale index entries cleaned up.
	sessions, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("want 0 sessions after key expiry, got %d", len(sessions))
	}

	// The stale index entry should have been removed.
	members := v.Raw().SMembers(ctx, sessionIndexKey).Val()
	if len(members) != 0 {
		t.Errorf("stale index entry not cleaned up: %v", members)
	}
}

func TestIntegration_SessionStore_WorkerIndexCleanup(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	reg := WorkerRegistration{
		ID:           WorkerID(),
		Addr:         ":7073",
		Capabilities: []string{"libx264"},
		MaxSessions:  4,
		RegisteredAt: time.Now().UTC(),
	}
	if err := store.RegisterWorker(ctx, reg); err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}

	// Manually delete the worker key (simulating TTL expiry).
	if err := v.Del(ctx, "worker:"+reg.ID); err != nil {
		t.Fatalf("Del raw key: %v", err)
	}

	// ListWorkers should self-heal.
	workers, err := store.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(workers) != 0 {
		t.Errorf("want 0 workers after key expiry, got %d", len(workers))
	}

	members := v.Raw().SMembers(ctx, workerIndexKey).Val()
	if len(members) != 0 {
		t.Errorf("stale worker index entry not cleaned up: %v", members)
	}
}
