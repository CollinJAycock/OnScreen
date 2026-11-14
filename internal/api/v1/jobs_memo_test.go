package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"
)

// countingJobsCounters counts reads, to show the memo collapses polls.
type countingJobsCounters struct {
	stubJobsCounters
	reads int
}

func (c *countingJobsCounters) CountItemsMissingArt(ctx context.Context) (int, error) {
	c.reads++
	return c.stubJobsCounters.CountItemsMissingArt(ctx)
}

func (c *countingJobsCounters) CountUnmatchedItems(ctx context.Context) (int, error) {
	c.reads++
	return c.stubJobsCounters.CountUnmatchedItems(ctx)
}

// Every open admin tab polls /jobs every 30 s; the counters are read at most
// once per jobsCountersTTL however many polls arrive, and a failed read is
// retried on the next poll rather than remembered.
func TestJobsHandler_CountersMemoized(t *testing.T) {
	counters := &countingJobsCounters{stubJobsCounters: stubJobsCounters{missingArt: 7, unmatched: 2}}
	h := NewJobsHandler(&stubScanLister{}, counters, nil, slog.Default())
	now := time.Unix(1_700_000_000, 0)
	h.now = func() time.Time { return now }
	get := func() (int, int) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.Get(rec, adminJobsRequest())
		data := decodeData(t, rec)
		return int(data.Data["missing_art_count"].(float64)), int(data.Data["unmatched_count"].(float64))
	}

	for i := 0; i < 5; i++ {
		if ma, um := get(); ma != 7 || um != 2 {
			t.Fatalf("counts = %d/%d, want 7/2", ma, um)
		}
	}
	if counters.reads != 2 {
		t.Fatalf("reads = %d after 5 polls inside the TTL, want 2 (one per counter)", counters.reads)
	}

	counters.missingArt = 3
	now = now.Add(jobsCountersTTL)
	if ma, _ := get(); ma != 3 {
		t.Fatalf("missing art after TTL = %d, want 3", ma)
	}
	if counters.reads != 4 {
		t.Fatalf("reads = %d, want 4 after the TTL lapsed", counters.reads)
	}

	counters.unmatchedErr = errors.New("db blip")
	now = now.Add(jobsCountersTTL)
	if _, um := get(); um != 0 {
		t.Fatalf("failed read should degrade to 0, got %d", um)
	}
	counters.unmatchedErr = nil
	if _, um := get(); um != 2 {
		t.Fatalf("failed read must not be memoized; got %d, want 2", um)
	}
}
