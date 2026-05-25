package v1

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// fakeAnalyticsDB satisfies analyticsQuerier and counts how many times the
// (representative) overview query runs, so a test can tell a real compute from
// a cache hit.
type fakeAnalyticsDB struct{ overviewCalls atomic.Int32 }

func (f *fakeAnalyticsDB) GetAnalyticsOverview(context.Context) (gen.AnalyticsOverviewRow, error) {
	f.overviewCalls.Add(1)
	return gen.AnalyticsOverviewRow{TotalItems: 1}, nil
}
func (f *fakeAnalyticsDB) GetLibraryAnalytics(context.Context) ([]gen.LibraryAnalyticsRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetVideoCodecBreakdown(context.Context) ([]gen.CodecCountRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetContainerBreakdown(context.Context) ([]gen.ContainerCountRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetPlaysPerDay(context.Context) ([]gen.DayCountRow, error) { return nil, nil }
func (f *fakeAnalyticsDB) GetBandwidthPerDay(context.Context) ([]gen.DayBytesRow, error) {
	return nil, nil
}
func (f *fakeAnalyticsDB) GetTopPlayed(context.Context) ([]gen.TopPlayedRow, error) { return nil, nil }
func (f *fakeAnalyticsDB) GetRecentPlays(context.Context) ([]gen.RecentPlayRow, error) {
	return nil, nil
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

	// ?refresh=true bypasses the cache and recomputes.
	if code := do("/analytics?refresh=true"); code != http.StatusOK {
		t.Fatalf("refresh call status: got %d, want 200", code)
	}
	if got := db.overviewCalls.Load(); got != 2 {
		t.Fatalf("expected refresh=true to recompute (2 total), got %d", got)
	}
}
