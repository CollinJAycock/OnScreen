package livetv

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// capFakeQuerier is an in-memory DVRQuerier covering the schedule CRUD the
// per-user cap touches. It does NOT implement CountLiveSchedulesForUser, so
// DVRService takes the in-Go fallback count; countingCapQuerier adds it.
type capFakeQuerier struct {
	DVRQuerier // panic on anything else

	mu    sync.Mutex
	rows  map[uuid.UUID]*Schedule
	lists int
}

func newCapFakeQuerier() *capFakeQuerier {
	return &capFakeQuerier{rows: map[uuid.UUID]*Schedule{}}
}

func (q *capFakeQuerier) put(sc Schedule) Schedule {
	q.mu.Lock()
	defer q.mu.Unlock()
	if sc.ID == uuid.Nil {
		sc.ID = uuid.New()
	}
	cp := sc
	q.rows[sc.ID] = &cp
	return sc
}

func (q *capFakeQuerier) CreateSchedule(_ context.Context, p CreateScheduleParams) (Schedule, error) {
	return q.put(Schedule{
		UserID: p.UserID, Type: p.Type, ProgramID: p.ProgramID, ChannelID: p.ChannelID,
		TitleMatch: p.TitleMatch, TimeStart: p.TimeStart, TimeEnd: p.TimeEnd, Enabled: true,
	}), nil
}

