package transcode

import (
	"context"
	"errors"
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

// ── ListByUserItem ───────────────────────────────────────────────────────────

func TestIntegration_SessionStore_ListByUserItem(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	userA := uuid.New()
	userB := uuid.New()
	itemX := uuid.New()
	itemY := uuid.New()

	// Two sessions for (userA, itemX), one for (userA, itemY), one for (userB, itemX).
	want := map[string]bool{}
	for i := 0; i < 2; i++ {
		s := Session{
			ID: NewSessionID(), UserID: userA, MediaItemID: itemX,
			FileID: uuid.New(), CreatedAt: time.Now().UTC(),
		}
		if err := store.Create(ctx, s); err != nil {
			t.Fatalf("Create userA/itemX[%d]: %v", i, err)
		}
		want[s.ID] = true
	}
	for _, s := range []Session{
		{ID: NewSessionID(), UserID: userA, MediaItemID: itemY, FileID: uuid.New(), CreatedAt: time.Now().UTC()},
		{ID: NewSessionID(), UserID: userB, MediaItemID: itemX, FileID: uuid.New(), CreatedAt: time.Now().UTC()},
	} {
		if err := store.Create(ctx, s); err != nil {
			t.Fatalf("Create noise session: %v", err)
		}
	}

	got, err := store.ListByUserItem(ctx, userA, itemX)
	if err != nil {
		t.Fatalf("ListByUserItem: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 sessions for (userA, itemX), got %d", len(got))
	}
	for _, s := range got {
		if !want[s.ID] {
			t.Errorf("unexpected session %s in result (different user or item)", s.ID)
		}
	}
}

// CountByUser must count only LIVE sessions (activity within ActiveSessionWindow)
// so an abandoned session — TV powered off mid-stream, client crash, missed
// DELETE — whose Valkey entry lingers for the 4 h TTL doesn't falsely hold a
// concurrency-cap slot. A brand-new session with no LastActivityAt falls back to
// CreatedAt so rapid Start-spam is still capped.
func TestIntegration_SessionStore_CountByUser_ExcludesStale(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	user := uuid.New()
	now := time.Now().UTC()

	sessions := []Session{
		// live: heartbeat just now → counted (even though created long ago)
		{ID: NewSessionID(), UserID: user, MediaItemID: uuid.New(), FileID: uuid.New(), CreatedAt: now.Add(-10 * time.Minute), LastActivityAt: now},
		// brand-new: no LastActivityAt yet, just created → counted (CreatedAt fallback)
		{ID: NewSessionID(), UserID: user, MediaItemID: uuid.New(), FileID: uuid.New(), CreatedAt: now},
		// abandoned: last activity well past the window → NOT counted
		{ID: NewSessionID(), UserID: user, MediaItemID: uuid.New(), FileID: uuid.New(), CreatedAt: now.Add(-30 * time.Minute), LastActivityAt: now.Add(-5 * time.Minute)},
		// created long ago, never fetched a segment → NOT counted (stale CreatedAt)
		{ID: NewSessionID(), UserID: user, MediaItemID: uuid.New(), FileID: uuid.New(), CreatedAt: now.Add(-10 * time.Minute)},
		// different user, live → not counted for `user`
		{ID: NewSessionID(), UserID: uuid.New(), MediaItemID: uuid.New(), FileID: uuid.New(), CreatedAt: now, LastActivityAt: now},
	}
	for i, s := range sessions {
		if err := store.Create(ctx, s); err != nil {
			t.Fatalf("Create[%d]: %v", i, err)
		}
	}

	n, err := store.CountByUser(ctx, user)
	if err != nil {
		t.Fatalf("CountByUser: %v", err)
	}
	if n != 2 {
		t.Errorf("CountByUser = %d, want 2 (live + brand-new counted; abandoned/stale excluded)", n)
	}
}

// TestIntegration_SessionStore_CreateWithUserCap covers the atomic write-time
// cap: it admits sessions up to the cap, rejects the one that would exceed it
// with ErrUserAtCap, and ignores the cap for other users / when maxPerUser<=0.
func TestIntegration_SessionStore_CreateWithUserCap(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()
	now := time.Now().UTC()
	user := uuid.New()

	mk := func(u uuid.UUID) Session {
		return Session{ID: NewSessionID(), UserID: u, MediaItemID: uuid.New(), FileID: uuid.New(), CreatedAt: now, LastActivityAt: now}
	}

	// Fill to the cap of 3.
	for i := 0; i < 3; i++ {
		if err := store.CreateWithUserCap(ctx, mk(user), 3); err != nil {
			t.Fatalf("CreateWithUserCap[%d]: %v", i, err)
		}
	}
	// The 4th must be rejected.
	if err := store.CreateWithUserCap(ctx, mk(user), 3); !errors.Is(err, ErrUserAtCap) {
		t.Fatalf("4th create: got %v, want ErrUserAtCap", err)
	}
	if n, _ := store.CountByUser(ctx, user); n != 3 {
		t.Errorf("count after cap hit = %d, want 3", n)
	}
	// A different user is unaffected.
	if err := store.CreateWithUserCap(ctx, mk(uuid.New()), 3); err != nil {
		t.Fatalf("other-user create: %v", err)
	}
	// maxPerUser <= 0 disables the cap.
	if err := store.CreateWithUserCap(ctx, mk(user), 0); err != nil {
		t.Fatalf("uncapped create: %v", err)
	}
	if n, _ := store.CountByUser(ctx, user); n != 4 {
		t.Errorf("count after uncapped create = %d, want 4", n)
	}
}

func TestIntegration_SessionStore_ListByUserItem_NoMatch(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	s := Session{
		ID: NewSessionID(), UserID: uuid.New(), MediaItemID: uuid.New(),
		FileID: uuid.New(), CreatedAt: time.Now().UTC(),
	}
	if err := store.Create(ctx, s); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.ListByUserItem(ctx, uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("ListByUserItem: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want 0 sessions, got %d", len(got))
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

	if err := store.SetWorkerInfo(ctx, sess.ID, "worker-1", "10.0.0.5:7073", false, false); err != nil {
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
