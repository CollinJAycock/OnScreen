package v1

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/watchlimit"
)

type countingWatchLimit struct {
	policyCalls int
	usageCalls  int
	tickCalls   int
	used        int
}

func (c *countingWatchLimit) GetPolicy(_ context.Context, _ uuid.UUID) (watchlimit.Policy, error) {
	c.policyCalls++
	return watchlimit.Policy{DailyLimitMinutes: wlIntPtr(60)}, nil
}
func (c *countingWatchLimit) TodayUsageSeconds(_ context.Context, _ uuid.UUID, _ time.Time) (int, error) {
	c.usageCalls++
	return c.used, nil
}
func (c *countingWatchLimit) AddTick(_ context.Context, _ uuid.UUID, _, _ time.Time) (int, error) {
	c.tickCalls++
	c.used += 10
	return c.used, nil
}

// The gate runs on every byte-serving request now, so the reads behind it must
// not be a DB round trip each time.
func TestWatchLimitMemo_CollapsesRepeatReads(t *testing.T) {
	inner := &countingWatchLimit{}
	m := newWatchLimitMemo(inner)
	uid, day := uuid.New(), watchlimit.LocalDay(time.Now())

	for i := 0; i < 50; i++ {
		if _, err := m.GetPolicy(context.Background(), uid); err != nil {
			t.Fatalf("GetPolicy: %v", err)
		}
		if _, err := m.TodayUsageSeconds(context.Background(), uid, day); err != nil {
			t.Fatalf("TodayUsageSeconds: %v", err)
		}
	}
	if inner.policyCalls != 1 {
		t.Errorf("policy reads: got %d, want 1 — every range request is hitting the DB", inner.policyCalls)
	}
	if inner.usageCalls != 1 {
		t.Errorf("usage reads: got %d, want 1", inner.usageCalls)
	}
}

// Different users must not share an entry.
func TestWatchLimitMemo_KeyedPerUser(t *testing.T) {
	inner := &countingWatchLimit{}
	m := newWatchLimitMemo(inner)
	for i := 0; i < 3; i++ {
		if _, err := m.GetPolicy(context.Background(), uuid.New()); err != nil {
			t.Fatalf("GetPolicy: %v", err)
		}
	}
	if inner.policyCalls != 3 {
		t.Errorf("policy reads: got %d, want 3 (one per distinct user)", inner.policyCalls)
	}
}

// A cached usage row from yesterday must not satisfy today's read — otherwise a
// restricted profile starts the new day already at its cap, or vice versa.
func TestWatchLimitMemo_UsageNotSharedAcrossDays(t *testing.T) {
	inner := &countingWatchLimit{}
	m := newWatchLimitMemo(inner)
	uid := uuid.New()
	today := watchlimit.LocalDay(time.Now())
	yesterday := today.AddDate(0, 0, -1)

	if _, err := m.TodayUsageSeconds(context.Background(), uid, yesterday); err != nil {
		t.Fatalf("TodayUsageSeconds: %v", err)
	}
	if _, err := m.TodayUsageSeconds(context.Background(), uid, today); err != nil {
		t.Fatalf("TodayUsageSeconds: %v", err)
	}
	if inner.usageCalls != 2 {
		t.Errorf("usage reads: got %d, want 2 — a rollover reused yesterday's total", inner.usageCalls)
	}
}

// AddTick is the write that accumulates usage. Memoing it would under-count
// watch time, and it must invalidate the cached total so the gate sees the
// advance immediately rather than a TTL later.
func TestWatchLimitMemo_AddTickPassesThroughAndInvalidates(t *testing.T) {
	inner := &countingWatchLimit{}
	m := newWatchLimitMemo(inner)
	uid, day := uuid.New(), watchlimit.LocalDay(time.Now())

	if _, err := m.TodayUsageSeconds(context.Background(), uid, day); err != nil {
		t.Fatalf("seed read: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := m.AddTick(context.Background(), uid, day, time.Now()); err != nil {
			t.Fatalf("AddTick: %v", err)
		}
	}
	if inner.tickCalls != 5 {
		t.Errorf("ticks: got %d, want 5 — memoing the write under-counts watch time", inner.tickCalls)
	}
	got, err := m.TodayUsageSeconds(context.Background(), uid, day)
	if err != nil {
		t.Fatalf("post-tick read: %v", err)
	}
	if got != inner.used {
		t.Errorf("usage after ticks: got %d, want %d — a stale total lets a capped "+
			"profile keep watching", got, inner.used)
	}
}

// The entry must expire, or a limit change never takes effect.
func TestWatchLimitMemo_EntryExpires(t *testing.T) {
	inner := &countingWatchLimit{}
	m := newWatchLimitMemo(inner).(*watchLimitMemo)
	uid := uuid.New()
	base := time.Now()
	m.nowFunc = func() time.Time { return base }

	if _, err := m.GetPolicy(context.Background(), uid); err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	m.nowFunc = func() time.Time { return base.Add(watchLimitMemoTTL + time.Second) }
	if _, err := m.GetPolicy(context.Background(), uid); err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if inner.policyCalls != 2 {
		t.Errorf("policy reads: got %d, want 2 — the entry never expires, so a "+
			"limit change would never take effect", inner.policyCalls)
	}
}

// nil in, nil out: watchLimitBlocks tests `wl == nil` to mean "feature off", so
// wrapping nil would silently enable a no-op gate.
func TestWatchLimitMemo_NilStaysNil(t *testing.T) {
	if m := newWatchLimitMemo(nil); m != nil {
		t.Error("wrapping a nil store produced a non-nil interface")
	}
}