func (q *capFakeQuerier) ListSchedulesForUser(_ context.Context, userID uuid.UUID) ([]Schedule, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.lists++
	var out []Schedule
	for _, r := range q.rows {
		if r.UserID == userID {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (q *capFakeQuerier) GetSchedule(_ context.Context, id uuid.UUID) (Schedule, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	r, ok := q.rows[id]
	if !ok {
		return Schedule{}, errors.New("not found")
	}
	return *r, nil
}

func (q *capFakeQuerier) SetScheduleEnabled(_ context.Context, id uuid.UUID, enabled bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if r, ok := q.rows[id]; ok {
		r.Enabled = enabled
	}
	return nil
}

// countingCapQuerier adds the exact SQL-backed count (liveScheduleCounter).
type countingCapQuerier struct {
	*capFakeQuerier
	live   int64
	err    error
	counts int
}

func (q *countingCapQuerier) CountLiveSchedulesForUser(_ context.Context, _ uuid.UUID) (int64, error) {
	q.counts++
	return q.live, q.err
}

func newCapDVR(q DVRQuerier) *DVRService {
	return NewDVRService(q, nil, "", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func onceParams(user uuid.UUID) CreateScheduleParams {
	pid := uuid.New()
	return CreateScheduleParams{UserID: user, Type: ScheduleTypeOnce, ProgramID: &pid}
}

func seriesSchedule(user uuid.UUID, enabled bool) Schedule {
	ch := uuid.New()
	title := "News"
	return Schedule{UserID: user, Type: ScheduleTypeSeries, ChannelID: &ch, TitleMatch: &title, Enabled: enabled}
}

// The regression: every guide "Record" click leaves a 'once' row behind that
// nothing ever deletes. Counting every row locked the user out for good after
// 100 lifetime one-offs. Spent one-offs (programme trimmed from the EPG, so
// program_id is NULL) must not count.
func TestDVRScheduleCap_SpentOneOffsDoNotCount(t *testing.T) {
	q := newCapFakeQuerier()
	user := uuid.New()
	for i := 0; i < 150; i++ {
		q.put(Schedule{UserID: user, Type: ScheduleTypeOnce, ProgramID: nil, Enabled: true})
	}
	if _, err := newCapDVR(q).CreateSchedule(context.Background(), onceParams(user)); err != nil {
		t.Fatalf("user with only spent one-offs was refused: %v", err)
	}
	// Nothing was deleted to make room.
	rows, _ := q.ListSchedulesForUser(context.Background(), user)
	if len(rows) != 151 {
		t.Errorf("schedule rows = %d, want 151 (no user data may be deleted)", len(rows))
	}
}

func TestDVRScheduleCap_LiveSchedulesStillCapped(t *testing.T) {
	q := newCapFakeQuerier()
	user := uuid.New()
	for i := 0; i < maxSchedulesPerUser/2; i++ {
		q.put(seriesSchedule(user, true))
		pid := uuid.New() // upcoming one-off, programme still in the EPG
		q.put(Schedule{UserID: user, Type: ScheduleTypeOnce, ProgramID: &pid, Enabled: true})
	}
	_, err := newCapDVR(q).CreateSchedule(context.Background(), onceParams(user))
	if !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("user at %d live schedules: got %v, want ErrInvalidSchedule", maxSchedulesPerUser, err)
	}
	// Another user is unaffected.
	if _, err := newCapDVR(q).CreateSchedule(context.Background(), onceParams(uuid.New())); err != nil {
		t.Errorf("a different user was refused: %v", err)
	}
}

// Disabled rules don't count, so re-enabling one must pass the cap too —
// otherwise "park rules disabled, create more, switch them back on" bypasses it.
func TestDVRScheduleCap_ReenableIsCapped(t *testing.T) {
	q := newCapFakeQuerier()
	user := uuid.New()
	parked := q.put(seriesSchedule(user, false))
	for i := 0; i < maxSchedulesPerUser-1; i++ {
		q.put(seriesSchedule(user, true))
	}
	svc := newCapDVR(q)
	ctx := context.Background()

	// 99 live + 1 disabled: one more create fits.
	created, err := svc.CreateSchedule(ctx, onceParams(user))
	if err != nil {
		t.Fatalf("create under the cap: %v", err)
	}
	// Now at 100 live: re-enabling the parked rule would make 101.
	if err := svc.SetScheduleEnabled(ctx, parked.ID, true); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("re-enable at the cap: got %v, want ErrInvalidSchedule", err)
	}
	if got, _ := q.GetSchedule(ctx, parked.ID); got.Enabled {
		t.Error("the refused re-enable still flipped the rule on")
	}
	// Disabling is never capped, and frees a slot for the re-enable.
	if err := svc.SetScheduleEnabled(ctx, created.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := svc.SetScheduleEnabled(ctx, parked.ID, true); err != nil {
		t.Errorf("re-enable after freeing a slot: %v", err)
	}
}

// When the querier offers the EPG-aware SQL count it is authoritative: it
// knows a one-off whose programme already aired (but isn't trimmed yet) is
// spent, which the in-Go fallback can't see.
func TestDVRScheduleCap_PrefersSQLCount(t *testing.T) {
	base := newCapFakeQuerier()
	user := uuid.New()
	for i := 0; i < 200; i++ {
		pid := uuid.New() // aired but not yet trimmed: fallback would count these
		base.put(Schedule{UserID: user, Type: ScheduleTypeOnce, ProgramID: &pid, Enabled: true})
	}
	q := &countingCapQuerier{capFakeQuerier: base, live: 3}
	if _, err := newCapDVR(q).CreateSchedule(context.Background(), onceParams(user)); err != nil {
		t.Fatalf("SQL count says 3 live, create refused: %v", err)
	}
	if q.counts != 1 || base.lists != 0 {
		t.Errorf("counts=%d lists=%d; want the SQL count used and the list fallback skipped", q.counts, base.lists)
	}

	q.live = maxSchedulesPerUser
	if _, err := newCapDVR(q).CreateSchedule(context.Background(), onceParams(user)); !errors.Is(err, ErrInvalidSchedule) {
		t.Errorf("SQL count at the cap: got %v, want ErrInvalidSchedule", err)
	}

	// A count failure is a server error, not a silent pass and not a 400.
	q.err = errors.New("db down")
	_, err := newCapDVR(q).CreateSchedule(context.Background(), onceParams(user))
	if err == nil || errors.Is(err, ErrInvalidSchedule) {
		t.Errorf("count failure: got %v, want a non-validation error", err)
	}
}
