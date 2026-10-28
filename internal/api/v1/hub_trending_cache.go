package v1

import (
	"context"
	"sync"
	"time"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/itemlabel"
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
//
// Past the TTL an entry is stale, not gone: for up to trendingStaleFactor
// TTLs a hub fetch gets the stale rows at once and one background query
// refreshes them. Expiring outright made whoever loaded the home page
// after each TTL wait out the whole query (5s+ on QA, where watch_events
// holds a heartbeat row every 10s of playback). Only a missing, invalidated
// or very old entry is fetched on the request path.
type trendingCache struct {
	mu         sync.Mutex
	entries    map[trendingCacheKey]trendingCacheEntry
	refreshing map[trendingCacheKey]bool
	// epoch bumps on invalidate, so a fetch that started before an item edit
	// can't write its pre-edit rows back afterwards.
	epoch uint64
	ttl   time.Duration
}

// trendingStaleFactor × ttl is how long a stale entry may still be served
// while it refreshes in the background (an hour at the 5-minute TTL).
const trendingStaleFactor = 12

// trendingRefreshTimeout bounds a background refresh, which runs detached
// from the request that triggered it.
const trendingRefreshTimeout = 2 * time.Minute

// trendingCacheKey separates "no ceiling" (admin) from "ceiling 0"
// (a rare but valid configured rank) by carrying a presence flag.
// Without it both cases would key on rank=0 and contaminate each
// other. labels is the viewer's label restriction (itemlabel.Filter.Key),
// which filters the rows in SQL just like the ceiling, so two viewers
// differing only in labels must never share an entry.
type trendingCacheKey struct {
	rank   int32
	has    bool
	labels string
}

type trendingCacheEntry struct {
	rows    []gen.ListTrendingRow
	fetched time.Time
}

func newTrendingCache(ttl time.Duration) *trendingCache {
	return &trendingCache{
		entries:    map[trendingCacheKey]trendingCacheEntry{},
		refreshing: map[trendingCacheKey]bool{},
		ttl:        ttl,
	}
}

func trendingKeyFor(maxRank *int32, labels itemlabel.Filter) trendingCacheKey {
	if maxRank == nil {
		return trendingCacheKey{labels: labels.Key()}
	}
	return trendingCacheKey{rank: *maxRank, has: true, labels: labels.Key()}
}

// get returns cached rows if a fresh entry exists for maxRank and labels.
// A stale entry (see trendingCache) is returned as-is while one background
// fetch replaces it; otherwise get calls fetch and stores the result.
// Errors are not cached — a failed fetch retries on the next call.
//
// Lock is held only for the read and the write, not across fetch.
// That admits a small thundering-herd window on simultaneous
// cache-miss requests (each may fire its own query), but the hub
// endpoint is rarely concurrent for a single user, and the second
// caller still benefits from the first one's eventual cache write.
func (c *trendingCache) get(
	ctx context.Context,
	maxRank *int32,
	labels itemlabel.Filter,
	fetch func(context.Context) ([]gen.ListTrendingRow, error),
) ([]gen.ListTrendingRow, error) {
	k := trendingKeyFor(maxRank, labels)

	c.mu.Lock()
	e, ok := c.entries[k]
	age := time.Since(e.fetched)
	epoch := c.epoch
	switch {
	case ok && age < c.ttl:
		c.mu.Unlock()
		return e.rows, nil
	case ok && age < c.ttl*trendingStaleFactor:
		if !c.refreshing[k] {
			c.refreshing[k] = true
			// Detached: the refresh outlives the request that noticed
			// the entry was stale.
			go c.refresh(context.WithoutCancel(ctx), k, epoch, fetch)
		}
		c.mu.Unlock()
		return e.rows, nil
	}
	c.mu.Unlock()

	rows, err := fetch(ctx)
	if err != nil {
		return nil, err
	}
	c.store(k, epoch, rows)
	return rows, nil
}

func (c *trendingCache) refresh(
	ctx context.Context,
	k trendingCacheKey,
	epoch uint64,
	fetch func(context.Context) ([]gen.ListTrendingRow, error),
) {
	ctx, cancel := context.WithTimeout(ctx, trendingRefreshTimeout)
	defer cancel()
	rows, err := fetch(ctx)
	c.mu.Lock()
	delete(c.refreshing, k)
	c.mu.Unlock()
	// A failed refresh keeps serving the stale rows; the next stale hit
	// tries again.
	if err == nil {
		c.store(k, epoch, rows)
	}
}

// store writes rows fetched under epoch, unless an invalidate has
// happened since.
func (c *trendingCache) store(k trendingCacheKey, epoch uint64, rows []gen.ListTrendingRow) {
	c.mu.Lock()
	if c.epoch == epoch {
		c.entries[k] = trendingCacheEntry{rows: rows, fetched: time.Now()}
	}
	c.mu.Unlock()
}

// invalidate drops every cached entry, so the next hub fetch re-reads
// trending. Called after an admin edits an item's metadata: a cached row
// keeps the old title, and, having been rating-filtered when it was
// fetched, could show a re-rated item to a profile it now exceeds.
// Dropped entries are fetched on the request path, never served stale.
func (c *trendingCache) invalidate() {
	c.mu.Lock()
	clear(c.entries)
	c.epoch++
	c.mu.Unlock()
}
