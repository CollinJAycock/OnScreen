//go:build integration

// Round-trips the stream-type analytics queries against the real
// watch_plays matview: NULL decisions and NULL client names must surface as
// the 'unknown' / 'Unknown client' sentinels the handler folds on, and the
// by-client window must drop plays older than the requested range.
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestAnalytics_Integration_StreamTypes(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "an-st-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "an-st-lib-"+uuid.New().String()[:8])

	fireTV, web := "Fire TV — Amazon AFTKRT", "Web — Chrome on Windows"
	str := func(s string) *string { return &s }
	now := time.Now()
	plays := []struct {
		client   *string
		decision *string
		at       time.Time
	}{
		{&fireTV, nil, now.Add(-1 * time.Hour)},
		{&fireTV, nil, now.Add(-2 * time.Hour)},
		{&fireTV, str("directPlay"), now.Add(-3 * time.Hour)},
		{&web, str("remux"), now.Add(-4 * time.Hour)},
		{&web, str("directStream"), now.Add(-5 * time.Hour)},
		{&web, str("transcode"), now.Add(-6 * time.Hour)},
		{nil, str("transcode"), now.Add(-7 * time.Hour)},
		// Outside both the 30-day by-client window and the per-day fetch.
		{&fireTV, str("directPlay"), now.AddDate(0, 0, -40)},
	}
	// One media item per play: watch_plays collapses a (user, media) pair's
	// terminal events within 30 minutes, and each row here is its own play.
	for i, p := range plays {
		item := seedMediaItem(ctx, t, q, lib, "Stream Type "+string(rune('A'+i)))
		if _, err := q.InsertWatchEvent(ctx, gen.InsertWatchEventParams{
			UserID:     user,
			MediaID:    item,
			EventType:  "stop",
			PositionMs: 60_000,
			ClientName: p.client,
			Decision:   p.decision,
			OccurredAt: pgtype.Timestamptz{Time: p.at, Valid: true},
		}); err != nil {
			t.Fatalf("InsertWatchEvent %d: %v", i, err)
		}
	}
	if _, err := pool.Exec(ctx, "REFRESH MATERIALIZED VIEW public.watch_plays"); err != nil {
		t.Fatalf("refresh watch_plays: %v", err)
	}

	type key struct{ client, decision string }
	byClient, err := q.GetStreamTypesByClient(ctx, 30)
	if err != nil {
		t.Fatalf("GetStreamTypesByClient: %v", err)
	}
	got := map[key]int64{}
	for _, r := range byClient {
		got[key{r.Client, r.Decision}] += r.Count
	}
	want := map[key]int64{
		{fireTV, "unknown"}:             2,
		{fireTV, "directPlay"}:          1,
		{web, "remux"}:                  1,
		{web, "directStream"}:           1,
		{web, "transcode"}:              1,
		{"Unknown client", "transcode"}: 1,
	}
	if len(got) != len(want) || len(byClient) != len(want) {
		t.Fatalf("by-client rows: got %v (%d rows), want %v", got, len(byClient), want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("by-client %v: got %d, want %d", k, got[k], n)
		}
	}

	perDay, err := q.GetStreamTypesPerDay(ctx, gen.GetStreamTypesPerDayParams{Tz: "UTC", Days: 30})
	if err != nil {
		t.Fatalf("GetStreamTypesPerDay: %v", err)
	}
	// Sum across days — the recent plays can straddle a UTC midnight.
	byDecision := map[string]int64{}
	for _, r := range perDay {
		byDecision[r.Decision] += r.Count
	}
	wantDecisions := map[string]int64{"unknown": 2, "directPlay": 1, "remux": 1, "directStream": 1, "transcode": 2}
	if len(byDecision) != len(wantDecisions) {
		t.Fatalf("per-day decisions: got %v, want %v", byDecision, wantDecisions)
	}
	for d, n := range wantDecisions {
		if byDecision[d] != n {
			t.Errorf("per-day %q: got %d, want %d", d, byDecision[d], n)
		}
	}
}
