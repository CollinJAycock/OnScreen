package v1

import (
	"context"
	"encoding/json"
	"fmt"
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
	overviewCalls  atomic.Int32
	lastTZ         atomic.Value // string
	lastDays       atomic.Int32
	lastClientDays atomic.Int32 // GetStreamTypesByClient's window
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
		{Date: day, Decision: "directStream", Count: 2},
		{Date: day, Decision: "remux", Count: 1},
		{Date: day, Decision: "transcode", Count: 2},
		{Date: day, Decision: "unknown", Count: 5}, // NULL, COALESCEd by the query
		{Date: day, Decision: "bogus", Count: 1},   // outside the allowlist
	}, nil
}
func (f *fakeAnalyticsDB) GetStreamTypesByClient(_ context.Context, days int32) ([]gen.GetStreamTypesByClientRow, error) {
	f.lastClientDays.Store(days)
	return []gen.GetStreamTypesByClientRow{
		{Client: "Fire TV — Amazon AFTKRT", Decision: "unknown", Count: 4},
		{Client: "Fire TV — Amazon AFTKRT", Decision: "directPlay", Count: 1},
		{Client: "Web — Chrome on Windows", Decision: "remux", Count: 2},
		{Client: "Web — Chrome on Windows", Decision: "transcode", Count: 1},
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

	// Stream-type split: directStream+remux→direct_stream, NULL and
	// off-allowlist→unknown, legacy direct = direct_play + direct_stream.
	if len(body.Data.StreamTypesByDay) != 1 {
		t.Fatalf("stream types: got %d days, want 1", len(body.Data.StreamTypesByDay))
	}
	st := body.Data.StreamTypesByDay[0]
	if st.Date != "2026-06-10" || st.DirectPlay != 3 || st.DirectStream != 3 ||
		st.Transcode != 2 || st.Unknown != 6 || st.Direct != 6 {
		t.Fatalf("stream split: got %+v, want direct_play=3 direct_stream=3 transcode=2 unknown=6 direct=6", st)
	}
	if body.Data.RangeDays != 30 {
		t.Fatalf("range_days: got %d, want 30", body.Data.RangeDays)
	}

	// By-client split runs over the requested window, busiest client first.
	if got := db.lastClientDays.Load(); got != 30 {
		t.Fatalf("by-client query got days %d, want 30", got)
	}
	want := []clientStreamTypes{
		{Client: "Fire TV — Amazon AFTKRT", streamSplit: streamSplit{DirectPlay: 1, Unknown: 4}, Total: 5},
		{Client: "Web — Chrome on Windows", streamSplit: streamSplit{DirectStream: 2, Transcode: 1}, Total: 3},
	}
	if len(body.Data.StreamTypesByClient) != len(want) {
		t.Fatalf("stream_types_by_client: got %+v, want %+v", body.Data.StreamTypesByClient, want)
	}
	for i := range want {
		if body.Data.StreamTypesByClient[i] != want[i] {
			t.Fatalf("stream_types_by_client[%d]: got %+v, want %+v", i, body.Data.StreamTypesByClient[i], want[i])
		}
	}
	if got, want := body.Data.StreamTotals, (streamSplit{DirectPlay: 1, DirectStream: 2, Transcode: 1, Unknown: 4}); got != want {
		t.Fatalf("stream_totals: got %+v, want %+v", got, want)
	}

	// Wire shape is the web contract: the embedded split flattens into each
	// object, and the legacy "direct" key survives for older web builds.
	var raw struct {
		Data struct {
			StreamTypesByDay    []map[string]any `json:"stream_types_by_day"`
			StreamTypesByClient []map[string]any `json:"stream_types_by_client"`
			StreamTotals        map[string]any   `json:"stream_totals"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	wantKeys := func(name string, obj map[string]any, keys ...string) {
		t.Helper()
		if len(obj) != len(keys) {
			t.Fatalf("%s: got keys %v, want exactly %v", name, obj, keys)
		}
		for _, k := range keys {
			var ok bool
			switch k {
			case "date", "client":
				_, ok = obj[k].(string)
			default:
				_, ok = obj[k].(float64)
			}
			if !ok {
				t.Fatalf("%s: key %q missing or mistyped in %v", name, k, obj)
			}
		}
	}
	wantKeys("stream_types_by_day[0]", raw.Data.StreamTypesByDay[0],
		"date", "direct_play", "direct_stream", "transcode", "unknown", "direct")
	wantKeys("stream_types_by_client[0]", raw.Data.StreamTypesByClient[0],
		"client", "direct_play", "direct_stream", "transcode", "unknown", "total")
	wantKeys("stream_totals", raw.Data.StreamTotals,
		"direct_play", "direct_stream", "transcode", "unknown")
}

func TestStreamTypesByDay_SplitAndLegacyDirect(t *testing.T) {
	d1 := pgtype.Date{Time: time.Date(2026, 6, 9, 0, 0, 0, 0, time.UTC), Valid: true}
	d2 := pgtype.Date{Time: time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC), Valid: true}
	got := streamTypesByDay([]gen.GetStreamTypesPerDayRow{
		{Date: d1, Decision: "remux", Count: 4},
		{Date: d1, Decision: "unknown", Count: 2},
		{Date: d2, Decision: "directPlay", Count: 5},
		{Date: d2, Decision: "directStream", Count: 1},
		{Date: d2, Decision: "transcode", Count: 7},
	})
	want := []dayStreamTypes{
		{Date: "2026-06-09", streamSplit: streamSplit{DirectStream: 4, Unknown: 2}, Direct: 4},
		{Date: "2026-06-10", streamSplit: streamSplit{DirectPlay: 5, DirectStream: 1, Transcode: 7}, Direct: 6},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d days, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("day %d: got %+v, want %+v", i, got[i], want[i])
		}
	}

	// No plays in the window is an empty array, not JSON null.
	if empty := streamTypesByDay(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("empty input: got %#v, want non-nil empty slice", empty)
	}
}

func TestStreamTypesByClient_TopCutAndTotals(t *testing.T) {
	// Ten clients: "c01" has 1 play … "c10" has 10, each a single decision
	// row, plus an unattributed client whose rows span every bucket.
	var rows []gen.GetStreamTypesByClientRow
	for n := 1; n <= 10; n++ {
		rows = append(rows, gen.GetStreamTypesByClientRow{
			Client: fmt.Sprintf("c%02d", n), Decision: "directPlay", Count: int64(n),
		})
	}
	rows = append(rows,
		gen.GetStreamTypesByClientRow{Client: "Unknown client", Decision: "directStream", Count: 2},
		gen.GetStreamTypesByClientRow{Client: "Unknown client", Decision: "remux", Count: 1},
		gen.GetStreamTypesByClientRow{Client: "Unknown client", Decision: "transcode", Count: 3},
		gen.GetStreamTypesByClientRow{Client: "Unknown client", Decision: "unknown", Count: 3},
	)

	clients, totals := streamTypesByClient(rows)

	if len(clients) != maxStreamTypeClients {
		t.Fatalf("got %d clients, want top %d", len(clients), maxStreamTypeClients)
	}
	// Unknown client (9) ties c09 (9): name breaks the tie, so the order is
	// c10, Unknown client, c09, c08 … c04 — and c03..c01 fall off the cut.
	wantOrder := []string{"c10", "Unknown client", "c09", "c08", "c07", "c06", "c05", "c04"}
	for i, name := range wantOrder {
		if clients[i].Client != name {
			t.Fatalf("clients[%d] = %q, want %q (full: %+v)", i, clients[i].Client, name, clients)
		}
	}
	unk := clients[1]
	if unk.streamSplit != (streamSplit{DirectStream: 3, Transcode: 3, Unknown: 3}) || unk.Total != 9 {
		t.Fatalf("Unknown client split: got %+v, want direct_stream=3 transcode=3 unknown=3 total=9", unk)
	}
	for _, c := range clients {
		if c.Total != c.total() {
			t.Fatalf("%s: total %d != sum of split %d", c.Client, c.Total, c.total())
		}
	}

	// Totals cover every client, including the three cut from the list.
	if want := (streamSplit{DirectPlay: 55, DirectStream: 3, Transcode: 3, Unknown: 3}); totals != want {
		t.Fatalf("totals: got %+v, want %+v", totals, want)
	}

	// Fewer clients than the cap are all kept; none is an empty array.
	if few, _ := streamTypesByClient(rows[:3]); len(few) != 3 {
		t.Fatalf("under the cap: got %d clients, want 3", len(few))
	}
	if none, totals := streamTypesByClient(nil); none == nil || len(none) != 0 || totals != (streamSplit{}) {
		t.Fatalf("empty input: got %#v / %+v, want non-nil empty slice and zero totals", none, totals)
	}
}
