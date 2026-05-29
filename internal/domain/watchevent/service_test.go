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

// scrobbleCall captures one invocation of the async scrobble hook.
type scrobbleCall struct {
	userID, mediaID uuid.UUID
	positionMS      int64
	durationMS      *int64
	at              time.Time
}

// A terminal 'stop' is the trigger the external scrobbler rides — first-party
// clients never emit a distinct 'scrobble' event, so Record must fan a 'stop'
// out to the hook with the final position + duration intact.
func TestRecord_StopFiresScrobbleHook(t *testing.T) {
	svc, _ := newTestService(t)
	calls := make(chan scrobbleCall, 1)
	svc.WithScrobbleHook(func(_ context.Context, userID, mediaID uuid.UUID, positionMS int64, durationMS *int64, at time.Time) {
		calls <- scrobbleCall{userID, mediaID, positionMS, durationMS, at}
	})

	userID, mediaID := uuid.New(), uuid.New()
	durMS := int64(300_000)
	at := time.Unix(1_700_000_000, 0).UTC()

	if err := svc.Record(context.Background(), RecordParams{
		UserID: userID, MediaID: mediaID, EventType: "stop",
		PositionMS: 200_000, DurationMS: &durMS, OccurredAt: at,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	select {
	case c := <-calls:
		if c.userID != userID || c.mediaID != mediaID {
			t.Errorf("ids: got user=%s media=%s, want user=%s media=%s", c.userID, c.mediaID, userID, mediaID)
		}
		if c.positionMS != 200_000 {
			t.Errorf("positionMS: got %d, want 200000", c.positionMS)
		}
		if c.durationMS == nil || *c.durationMS != durMS {
			t.Errorf("durationMS: got %v, want %d", c.durationMS, durMS)
		}
		if !c.at.Equal(at) {
			t.Errorf("at: got %s, want %s", c.at, at)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scrobble hook was not called for stop event")
	}
}

// Only 'stop' triggers the hook. In particular the legacy 'scrobble' event
// still refreshes the matview (see TestRecord_ScrobbleTriggersRefresh) but must
// NOT double-dispatch a listen now that the trigger moved to 'stop'.
func TestRecord_NonStopDoesNotFireScrobbleHook(t *testing.T) {
	for _, et := range []string{"play", "pause", "resume", "seek", "scrobble"} {
		t.Run(et, func(t *testing.T) {
			svc, _ := newTestService(t)
			fired := make(chan struct{}, 1)
			svc.WithScrobbleHook(func(context.Context, uuid.UUID, uuid.UUID, int64, *int64, time.Time) {
				fired <- struct{}{}
			})

			if err := svc.Record(context.Background(), RecordParams{
				UserID: uuid.New(), MediaID: uuid.New(), EventType: et,
				PositionMS: 200_000, OccurredAt: time.Now(),
			}); err != nil {
				t.Fatalf("Record: %v", err)
			}

			select {
			case <-fired:
				t.Errorf("scrobble hook must not fire for %q event", et)
			case <-time.After(50 * time.Millisecond):
				// expected: no dispatch
			}
		})
	}
}

// Record fills a zero OccurredAt with the current time before handing it to the
// hook, so a listen always carries a usable listened_at timestamp.
func TestRecord_StopHookDefaultsTimestamp(t *testing.T) {
	svc, _ := newTestService(t)
	calls := make(chan scrobbleCall, 1)
	svc.WithScrobbleHook(func(_ context.Context, userID, mediaID uuid.UUID, positionMS int64, durationMS *int64, at time.Time) {
		calls <- scrobbleCall{userID, mediaID, positionMS, durationMS, at}
	})

	before := time.Now().UTC()
	if err := svc.Record(context.Background(), RecordParams{
		UserID: uuid.New(), MediaID: uuid.New(), EventType: "stop",
		PositionMS: 200_000, // OccurredAt left zero
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	select {
	case c := <-calls:
		if c.at.IsZero() {
			t.Fatal("expected a defaulted timestamp, got zero")
		}
		if c.at.Before(before) {
			t.Errorf("defaulted at %s precedes call start %s", c.at, before)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scrobble hook was not called")
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
