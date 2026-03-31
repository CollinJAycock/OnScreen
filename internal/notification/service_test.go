package notification

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── mock DB ─────────────────────────────────────────────────────────────────

type mockDB struct {
	created    []gen.CreateNotificationParams
	createErr  error
	userIDs    []uuid.UUID
	userIDsErr error
}

func (m *mockDB) CreateNotification(_ context.Context, arg gen.CreateNotificationParams) (gen.Notification, error) {
	if m.createErr != nil {
		return gen.Notification{}, m.createErr
	}
	m.created = append(m.created, arg)
	return gen.Notification{
		ID:        uuid.New(),
		UserID:    arg.UserID,
		Type:      arg.Type,
		Title:     arg.Title,
		Body:      arg.Body,
		ItemID:    arg.ItemID,
		Read:      false,
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}, nil
}

func (m *mockDB) ListAllUserIDs(_ context.Context) ([]uuid.UUID, error) {
	if m.userIDsErr != nil {
		return nil, m.userIDsErr
	}
	return m.userIDs, nil
}

// ── tests ───────────────────────────────────────────────────────────────────

func TestNotify_CreatesAndPublishes(t *testing.T) {
	db := &mockDB{}
	broker := NewBroker()
	svc := NewService(db, broker, slog.Default())

	uid := uuid.New()
	ch := broker.Subscribe(uid)
	defer broker.Unsubscribe(uid, ch)

	svc.Notify(context.Background(), uid, "system", "Test Title", "Test Body", nil)

	if len(db.created) != 1 {
		t.Fatalf("created: got %d, want 1", len(db.created))
	}
	if db.created[0].Type != "system" {
		t.Errorf("type: got %q, want %q", db.created[0].Type, "system")
	}

	select {
	case ev := <-ch:
		if ev.Title != "Test Title" {
			t.Errorf("event title: got %q, want %q", ev.Title, "Test Title")
		}
		if ev.Body != "Test Body" {
			t.Errorf("event body: got %q, want %q", ev.Body, "Test Body")
		}
		if ev.Read {
			t.Error("expected Read=false for new notification")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for SSE event")
	}
}

func TestNotify_WithItemID(t *testing.T) {
	db := &mockDB{}
	broker := NewBroker()
	svc := NewService(db, broker, slog.Default())

	uid := uuid.New()
	itemID := uuid.New()
	ch := broker.Subscribe(uid)
	defer broker.Unsubscribe(uid, ch)

	svc.Notify(context.Background(), uid, "new_content", "New Movie", "", &itemID)

	if !db.created[0].ItemID.Valid {
		t.Error("expected ItemID to be set")
	}

	ev := <-ch
	if ev.ItemID == nil {
		t.Fatal("expected event.ItemID to be non-nil")
	}
	if *ev.ItemID != itemID.String() {
		t.Errorf("ItemID: got %q, want %q", *ev.ItemID, itemID.String())
	}
}

func TestNotify_DBError_NoPublish(t *testing.T) {
	db := &mockDB{createErr: context.DeadlineExceeded}
	broker := NewBroker()
	svc := NewService(db, broker, slog.Default())

	uid := uuid.New()
	ch := broker.Subscribe(uid)
	defer broker.Unsubscribe(uid, ch)

	svc.Notify(context.Background(), uid, "system", "Fail", "", nil)

	select {
	case ev := <-ch:
		t.Errorf("expected no event on DB error, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// Expected — no event published.
	}
}

func TestNotifyAllUsers_BroadcastsToEachUser(t *testing.T) {
	uid1 := uuid.New()
	uid2 := uuid.New()
	db := &mockDB{userIDs: []uuid.UUID{uid1, uid2}}
	broker := NewBroker()
	svc := NewService(db, broker, slog.Default())

	ch1 := broker.Subscribe(uid1)
	ch2 := broker.Subscribe(uid2)
	defer broker.Unsubscribe(uid1, ch1)
	defer broker.Unsubscribe(uid2, ch2)

	svc.NotifyAllUsers(context.Background(), "system", "Broadcast", "Hi all", nil)

	if len(db.created) != 2 {
		t.Fatalf("created: got %d, want 2", len(db.created))
	}

	for i, ch := range []chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if ev.Title != "Broadcast" {
				t.Errorf("user%d title: got %q, want %q", i+1, ev.Title, "Broadcast")
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("user%d: timed out waiting for event", i+1)
		}
	}
}

func TestNotifyScanComplete_ZeroItems_NoNotification(t *testing.T) {
	db := &mockDB{userIDs: []uuid.UUID{uuid.New()}}
	svc := NewService(db, NewBroker(), slog.Default())

	svc.NotifyScanComplete(context.Background(), "Movies", 0)

	if len(db.created) != 0 {
		t.Errorf("expected no notifications for 0 new items, got %d", len(db.created))
	}
}

func TestNotifyScanComplete_SingularPlural(t *testing.T) {
	tests := []struct {
		count int
		want  string
	}{
		{1, "Movies: 1 new item added"},
		{5, "Movies: 5 new items added"},
	}
	for _, tt := range tests {
		db := &mockDB{userIDs: []uuid.UUID{uuid.New()}}
		svc := NewService(db, NewBroker(), slog.Default())

		svc.NotifyScanComplete(context.Background(), "Movies", tt.count)

		if len(db.created) != 1 {
			t.Fatalf("count=%d: created %d, want 1", tt.count, len(db.created))
		}
		if db.created[0].Body != tt.want {
			t.Errorf("count=%d: body got %q, want %q", tt.count, db.created[0].Body, tt.want)
		}
	}
}

func TestNotifyNewContent_IncludesItemID(t *testing.T) {
	uid := uuid.New()
	itemID := uuid.New()
	db := &mockDB{userIDs: []uuid.UUID{uid}}
	svc := NewService(db, NewBroker(), slog.Default())

	svc.NotifyNewContent(context.Background(), "Inception", itemID)

	if len(db.created) != 1 {
		t.Fatalf("created: got %d, want 1", len(db.created))
	}
	if db.created[0].Title != "New: Inception" {
		t.Errorf("title: got %q, want %q", db.created[0].Title, "New: Inception")
	}
	if !db.created[0].ItemID.Valid {
		t.Error("expected ItemID to be set")
	}
	if db.created[0].Type != "new_content" {
		t.Errorf("type: got %q, want %q", db.created[0].Type, "new_content")
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{100, "100"},
		{9999, "9999"},
	}
	for _, tt := range tests {
		if got := itoa(tt.n); got != tt.want {
			t.Errorf("itoa(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
