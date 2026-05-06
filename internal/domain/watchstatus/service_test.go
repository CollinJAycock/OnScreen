package watchstatus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type stubQuerier struct {
	getResult Status
	getErr    error
	upsertResult Status
	upsertErr error
	deleteErr error

	upsertCalls []struct {
		userID, mediaID uuid.UUID
		status          string
	}
	deleteCalls []struct {
		userID, mediaID uuid.UUID
	}
}

func (s *stubQuerier) GetUserWatchStatus(_ context.Context, _, _ uuid.UUID) (Status, error) {
	return s.getResult, s.getErr
}
func (s *stubQuerier) UpsertUserWatchStatus(_ context.Context, userID, mediaID uuid.UUID, status string) (Status, error) {
	s.upsertCalls = append(s.upsertCalls, struct {
		userID, mediaID uuid.UUID
		status          string
	}{userID, mediaID, status})
	return s.upsertResult, s.upsertErr
}
func (s *stubQuerier) DeleteUserWatchStatus(_ context.Context, userID, mediaID uuid.UUID) error {
	s.deleteCalls = append(s.deleteCalls, struct {
		userID, mediaID uuid.UUID
	}{userID, mediaID})
	return s.deleteErr
}

func TestIsValidStatus(t *testing.T) {
	for _, ok := range []string{"plan_to_watch", "watching", "completed", "on_hold", "dropped"} {
		if !IsValidStatus(ok) {
			t.Errorf("IsValidStatus(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "watched", "PLAN_TO_WATCH", "rewatching", "garbage"} {
		if IsValidStatus(bad) {
			t.Errorf("IsValidStatus(%q) = true, want false", bad)
		}
	}
}

func TestService_Set_RejectsInvalid(t *testing.T) {
	svc := New(&stubQuerier{})
	_, err := svc.Set(context.Background(), uuid.New(), uuid.New(), "rewatching")
	if err == nil {
		t.Fatal("Set with invalid status should return an error")
	}
}

func TestService_Set_PassesThroughOnValid(t *testing.T) {
	user, item := uuid.New(), uuid.New()
	q := &stubQuerier{
		upsertResult: Status{
			UserID:      user,
			MediaItemID: item,
			Status:      "watching",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		},
	}
	svc := New(q)
	got, err := svc.Set(context.Background(), user, item, "watching")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != "watching" {
		t.Errorf("got status %q, want watching", got.Status)
	}
	if len(q.upsertCalls) != 1 {
		t.Fatalf("expected 1 upsert call, got %d", len(q.upsertCalls))
	}
}

func TestService_Get_PropagatesNotFound(t *testing.T) {
	q := &stubQuerier{getErr: ErrNotFound}
	svc := New(q)
	_, err := svc.Get(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestService_Clear_CallsDelete(t *testing.T) {
	user, item := uuid.New(), uuid.New()
	q := &stubQuerier{}
	svc := New(q)
	if err := svc.Clear(context.Background(), user, item); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(q.deleteCalls) != 1 {
		t.Fatalf("expected 1 delete call, got %d", len(q.deleteCalls))
	}
	if q.deleteCalls[0].userID != user || q.deleteCalls[0].mediaID != item {
		t.Errorf("delete called with wrong IDs: %+v", q.deleteCalls[0])
	}
}

func TestAllStatuses_OrderAndContent(t *testing.T) {
	got := AllStatuses()
	want := []string{"plan_to_watch", "watching", "on_hold", "completed", "dropped"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
