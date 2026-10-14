package v1

import (
	"context"
	"errors"
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

func TestTrendingCache_RefetchAfterTTL(t *testing.T) {
	c := newTrendingCache(10 * time.Millisecond)
	calls := 0
	fetch := func(context.Context) ([]gen.ListTrendingRow, error) {
		calls++
		return nil, nil
	}
	_, _ = c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
	time.Sleep(20 * time.Millisecond)
	_, _ = c.get(context.Background(), nil, itemlabel.Filter{}, fetch)
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
