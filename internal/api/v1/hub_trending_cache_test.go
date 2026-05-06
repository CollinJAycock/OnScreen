package v1

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onscreen/onscreen/internal/db/gen"
)

func TestTrendingCache_HitSkipsFetch(t *testing.T) {
	c := newTrendingCache(time.Minute)
	calls := 0
	fetch := func(context.Context) ([]gen.ListTrendingRow, error) {
		calls++
		return []gen.ListTrendingRow{{Title: "A"}}, nil
	}
	if _, err := c.get(context.Background(), nil, fetch); err != nil {
		t.Fatal(err)
	}
	if _, err := c.get(context.Background(), nil, fetch); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 fetch, got %d", calls)
	}
}

func TestTrendingCache_RefetchAfterTTL(t *testing.T) {
	c := newTrendingCache(10 * time.Millisecond)
	calls := 0
	fetch := func(context.Context) ([]gen.ListTrendingRow, error) {
		calls++
		return nil, nil
	}
	_, _ = c.get(context.Background(), nil, fetch)
	time.Sleep(20 * time.Millisecond)
	_, _ = c.get(context.Background(), nil, fetch)
	if calls != 2 {
		t.Fatalf("expected refetch after TTL, got %d calls", calls)
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
	_, _ = c.get(context.Background(), nil, fetch)   // admin
	_, _ = c.get(context.Background(), &zero, fetch) // rank=0
	_, _ = c.get(context.Background(), nil, fetch)   // admin again — cached
	_, _ = c.get(context.Background(), &zero, fetch) // rank=0 again — cached
	if calls != 2 {
		t.Fatalf("expected 2 fetches (one per key), got %d", calls)
	}
}

func TestTrendingCache_FetchErrorNotCached(t *testing.T) {
	c := newTrendingCache(time.Minute)
	calls := 0
	fetch := func(context.Context) ([]gen.ListTrendingRow, error) {
		calls++
		return nil, errors.New("boom")
	}
	_, err := c.get(context.Background(), nil, fetch)
	if err == nil {
		t.Fatal("expected error on first call")
	}
	_, err = c.get(context.Background(), nil, fetch)
	if err == nil {
		t.Fatal("expected error on second call (failed fetch should not have been cached)")
	}
	if calls != 2 {
		t.Fatalf("expected refetch after error, got %d calls", calls)
	}
}
