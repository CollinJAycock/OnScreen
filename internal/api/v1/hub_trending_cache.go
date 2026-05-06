package v1

import (
	"context"
	"sync"
	"time"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// trendingCache memoizes ListTrending output. Trending is global
// (not user-personalized) so the only input that varies the cached
// shape is the parental rating ceiling — most users share the same
// value (admins → nil, default users → one specific rank), so the
// keyspace is tiny in practice.
//
// Why this exists: the underlying SQL is ~1s on a populated catalog
// (joins watch_events partitions, double-walks media_items for the
// grandparent rollup + title lookup, GROUP BY with COUNT DISTINCT).
// Without caching that runs sequentially after the per-library
// recently-added strips on every hub fetch.
//
// TTL is short enough that newly-watched items surface within
// minutes, long enough to amortize the cost across the dozens of
// hub fetches a single client makes per session.
type trendingCache struct {
	mu      sync.Mutex
	entries map[trendingCacheKey]trendingCacheEntry
	ttl     time.Duration
}

// trendingCacheKey separates "no ceiling" (admin) from "ceiling 0"
// (a rare but valid configured rank) by carrying a presence flag.
// Without it both cases would key on rank=0 and contaminate each
// other.
type trendingCacheKey struct {
	rank int32
	has  bool
}

type trendingCacheEntry struct {
	rows    []gen.ListTrendingRow
	fetched time.Time
}

func newTrendingCache(ttl time.Duration) *trendingCache {
	return &trendingCache{
		entries: map[trendingCacheKey]trendingCacheEntry{},
		ttl:     ttl,
	}
}

func trendingKeyFor(maxRank *int32) trendingCacheKey {
	if maxRank == nil {
		return trendingCacheKey{}
	}
	return trendingCacheKey{rank: *maxRank, has: true}
}

// get returns cached rows if a fresh entry exists for maxRank,
// otherwise calls fetch and stores the result. Errors are not
// cached — a failed fetch retries on the next call.
//
// Lock is held only for the read and the write, not across fetch.
// That admits a small thundering-herd window on simultaneous
// cache-miss requests (each may fire its own query), but the hub
// endpoint is rarely concurrent for a single user, and the second
// caller still benefits from the first one's eventual cache write.
func (c *trendingCache) get(
	ctx context.Context,
	maxRank *int32,
	fetch func(context.Context) ([]gen.ListTrendingRow, error),
) ([]gen.ListTrendingRow, error) {
	k := trendingKeyFor(maxRank)

	c.mu.Lock()
	e, ok := c.entries[k]
	fresh := ok && time.Since(e.fetched) < c.ttl
	c.mu.Unlock()
	if fresh {
		return e.rows, nil
	}

	rows, err := fetch(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.entries[k] = trendingCacheEntry{rows: rows, fetched: time.Now()}
	c.mu.Unlock()
	return rows, nil
}
