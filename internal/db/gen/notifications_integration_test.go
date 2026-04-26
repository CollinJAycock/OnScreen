//go:build integration

// Round-trips the notifications-table queries. Notifications surface
// every async state change to the user (request approved, scan
// complete, etc.) so a query bug here means the user's bell icon
// shows the wrong count or the wrong rows.
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func createNotif(t *testing.T, q *gen.Queries, userID uuid.UUID, typ, title string) gen.Notification {
	t.Helper()
	n, err := q.CreateNotification(context.Background(), gen.CreateNotificationParams{
		UserID: userID, Type: typ, Title: title, Body: "body",
	})
	if err != nil {
		t.Fatalf("CreateNotification: %v", err)
	}
	return n
}

// TestNotifications_Integration_CreateAndCount round-trips the create
// → count-unread loop the bell icon depends on. Three new notifications
// → unread count of 3.
func TestNotifications_Integration_CreateAndCount(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "notif-cnt-"+uuid.New().String()[:8])

	// Pre-condition: no rows = count 0.
	if c, err := q.CountUnreadNotifications(ctx, user); err != nil || c != 0 {
		t.Fatalf("initial count: got %d err=%v, want 0", c, err)
	}

	for i := 0; i < 3; i++ {
		createNotif(t, q, user, "scan.complete", "Scan done")
	}

	got, err := q.CountUnreadNotifications(ctx, user)
	if err != nil {
		t.Fatalf("CountUnreadNotifications: %v", err)
	}
	if got != 3 {
		t.Errorf("count = %d, want 3", got)
	}
}

// TestNotifications_Integration_MarkOneAsReadDoesntAffectOthers proves
// MarkNotificationRead is row-scoped. Marking notification #2 as read
// must not affect the read state of #1 or #3.
func TestNotifications_Integration_MarkOneAsReadDoesntAffectOthers(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "notif-mark-"+uuid.New().String()[:8])
	n1 := createNotif(t, q, user, "x", "one")
	n2 := createNotif(t, q, user, "x", "two")
	n3 := createNotif(t, q, user, "x", "three")

	if err := q.MarkNotificationRead(ctx, gen.MarkNotificationReadParams{
		ID: n2.ID, UserID: user,
	}); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}

	rows, err := q.ListNotifications(ctx, gen.ListNotificationsParams{
		UserID: user, Limit: 100, Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3 (mark-read should NOT delete)", len(rows))
	}
	readMap := map[uuid.UUID]bool{}
	for _, r := range rows {
		readMap[r.ID] = r.Read
	}
	if readMap[n1.ID] {
		t.Error("n1 unexpectedly marked read")
	}
	if !readMap[n2.ID] {
		t.Error("n2 should be marked read")
	}
	if readMap[n3.ID] {
		t.Error("n3 unexpectedly marked read")
	}

	// Unread count should drop to 2 (we read 1 of 3).
	if c, _ := q.CountUnreadNotifications(ctx, user); c != 2 {
		t.Errorf("unread count after marking one read = %d, want 2", c)
	}
}

// TestNotifications_Integration_MarkAllReadIsUserScoped proves
// MarkAllNotificationsRead doesn't bleed across user boundaries — a
// regression where the WHERE user_id clause fell off would mark every
// user's bell icon as zero.
func TestNotifications_Integration_MarkAllReadIsUserScoped(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	alice := seedUser(ctx, t, q, "notif-alice-"+uuid.New().String()[:8])
	bob := seedUser(ctx, t, q, "notif-bob-"+uuid.New().String()[:8])

	createNotif(t, q, alice, "x", "alice msg")
	createNotif(t, q, bob, "x", "bob msg")

	if err := q.MarkAllNotificationsRead(ctx, alice); err != nil {
		t.Fatalf("MarkAllNotificationsRead: %v", err)
	}

	if c, _ := q.CountUnreadNotifications(ctx, alice); c != 0 {
		t.Errorf("alice unread = %d, want 0", c)
	}
	if c, _ := q.CountUnreadNotifications(ctx, bob); c != 1 {
		t.Errorf("bob unread = %d, want 1 — MarkAllRead leaked across users", c)
	}
}

// TestNotifications_Integration_ListIsUserScoped proves ListNotifications
// only returns the requesting user's rows. Same kind of WHERE user_id
// regression guard as the previous test.
func TestNotifications_Integration_ListIsUserScoped(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	alice := seedUser(ctx, t, q, "notif-lst-a-"+uuid.New().String()[:8])
	bob := seedUser(ctx, t, q, "notif-lst-b-"+uuid.New().String()[:8])

	createNotif(t, q, alice, "x", "alice 1")
	createNotif(t, q, alice, "x", "alice 2")
	createNotif(t, q, bob, "x", "bob 1")

	rows, err := q.ListNotifications(ctx, gen.ListNotificationsParams{
		UserID: alice, Limit: 100, Offset: 0,
	})
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("alice: got %d rows, want 2 (bob's row leaked?)", len(rows))
	}
	for _, r := range rows {
		if r.UserID != alice {
			t.Errorf("got bob's row in alice's list: %+v", r)
		}
	}
}
