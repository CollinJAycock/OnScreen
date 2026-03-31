package watchevent

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// mockQuerier is a minimal in-memory Querier for unit tests.
type mockQuerier struct {
	insertCalled  bool
	insertParams  InsertWatchEventParams
	insertErr     error
	refreshCalled bool
	refreshErr    error

	states map[string]WatchState // key: userID+":"+mediaID
}

func newMockQuerier() *mockQuerier {
	return &mockQuerier{states: make(map[string]WatchState)}
}

func (m *mockQuerier) InsertWatchEvent(_ context.Context, p InsertWatchEventParams) (InsertWatchEventRow, error) {
	m.insertCalled = true
	m.insertParams = p
	if m.insertErr != nil {
		return InsertWatchEventRow{}, m.insertErr
	}
	return InsertWatchEventRow{ID: uuid.New(), OccurredAt: p.OccurredAt}, nil
}

func (m *mockQuerier) RefreshWatchState(_ context.Context) error {
	m.refreshCalled = true
	return m.refreshErr
}

func (m *mockQuerier) GetWatchState(_ context.Context, userID, mediaID uuid.UUID) (WatchState, error) {
	key := userID.String() + ":" + mediaID.String()
	s, ok := m.states[key]
	if !ok {
		return WatchState{}, pgx.ErrNoRows
	}
	return s, nil
}

func (m *mockQuerier) ListWatchStateForUser(_ context.Context, userID uuid.UUID) ([]WatchState, error) {
	var out []WatchState
	for _, s := range m.states {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	return out, nil
}

func newTestService(t *testing.T) (*Service, *mockQuerier) {
	t.Helper()
	q := newMockQuerier()
	svc := NewService(q, q, slog.Default())
	return svc, q
}

func TestRecord_InsertsEvent(t *testing.T) {
	svc, q := newTestService(t)

	userID := uuid.New()
	mediaID := uuid.New()
	now := time.Now().UTC()

	err := svc.Record(context.Background(), RecordParams{
		UserID:     userID,
		MediaID:    mediaID,
		EventType:  "play",
		PositionMS: 5000,
		OccurredAt: now,
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	if !q.insertCalled {
		t.Fatal("expected InsertWatchEvent to be called")
	}
	if q.insertParams.EventType != "play" {
		t.Errorf("want EventType=play, got %s", q.insertParams.EventType)
	}
	if q.insertParams.PositionMS != 5000 {
		t.Errorf("want PositionMS=5000, got %d", q.insertParams.PositionMS)
	}
}

func TestRecord_InsertError_Propagates(t *testing.T) {
	svc, q := newTestService(t)
	q.insertErr = errors.New("db down")

	err := svc.Record(context.Background(), RecordParams{
		UserID:     uuid.New(),
		MediaID:    uuid.New(),
		EventType:  "play",
		OccurredAt: time.Now(),
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRecord_StopTriggersRefresh(t *testing.T) {
	svc, q := newTestService(t)

	err := svc.Record(context.Background(), RecordParams{
		UserID:     uuid.New(),
		MediaID:    uuid.New(),
		EventType:  "stop",
		OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	// Give the goroutine a moment to run.
	time.Sleep(10 * time.Millisecond)
	if !q.refreshCalled {
		t.Error("expected RefreshWatchState to be called after stop event")
	}
}

func TestRecord_ScrobbleTriggersRefresh(t *testing.T) {
	svc, q := newTestService(t)

	err := svc.Record(context.Background(), RecordParams{
		UserID:     uuid.New(),
		MediaID:    uuid.New(),
		EventType:  "scrobble",
		OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Record returned error: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if !q.refreshCalled {
		t.Error("expected RefreshWatchState to be called after scrobble event")
	}
}

func TestRecord_PlayNoRefresh(t *testing.T) {
	svc, q := newTestService(t)

	_ = svc.Record(context.Background(), RecordParams{
		UserID:     uuid.New(),
		MediaID:    uuid.New(),
		EventType:  "play",
		OccurredAt: time.Now(),
	})
	time.Sleep(10 * time.Millisecond)
	if q.refreshCalled {
		t.Error("did not expect RefreshWatchState for play event")
	}
}

func TestGetState_NotFound_ReturnsUnwatched(t *testing.T) {
	svc, _ := newTestService(t)

	userID := uuid.New()
	mediaID := uuid.New()

	state, err := svc.GetState(context.Background(), userID, mediaID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != "unwatched" {
		t.Errorf("want status=unwatched, got %s", state.Status)
	}
	if state.UserID != userID {
		t.Errorf("want UserID preserved in unwatched state")
	}
	if state.MediaID != mediaID {
		t.Errorf("want MediaID preserved in unwatched state")
	}
}

func TestGetState_Found(t *testing.T) {
	svc, q := newTestService(t)

	userID := uuid.New()
	mediaID := uuid.New()
	q.states[userID.String()+":"+mediaID.String()] = WatchState{
		UserID:  userID,
		MediaID: mediaID,
		Status:  "in_progress",
	}

	state, err := svc.GetState(context.Background(), userID, mediaID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if state.Status != "in_progress" {
		t.Errorf("want status=in_progress, got %s", state.Status)
	}
}

func TestListStates_Error(t *testing.T) {
	svc, q := newTestService(t)
	q.insertErr = nil // not relevant here

	// Override ListWatchStateForUser to return an error.
	// We do this by using a separate errQuerier.
	errQ := &errListQuerier{inner: q}
	svc.ro = errQ

	_, err := svc.ListStates(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("expected error from ListStates, got nil")
	}
}

// errListQuerier wraps mockQuerier and returns an error on ListWatchStateForUser.
type errListQuerier struct{ inner *mockQuerier }

func (e *errListQuerier) InsertWatchEvent(ctx context.Context, p InsertWatchEventParams) (InsertWatchEventRow, error) {
	return e.inner.InsertWatchEvent(ctx, p)
}
func (e *errListQuerier) RefreshWatchState(ctx context.Context) error {
	return e.inner.RefreshWatchState(ctx)
}
func (e *errListQuerier) GetWatchState(ctx context.Context, userID, mediaID uuid.UUID) (WatchState, error) {
	return e.inner.GetWatchState(ctx, userID, mediaID)
}
func (e *errListQuerier) ListWatchStateForUser(_ context.Context, _ uuid.UUID) ([]WatchState, error) {
	return nil, errors.New("list error")
}

func TestListStates(t *testing.T) {
	svc, q := newTestService(t)

	userID := uuid.New()
	for i := 0; i < 3; i++ {
		mid := uuid.New()
		q.states[userID.String()+":"+mid.String()] = WatchState{
			UserID: userID, MediaID: mid, Status: "watched",
		}
	}

	states, err := svc.ListStates(context.Background(), userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(states) != 3 {
		t.Errorf("want 3 states, got %d", len(states))
	}
}
