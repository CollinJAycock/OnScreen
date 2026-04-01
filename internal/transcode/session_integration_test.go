package transcode

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/testvalkey"
)

func TestIntegration_SessionStore_CreateGetDelete(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	sess := Session{
		ID:          NewSessionID(),
		UserID:      uuid.New(),
		MediaItemID: uuid.New(),
		FileID:      uuid.New(),
		Decision:    "transcode",
		FilePath:    "/media/movie.mkv",
		CreatedAt:   time.Now().UTC(),
		ClientName:  "Infuse",
	}

	if err := store.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("want ID %s, got %s", sess.ID, got.ID)
	}
	if got.Decision != "transcode" {
		t.Errorf("want Decision=transcode, got %s", got.Decision)
	}
	if got.ClientName != "Infuse" {
		t.Errorf("want ClientName=Infuse, got %s", got.ClientName)
	}

	if err := store.Delete(ctx, sess.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = store.Get(ctx, sess.ID)
	if err == nil {
		t.Error("expected error after Delete, got nil")
	}
}

func TestIntegration_SessionStore_List(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		sess := Session{
			ID:          NewSessionID(),
			UserID:      uuid.New(),
			MediaItemID: uuid.New(),
			FileID:      uuid.New(),
			CreatedAt:   time.Now().UTC(),
		}
		if err := store.Create(ctx, sess); err != nil {
			t.Fatalf("Create session %d: %v", i, err)
		}
	}

	sessions, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("want 3 sessions, got %d", len(sessions))
	}
}

func TestIntegration_SessionStore_Heartbeat(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	sessionID := NewSessionID()

	// No heartbeat yet — should not be alive.
	alive, err := store.IsAlive(ctx, sessionID)
	if err != nil {
		t.Fatalf("IsAlive: %v", err)
	}
	if alive {
		t.Error("expected session to not be alive before heartbeat")
	}

	if err := store.SetHeartbeat(ctx, sessionID); err != nil {
		t.Fatalf("SetHeartbeat: %v", err)
	}

	alive, err = store.IsAlive(ctx, sessionID)
	if err != nil {
		t.Fatalf("IsAlive after heartbeat: %v", err)
	}
	if !alive {
		t.Error("expected session to be alive after heartbeat")
	}
}

func TestIntegration_SessionStore_EnqueueDequeue(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	job := TranscodeJob{
		SessionID:   NewSessionID(),
		FilePath:    "/media/movie.mkv",
		SessionDir:  "/tmp/onscreen/sessions/abc",
		Decision:    "transcode",
		Encoder:     "libx264",
		Width:       1920,
		Height:      1080,
		BitrateKbps: 8000,
		AudioCodec:  "aac",
		EnqueuedAt:  time.Now().UTC(),
	}

	if err := store.EnqueueJob(ctx, job); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	got, err := store.DequeueJob(ctx, "test:7073", 2*time.Second)
	if err != nil {
		t.Fatalf("DequeueJob: %v", err)
	}
	if got == nil {
		t.Fatal("expected job, got nil")
	}
	if got.SessionID != job.SessionID {
		t.Errorf("want SessionID %s, got %s", job.SessionID, got.SessionID)
	}
	if got.BitrateKbps != 8000 {
		t.Errorf("want BitrateKbps 8000, got %d", got.BitrateKbps)
	}
}

func TestIntegration_SessionStore_Dequeue_Timeout(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	// Queue is empty — should return nil, nil after timeout.
	got, err := store.DequeueJob(ctx, "test:7073", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("DequeueJob timeout: %v", err)
	}
	if got != nil {
		t.Error("expected nil job on empty queue timeout")
	}
}

func TestIntegration_SessionStore_WorkerRegistration(t *testing.T) {
	v := testvalkey.New(t)
	store := NewSessionStore(v)
	ctx := context.Background()

	reg := WorkerRegistration{
		ID:             WorkerID(),
		Addr:           ":7073",
		Capabilities:   []string{"libx264"},
		MaxSessions:    4,
		ActiveSessions: 1,
		RegisteredAt:   time.Now().UTC(),
	}

	if err := store.RegisterWorker(ctx, reg); err != nil {
		t.Fatalf("RegisterWorker: %v", err)
	}

	workers, err := store.ListWorkers(ctx)
	if err != nil {
		t.Fatalf("ListWorkers: %v", err)
	}
	if len(workers) != 1 {
		t.Fatalf("want 1 worker, got %d", len(workers))
	}
	if workers[0].ID != reg.ID {
		t.Errorf("want worker ID %s, got %s", reg.ID, workers[0].ID)
	}
	if workers[0].MaxSessions != 4 {
		t.Errorf("want MaxSessions 4, got %d", workers[0].MaxSessions)
	}
}
