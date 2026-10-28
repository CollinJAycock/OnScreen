package v1

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/itemlabel"
)

func TestTrendingCache_HitSkipsFetch(t *testing.T) {
	c := newTrendingCache(time.Minute)
	calls := 0
	fetch := func(context.Context) ([]gen.ListTrendingRow, error) {
		calls++
		return []gen.ListTrendingRow{{Title: "A"}}, nil
	}
	if _, err := c.get(context.Background(), nil, itemlabel.Filter{}, fetch); err != nil {
		t.Fatal(err)
	}
	if _, err := c.get(context.Background(), nil, itemlabel.Filter{}, fetch); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 fetch, got %d", calls)
	}
}

func TestTrendingCache_StaleServedWhileRefreshing(t *testing.T) {
	// Past the TTL the old rows come back at once and one background fetch
	// replaces them — the hub fetch that noticed must not wait on the query.
	c := newTrendingCache(50 * time.Millisecond)
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func(context.Context) ([]gen.ListTrendingRow, error) {
		if calls.Add(1) > 1 {
			<-release
			return []gen.ListTrendingRow{{Title: "new"}}, nil
		}
		return []gen.ListTrendingRow{{Title: "old"}}, nil
	}
	_, _ = c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
	time.Sleep(60 * time.Millisecond)

	for i := range 3 { // several stale hits start only one refresh
		rows, err := c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Title != "old" {
			t.Fatalf("stale hit %d: want the old rows, got %+v", i, rows)
		}
	}
	close(release)
	waitFor(t, func() bool {
		rows, _ := c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
		return len(rows) == 1 && rows[0].Title == "new"
	})
	if n := calls.Load(); n != 2 {
		t.Fatalf("expected 1 initial + 1 background fetch, got %d", n)
	}
}

func TestTrendingCache_VeryOldEntryFetchedInline(t *testing.T) {
	// Past the stale window the entry is not served at all.
	c := newTrendingCache(time.Millisecond)
	calls := 0
	fetch := func(context.Context) ([]gen.ListTrendingRow, error) {
		calls++
		return []gen.ListTrendingRow{{Title: "v" + string(rune('0'+calls))}}, nil
	}
	_, _ = c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
	time.Sleep(trendingStaleFactor*time.Millisecond + 10*time.Millisecond)
	rows, _ := c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
	if calls != 2 || rows[0].Title != "v2" {
		t.Fatalf("expected an inline refetch returning v2, got %d calls, rows %+v", calls, rows)
	}
}

func TestTrendingCache_ZeroTTLAlwaysFetches(t *testing.T) {
	c := newTrendingCache(0)
	calls := 0
	fetch := func(context.Context) ([]gen.ListTrendingRow, error) {
		calls++
		return nil, nil
	}
	_, _ = c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
	_, _ = c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
	if calls != 2 {
		t.Fatalf("expected a fetch per call with TTL 0, got %d", calls)
	}
}

func TestTrendingCache_InvalidateDropsInFlightRefresh(t *testing.T) {
	// A refresh that started before an item edit must not write its
	// pre-edit rows over the invalidation, and an invalidated entry is
	// fetched inline rather than served stale.
	c := newTrendingCache(50 * time.Millisecond)
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func(context.Context) ([]gen.ListTrendingRow, error) {
		switch calls.Add(1) {
		case 1:
			return []gen.ListTrendingRow{{Title: "first"}}, nil
		case 2:
			<-release
			return []gen.ListTrendingRow{{Title: "pre-edit"}}, nil
		default:
			return []gen.ListTrendingRow{{Title: "post-edit"}}, nil
		}
	}
	_, _ = c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
	time.Sleep(60 * time.Millisecond)
	_, _ = c.get(context.Background(), nil, itemlabel.Filter{}, fetch) // starts refresh #2
	waitFor(t, func() bool { return calls.Load() == 2 })               // #2 is now blocked in fetch
	c.invalidate()
	rows, _ := c.get(context.Background(), nil, itemlabel.Filter{}, fetch) // inline #3
	if rows[0].Title != "post-edit" {
		t.Fatalf("after invalidate want post-edit rows inline, got %+v", rows)
	}
	close(release)
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return !c.refreshing[trendingKeyFor(nil, itemlabel.Filter{})]
	})
	rows, _ = c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
	if rows[0].Title != "post-edit" {
		t.Fatalf("stale refresh overwrote the invalidated entry: %+v", rows)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 2s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTrendingCache_RatingRankSeparatesEntries(t *testing.T) {
	// Admins (nil) and rating-0 users must NOT share a cache entry —
	// rating=0 is a valid configured ceiling, not "unrestricted."
	c := newTrendingCache(time.Minute)
	calls := 0
	fetch := func(context.Context) ([]gen.ListTrendingRow, error) {
		calls++
		return nil, nil
	}
	zero := int32(0)
	_, _ = c.get(context.Background(), nil, itemlabel.Filter{}, fetch)   // admin
	_, _ = c.get(context.Background(), &zero, itemlabel.Filter{}, fetch) // rank=0
	_, _ = c.get(context.Background(), nil, itemlabel.Filter{}, fetch)   // admin again — cached
	_, _ = c.get(context.Background(), &zero, itemlabel.Filter{}, fetch) // rank=0 again — cached
	if calls != 2 {
		t.Fatalf("expected 2 fetches (one per key), got %d", calls)
	}
}

func TestTrendingCache_LabelRestrictionSeparatesEntries(t *testing.T) {
	// The label restriction filters the rows in SQL like the ceiling, so an
	// unrestricted viewer's cached trending must never be served to a
	// label-restricted one (or one restriction's to another's).
	c := newTrendingCache(time.Minute)
	calls := 0
	fetch := func(context.Context) ([]gen.ListTrendingRow, error) {
		calls++
		return nil, nil
	}
	horror := itemlabel.Filter{Blocked: []string{"horror"}}
	kids := itemlabel.Filter{Allowed: []string{"kids"}}
	_, _ = c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
	_, _ = c.get(context.Background(), nil, horror, fetch)
	_, _ = c.get(context.Background(), nil, kids, fetch)
	_, _ = c.get(context.Background(), nil, horror, fetch) // cached
	if calls != 3 {
		t.Fatalf("expected 3 fetches (one per restriction), got %d", calls)
	}
}

func TestTrendingCache_FetchErrorNotCached(t *testing.T) {
	c := newTrendingCache(time.Minute)
	calls := 0
	fetch := func(context.Context) ([]gen.ListTrendingRow, error) {
		calls++
		return nil, errors.New("boom")
	}
	_, err := c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
	if err == nil {
		t.Fatal("expected error on first call")
	}
	_, err = c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
	if err == nil {
		t.Fatal("expected error on second call (failed fetch should not have been cached)")
	}
	if calls != 2 {
		t.Fatalf("expected refetch after error, got %d calls", calls)
	}
}
