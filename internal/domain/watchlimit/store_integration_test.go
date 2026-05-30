//go:build integration

// Integration coverage for the raw-SQL watchlimit.Store against a real
// PostgreSQL container (internal/testdb). These exercise the parts pure unit
// tests can't reach: the ON CONFLICT upserts in SetPolicy, the
// ErrNoRows→zero-value branches in GetPolicy / TodayUsageSeconds, and — most
// importantly — the clamped-delta accumulator arithmetic in AddTick
// (LEAST(GREATEST(EXTRACT(EPOCH ...)::int, 0), maxTickSeconds)), which is the
// load-bearing SQL that keeps the daily counter tracking active watching
// rather than elapsed wall-clock time.
//
// Run with: make test-int
//
//	(or: go test -tags integration -run Integration ./internal/domain/watchlimit/)
package watchlimit

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// seedWatchUser inserts a real users row — the policy + usage tables FK to it
// (ON DELETE CASCADE) — and returns its id. Uses the production CreateUser
// query so the fixture matches a real account.
func seedWatchUser(ctx context.Context, t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	hash := "placeholder-bcrypt-hash"
	u, err := gen.New(pool).CreateUser(ctx, gen.CreateUserParams{
		Username:     "wl-" + uuid.New().String()[:8],
		PasswordHash: &hash,
		IsAdmin:      false,
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u.ID
}

// assertPolicyEq compares the three nullable policy fields, treating nil and a
// value as distinct (a cleared field must read back nil, not 0).
func assertPolicyEq(t *testing.T, got, want Policy) {
	t.Helper()
	check := func(name string, g, w *int) {
		switch {
		case g == nil && w == nil:
		case g == nil || w == nil:
			t.Errorf("%s: got %v, want %v", name, g, w)
		case *g != *w:
			t.Errorf("%s: got %d, want %d", name, *g, *w)
		}
	}
	check("daily_limit_minutes", got.DailyLimitMinutes, want.DailyLimitMinutes)
	check("allowed_start_minute", got.AllowedStartMinute, want.AllowedStartMinute)
	check("allowed_end_minute", got.AllowedEndMinute, want.AllowedEndMinute)
}

// A missing policy row is the unrestricted zero value, not an error — callers
// rely on this to skip the usage read entirely for unrestricted users.
func TestStore_Integration_GetPolicyMissingIsUnrestricted(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := NewStore(pool)
	uid := seedWatchUser(ctx, t, pool)

	p, err := s.GetPolicy(ctx, uid)
	if err != nil {
		t.Fatalf("GetPolicy (missing row): %v", err)
	}
	if p.Restricted() {
		t.Errorf("missing policy should be unrestricted, got %+v", p)
	}
}

// SetPolicy must round-trip through both the INSERT and the ON CONFLICT DO
// UPDATE branches, and an all-nil policy must clear every limit while leaving
// the row present.
func TestStore_Integration_SetPolicyUpsertRoundTrip(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := NewStore(pool)
	uid := seedWatchUser(ctx, t, pool)

	// Insert path.
	want := Policy{DailyLimitMinutes: intp(120), AllowedStartMinute: intp(480), AllowedEndMinute: intp(1200)}
	if err := s.SetPolicy(ctx, uid, want); err != nil {
		t.Fatalf("SetPolicy (insert): %v", err)
	}
	got, err := s.GetPolicy(ctx, uid)
	if err != nil {
		t.Fatalf("GetPolicy after insert: %v", err)
	}
	assertPolicyEq(t, got, want)

	// Update path: change the cap and clear the allowed-hours window.
	want2 := Policy{DailyLimitMinutes: intp(30)}
	if err := s.SetPolicy(ctx, uid, want2); err != nil {
		t.Fatalf("SetPolicy (update): %v", err)
	}
	got2, err := s.GetPolicy(ctx, uid)
	if err != nil {
		t.Fatalf("GetPolicy after update: %v", err)
	}
	assertPolicyEq(t, got2, want2)
	if got2.AllowedStartMinute != nil || got2.AllowedEndMinute != nil {
		t.Errorf("update should have cleared the window, got start=%v end=%v",
			got2.AllowedStartMinute, got2.AllowedEndMinute)
	}

	// Clear-all path: an all-nil policy is "remove limits".
	if err := s.SetPolicy(ctx, uid, Policy{}); err != nil {
		t.Fatalf("SetPolicy (clear): %v", err)
	}
	got3, err := s.GetPolicy(ctx, uid)
	if err != nil {
		t.Fatalf("GetPolicy after clear: %v", err)
	}
	if got3.Restricted() {
		t.Errorf("cleared policy should be unrestricted, got %+v", got3)
	}
}

// A day with no usage row reads as 0, not ErrNoRows.
func TestStore_Integration_TodayUsageMissingIsZero(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := NewStore(pool)
	uid := seedWatchUser(ctx, t, pool)

	used, err := s.TodayUsageSeconds(ctx, uid, LocalDay(time.Now()))
	if err != nil {
		t.Fatalf("TodayUsageSeconds (missing row): %v", err)
	}
	if used != 0 {
		t.Errorf("missing usage row should be 0, got %d", used)
	}
}

// AddTick accumulates the clamped wall-clock delta between ticks: the first
// tick only establishes last_tick_at, a small gap adds in full, and a large
// gap (paused / backgrounded player) is capped at maxTickSeconds.
func TestStore_Integration_AddTickAccumulatesClampedDelta(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := NewStore(pool)
	uid := seedWatchUser(ctx, t, pool)

	day := LocalDay(time.Now())
	t0 := time.Now()

	// First tick of the day just establishes last_tick_at (adds 0).
	total, err := s.AddTick(ctx, uid, day, t0)
	if err != nil {
		t.Fatalf("AddTick #1: %v", err)
	}
	if total != 0 {
		t.Errorf("first tick total = %d, want 0", total)
	}

	// A +10s gap is under the 30s clamp, so the full 10s is added.
	total, err = s.AddTick(ctx, uid, day, t0.Add(10*time.Second))
	if err != nil {
		t.Fatalf("AddTick #2: %v", err)
	}
	if total != 10 {
		t.Errorf("after +10s total = %d, want 10", total)
	}

	// A +10min gap is clamped to maxTickSeconds (30), not the full 600s.
	total, err = s.AddTick(ctx, uid, day, t0.Add(10*time.Second+10*time.Minute))
	if err != nil {
		t.Fatalf("AddTick #3: %v", err)
	}
	if total != 40 {
		t.Errorf("after clamped big gap total = %d, want 40 (10 + clamp 30)", total)
	}

	// The accumulated total is what the progress gate reads back.
	used, err := s.TodayUsageSeconds(ctx, uid, day)
	if err != nil {
		t.Fatalf("TodayUsageSeconds: %v", err)
	}
	if used != 40 {
		t.Errorf("TodayUsageSeconds = %d, want 40", used)
	}
}

// A tick whose timestamp predates the previous one (clock skew / reordering)
// must not subtract accrued time — GREATEST(delta, 0) floors the increment.
func TestStore_Integration_AddTickNegativeDeltaFlooredAtZero(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := NewStore(pool)
	uid := seedWatchUser(ctx, t, pool)

	day := LocalDay(time.Now())
	t0 := time.Now()

	if _, err := s.AddTick(ctx, uid, day, t0); err != nil {
		t.Fatalf("AddTick #1: %v", err)
	}
	total, err := s.AddTick(ctx, uid, day, t0.Add(-5*time.Second))
	if err != nil {
		t.Fatalf("AddTick #2 (negative delta): %v", err)
	}
	if total != 0 {
		t.Errorf("negative-delta tick total = %d, want 0", total)
	}
}

// Usage is keyed by (user_id, day): two different local days accumulate
// independently, which is what resets "today's" cap at midnight.
func TestStore_Integration_AddTickPerDayIsolation(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	s := NewStore(pool)
	uid := seedWatchUser(ctx, t, pool)

	day1 := time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local)
	day2 := time.Date(2026, 1, 3, 0, 0, 0, 0, time.Local)
	base := time.Date(2026, 1, 2, 12, 0, 0, 0, time.Local)

	// day1: two ticks 10s apart → 0 then 10.
	if _, err := s.AddTick(ctx, uid, day1, base); err != nil {
		t.Fatalf("day1 tick #1: %v", err)
	}
	if _, err := s.AddTick(ctx, uid, day1, base.Add(10*time.Second)); err != nil {
		t.Fatalf("day1 tick #2: %v", err)
	}
	// day2: a single establishing tick → 0.
	if _, err := s.AddTick(ctx, uid, day2, base.Add(24*time.Hour)); err != nil {
		t.Fatalf("day2 tick #1: %v", err)
	}

	used1, err := s.TodayUsageSeconds(ctx, uid, day1)
	if err != nil {
		t.Fatalf("usage day1: %v", err)
	}
	used2, err := s.TodayUsageSeconds(ctx, uid, day2)
	if err != nil {
		t.Fatalf("usage day2: %v", err)
	}
	if used1 != 10 {
		t.Errorf("day1 usage = %d, want 10", used1)
	}
	if used2 != 0 {
		t.Errorf("day2 usage = %d, want 0 (separate day, single establishing tick)", used2)
	}
}
