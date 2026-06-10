package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// fakeAnalyticsDB satisfies analyticsQuerier and counts how many times the
// (representative) overview query runs, so a test can tell a real compute from
// a cache hit. lastTZ / lastDays record what the windowed queries received.
type fakeAnalyticsDB struct {
	overviewCalls atomic.Int32
	lastTZ        atomic.Value // string
	lastDays      atomic.Int32
}

func (f *fakeAnalyticsDB) GetAnalyticsOverview(context.Context) (gen.GetAnalyticsOverviewRow, error) {
	f.overviewCalls.Add(1)
	return gen.GetAnalyticsOverviewRow{TotalItems: 1}, nil
}
func (f *fakeAnalyticsDB) GetLibraryAnalytics(context.Context) ([]gen.GetLibraryAnalyticsRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetVideoCodecBreakdown(context.Context) ([]gen.GetVideoCodecBreakdownRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetContainerBreakdown(context.Context) ([]gen.GetContainerBreakdownRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetPlaysPerDay(_ context.Context, arg gen.GetPlaysPerDayParams) ([]gen.GetPlaysPerDayRow, error) {
	f.lastTZ.Store(arg.Tz)
	f.lastDays.Store(arg.Days)
	return nil, nil
}
func (f *fakeAnalyticsDB) GetBandwidthPerDay(context.Context, gen.GetBandwidthPerDayParams) ([]gen.GetBandwidthPerDayRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetTopPlayed(context.Context, int32) ([]gen.GetTopPlayedRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetRecentPlays(context.Context) ([]gen.GetRecentPlaysRow, error) {
	// Local-zone timestamp: the handler must emit it as UTC RFC 3339, not
	// stamp the local wall-clock with a literal Z (the bug this guards).
	loc := time.FixedZone("EDT", -4*3600)
	return []gen.GetRecentPlaysRow{{
		Title:      "Bird Box",
		Type:       "movie",
		OccurredAt: pgtype.Timestamptz{Time: time.Date(2026, 6, 10, 10, 44, 47, 0, loc), Valid: true},
	}}, nil
}
func (f *fakeAnalyticsDB) GetTopUsers(context.Context, int32) ([]gen.GetTopUsersRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetClientBreakdown(context.Context, int32) ([]gen.GetClientBreakdownRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetPlaysByHour(context.Context, gen.GetPlaysByHourParams) ([]gen.GetPlaysByHourRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetCompletionStats(context.Context, int32) (gen.GetCompletionStatsRow, error) {
	return gen.GetCompletionStatsRow{PlaysWithDuration: 10, Completed: 7}, nil
}
func (f *fakeAnalyticsDB) GetStreamTypesPerDay(context.Context, gen.GetStreamTypesPerDayParams) ([]gen.GetStreamTypesPerDayRow, error) {
	day := pgtype.Date{Time: time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC), Valid: true}
	return []gen.GetStreamTypesPerDayRow{
		{Date: day, Decision: "directPlay", Count: 3},
		{Date: day, Decision: "remux", Count: 1},
		{Date: day, Decision: "transcode", Count: 2},
		{Date: day, Decision: "unknown", Count: 5},
	}, nil
}

func TestAnalytics_CachesResponse(t *testing.T) {
	db := &fakeAnalyticsDB{}
	h := NewAnalyticsHandler(db, slog.Default())

	do := func(target string) int {
		rec := httptest.NewRecorder()
		h.Get(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec.Code
	}

	// First call computes (1 DB hit); second within TTL is served from cache.
	if code := do("/analytics"); code != http.StatusOK {
		t.Fatalf("first call status: got %d, want 200", code)
	}
	if code := do("/analytics"); code != http.StatusOK {
		t.Fatalf("cached call status: got %d, want 200", code)
	}
	if got := db.overviewCalls.Load(); got != 1 {
		t.Fatalf("expected 1 DB compute across two calls (second cached), got %d", got)
	}

	// A different display timezone is a different cache key.
	if code := do("/analytics?tz=America/Detroit"); code != http.StatusOK {
		t.Fatalf("tz call status: got %d, want 200", code)
	}
	if got := db.overviewCalls.Load(); got != 2 {
		t.Fatalf("expected per-tz cache miss to recompute (2 total), got %d", got)
	}
	if got := db.lastTZ.Load(); got != "America/Detroit" {
		t.Fatalf("per-day queries got tz %q, want America/Detroit", got)
	}

	// A different range is a different cache key, and clamps to presets.
	if code := do("/analytics?days=7"); code != http.StatusOK {
		t.Fatalf("days call status: got %d, want 200", code)
	}
	if got := db.overviewCalls.Load(); got != 3 {
		t.Fatalf("expected per-range cache miss to recompute (3 total), got %d", got)
	}
	if got := db.lastDays.Load(); got != 7 {
		t.Fatalf("windowed queries got days %d, want 7", got)
	}
	// Unrecognized values clamp to 30 (refresh bypasses the warm 30-day
	// cache entry so the windowed query actually re-runs).
	if code := do("/analytics?days=12345&refresh=true"); code != http.StatusOK {
		t.Fatalf("clamped days status: got %d, want 200", code)
	}
	if got := db.lastDays.Load(); got != 30 {
		t.Fatalf("unrecognized days should clamp to 30, got %d", got)
	}

	// ?refresh=true bypasses the cache and recomputes.
	before := db.overviewCalls.Load()
	if code := do("/analytics?refresh=true"); code != http.StatusOK {
		t.Fatalf("refresh call status: got %d, want 200", code)
	}
	if got := db.overviewCalls.Load(); got != before+1 {
		t.Fatalf("expected refresh=true to recompute (%d total), got %d", before+1, got)
	}
}

func TestAnalytics_InvalidTZFallsBackToUTC(t *testing.T) {
	db := &fakeAnalyticsDB{}
	h := NewAnalyticsHandler(db, slog.Default())

	rec := httptest.NewRecorder()
	h.Get(rec, httptest.NewRequest(http.MethodGet, "/analytics?tz=Not/AZone", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if got := db.lastTZ.Load(); got != "UTC" {
		t.Fatalf("invalid tz should fall back to UTC, got %q", got)
	}
}

func TestAnalytics_ResponseShape(t *testing.T) {
	db := &fakeAnalyticsDB{}
	h := NewAnalyticsHandler(db, slog.Default())

	rec := httptest.NewRecorder()
	h.Get(rec, httptest.NewRequest(http.MethodGet, "/analytics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var body struct {
		Data analyticsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Recent-play times are UTC: 10:44:47 EDT (UTC-4) is 14:44:47 UTC.
	if len(body.Data.RecentPlays) != 1 {
		t.Fatalf("recent plays: got %d, want 1", len(body.Data.RecentPlays))
	}
	if got := body.Data.RecentPlays[0].OccurredAt; got != "2026-06-10T14:44:47Z" {
		t.Fatalf("occurred_at: got %q, want 2026-06-10T14:44:47Z", got)
	}

	// Completion passthrough.
	if body.Data.Completion.Plays != 10 || body.Data.Completion.Completed != 7 {
		t.Fatalf("completion: got %+v, want plays=10 completed=7", body.Data.Completion)
	}

	// Stream-type collapse: directPlay+remux→direct, transcode, unknown.
	if len(body.Data.StreamTypesByDay) != 1 {
		t.Fatalf("stream types: got %d days, want 1", len(body.Data.StreamTypesByDay))
	}
	st := body.Data.StreamTypesByDay[0]
	if st.Direct != 4 || st.Transcode != 2 || st.Unknown != 5 {
		t.Fatalf("stream split: got %+v, want direct=4 transcode=2 unknown=5", st)
	}
	if body.Data.RangeDays != 30 {
		t.Fatalf("range_days: got %d, want 30", body.Data.RangeDays)
	}
}
