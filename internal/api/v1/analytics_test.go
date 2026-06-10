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
// a cache hit. lastTZ records what zone the per-day queries received.
type fakeAnalyticsDB struct {
	overviewCalls atomic.Int32
	lastTZ        atomic.Value // string
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
func (f *fakeAnalyticsDB) GetPlaysPerDay(_ context.Context, tz string) ([]gen.GetPlaysPerDayRow, error) {
	f.lastTZ.Store(tz)
	return nil, nil
}
func (f *fakeAnalyticsDB) GetBandwidthPerDay(_ context.Context, tz string) ([]gen.GetBandwidthPerDayRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetTopPlayed(context.Context) ([]gen.GetTopPlayedRow, error) {
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

	// ?refresh=true bypasses the cache and recomputes.
	if code := do("/analytics?refresh=true"); code != http.StatusOK {
		t.Fatalf("refresh call status: got %d, want 200", code)
	}
	if got := db.overviewCalls.Load(); got != 3 {
		t.Fatalf("expected refresh=true to recompute (3 total), got %d", got)
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

func TestAnalytics_RecentPlayTimesAreUTC(t *testing.T) {
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
	if len(body.Data.RecentPlays) != 1 {
		t.Fatalf("recent plays: got %d, want 1", len(body.Data.RecentPlays))
	}
	// 10:44:47 EDT (UTC-4) is 14:44:47 UTC.
	if got := body.Data.RecentPlays[0].OccurredAt; got != "2026-06-10T14:44:47Z" {
		t.Fatalf("occurred_at: got %q, want 2026-06-10T14:44:47Z", got)
	}
}
