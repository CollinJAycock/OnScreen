package notification

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
)

var errTest = errors.New("insert failed")

type recordingAgents struct {
	mu     sync.Mutex
	events []AgentEvent
}

func (r *recordingAgents) DispatchAgentEvent(_ context.Context, ev AgentEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

// TestAgents_OneEventPerCall_NotPerRecipient: NotifyAdmins to three admins and
// NotifyAllUsers to four users each reach the outbound agents ONCE, with the
// broadcast scope and no recipient; the per-recipient copies don't.
func TestAgents_OneEventPerCall_NotPerRecipient(t *testing.T) {
	db := &mockDB{
		adminIDs: []uuid.UUID{uuid.New(), uuid.New(), uuid.New()},
		userIDs:  []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()},
	}
	svc := NewService(db, NewBroker(), slog.Default())
	rec := &recordingAgents{}
	svc.SetAgentDispatcher(rec)

	item := uuid.New()
	svc.NotifyAdmins(context.Background(), TypeRequestPending, "New request", "alice requested \"Dune\"", nil)
	svc.NotifyAllUsers(context.Background(), TypeSystem, "Maintenance", "tonight", &item)

	if len(db.created) != 7 {
		t.Fatalf("in-app notifications = %d, want 3 admin + 4 user copies", len(db.created))
	}
	if len(rec.events) != 2 {
		t.Fatalf("agent events = %d, want exactly 2 (one per call): %+v", len(rec.events), rec.events)
	}
	if ev := rec.events[0]; ev.Scope != AgentScopeAdmins || ev.Type != TypeRequestPending || ev.UserID != nil {
		t.Errorf("admin event = %+v", ev)
	}
	if ev := rec.events[1]; ev.Scope != AgentScopeAll || ev.ItemID == nil || *ev.ItemID != item {
		t.Errorf("broadcast event = %+v", ev)
	}
}

// TestAgents_NotifyCarriesRecipient: a direct Notify is forwarded with the
// user scope and the recipient (the requester, for request_* notices) — even
// when the in-app insert fails.
func TestAgents_NotifyCarriesRecipient(t *testing.T) {
	db := &mockDB{createErr: errTest}
	svc := NewService(db, NewBroker(), slog.Default())
	rec := &recordingAgents{}
	svc.SetAgentDispatcher(rec)

	user, item := uuid.New(), uuid.New()
	svc.Notify(context.Background(), user, TypeRequestAvailable, "Now available: Dune", "\"Dune\" is ready to watch.", &item)

	if len(rec.events) != 1 {
		t.Fatalf("agent events = %d, want 1", len(rec.events))
	}
	ev := rec.events[0]
	if ev.Scope != AgentScopeUser || ev.UserID == nil || *ev.UserID != user || ev.ItemID == nil || *ev.ItemID != item {
		t.Errorf("event = %+v", ev)
	}
}

// TestAgents_ScanCompleteNotForwarded: per-library scan notices stay in-app;
// the agents announce new titles through their own batched path.
func TestAgents_ScanCompleteNotForwarded(t *testing.T) {
	db := &mockDB{userIDs: []uuid.UUID{uuid.New(), uuid.New()}}
	svc := NewService(db, NewBroker(), slog.Default())
	rec := &recordingAgents{}
	svc.SetAgentDispatcher(rec)

	svc.NotifyScanComplete(context.Background(), uuid.New(), "Movies", 3)
	if len(db.created) != 2 {
		t.Fatalf("in-app notifications = %d, want 2", len(db.created))
	}
	if len(rec.events) != 0 {
		t.Errorf("scan_complete reached the agents: %+v", rec.events)
	}
}

// TestAgents_DetachedIsNoop: with no dispatcher (or after detaching) nothing
// is forwarded and nothing panics.
func TestAgents_DetachedIsNoop(t *testing.T) {
	db := &mockDB{adminIDs: []uuid.UUID{uuid.New()}}
	svc := NewService(db, NewBroker(), slog.Default())
	svc.NotifyAdmins(context.Background(), TypeIssueReported, "Problem reported", "x", nil)
	rec := &recordingAgents{}
	svc.SetAgentDispatcher(rec)
	svc.SetAgentDispatcher(nil)
	svc.NotifyAdmins(context.Background(), TypeIssueReported, "Problem reported", "x", nil)
	if len(rec.events) != 0 {
		t.Errorf("detached dispatcher still received %d events", len(rec.events))
	}
}
