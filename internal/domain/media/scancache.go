package media

import (
	"container/list"
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

// DefaultScanCacheSize bounds a scan's item cache. An entry is a lookup key
// (~100–200 bytes) plus an ID, so the cap holds memory well under a few MB
// even on huge libraries; the scanner walks files in path order, so a show's
// or album's files arrive together and LRU keeps the parents that matter.
const DefaultScanCacheSize = 4096

// ScanCache remembers, for one scan run, which item each FindOrCreateItem /
// FindOrCreateHierarchyItem lookup resolved to. A TV scan resolves the same
// show and season for every episode file, and a music scan the same artist
// and album for every track; without the cache each of those repeats the
// title lookups (exact, flexible, full-text search, folder fallback) or lists
// the parent's children again.
//
// Only the key → ID mapping is cached, never the row. A hit re-reads the row
// by primary key and re-checks that it still matches (not deleted, same
// library, type and parent, and for a parented item still the same index /
// title / disc match), so callers see exactly the fresh row an uncached lookup
// would give them, and an item merged away or soft-deleted mid-scan (an
// enrichment merge, a dedupe heal) falls through to the full lookup instead of
// collecting files. Items the scan creates are added as they are created.
//
// Safe for concurrent use by the scan's worker goroutines. Attach one per
// scan run with WithScanCache; without one, lookups behave exactly as before.
type ScanCache struct {
	mu    sync.Mutex
	max   int
	ll    *list.List // front = most recently used; values are *scanCacheEntry
	items map[string]*list.Element

	hits, misses, stale atomic.Int64
}

type scanCacheEntry struct {
	key string
	id  uuid.UUID
}

// NewScanCache returns an empty cache holding at most max entries
// (DefaultScanCacheSize when max <= 0).
func NewScanCache(max int) *ScanCache {
	if max <= 0 {
		max = DefaultScanCacheSize
	}
	return &ScanCache{max: max, ll: list.New(), items: make(map[string]*list.Element)}
}

type scanCacheCtxKey struct{}

// WithScanCache returns ctx carrying c. FindOrCreateItem and
// FindOrCreateHierarchyItem called with the returned context use the cache.
func WithScanCache(ctx context.Context, c *ScanCache) context.Context {
	return context.WithValue(ctx, scanCacheCtxKey{}, c)
}

func scanCacheFrom(ctx context.Context) *ScanCache {
	c, _ := ctx.Value(scanCacheCtxKey{}).(*ScanCache)
	return c
}

// ScanCacheStats is a cache's hit/miss accounting for one scan.
type ScanCacheStats struct {
	Hits   int64 // resolved from the cache (one primary-key read)
	Misses int64 // full lookup ran
	Stale  int64 // cached item no longer matched; dropped and looked up again
	Size   int   // entries held now
}

// Stats reports the cache's counters.
func (c *ScanCache) Stats() ScanCacheStats {
	c.mu.Lock()
	n := c.ll.Len()
	c.mu.Unlock()
	return ScanCacheStats{Hits: c.hits.Load(), Misses: c.misses.Load(), Stale: c.stale.Load(), Size: n}
}

func (c *ScanCache) get(key string) (uuid.UUID, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return uuid.Nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*scanCacheEntry).id, true
}

func (c *ScanCache) put(key string, id uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value.(*scanCacheEntry).id = id
		c.ll.MoveToFront(el)
		return
	}
	c.items[key] = c.ll.PushFront(&scanCacheEntry{key: key, id: id})
	for c.ll.Len() > c.max {
		oldest := c.ll.Back()
		c.ll.Remove(oldest)
		delete(c.items, oldest.Value.(*scanCacheEntry).key)
	}
}

func (c *ScanCache) drop(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.Remove(el)
		delete(c.items, key)
	}
}

// Lookup kinds: the two FindOrCreate paths resolve differently, so the same
// params must not share an entry across them.
const (
	scanKindItem      = 'i' // FindOrCreateItem → findItemByTitle
	scanKindHierarchy = 'h' // FindOrCreateHierarchyItem → findHierarchyItem
)

// scanCacheKey is every input the lookup reads, so two calls share an entry
// only when an uncached lookup would treat them identically.
func scanCacheKey(kind byte, p CreateItemParams) string {
	var b strings.Builder
	b.Grow(128 + len(p.Title) + len(p.FolderPath))
	b.WriteByte(kind)
	field := func(s string) {
		b.WriteByte(0)
		b.WriteString(s)
	}
	intp := func(v *int) {
		if v == nil {
			field("-")
			return
		}
		field(strconv.Itoa(*v))
	}
	field(p.LibraryID.String())
	field(p.Type)
	field(p.Title)
	intp(p.Year)
	intp(p.TMDBID)
	if p.ParentID != nil {
		field(p.ParentID.String())
	} else {
		field("-")
	}
	intp(p.Index)
	intp(p.DiscNumber)
	field(p.FolderPath)
	return b.String()
}

// cacheable reports whether an uncached lookup could ever find an existing
// item for p. When it can't (no title and no index), every call creates a
// fresh item, and caching would wrongly hand back the first one.
func cacheable(kind byte, p CreateItemParams) bool {
	if p.Title != "" {
		return true
	}
	return kind == scanKindHierarchy && p.ParentID != nil && p.Index != nil
}

// cachedItem returns the item a previous lookup of key resolved to, re-read
// and re-checked against p, or nil (a miss, or a stale entry, now dropped).
func (s *Service) cachedItem(ctx context.Context, c *ScanCache, kind byte, key string, p CreateItemParams) *Item {
	id, ok := c.get(key)
	if !ok {
		c.misses.Add(1)
		return nil
	}
	it, err := s.rw.GetMediaItem(ctx, id) // deleted rows read as not found
	if err != nil || !stillMatches(&it, kind, p) {
		c.drop(key)
		c.stale.Add(1)
		c.misses.Add(1)
		return nil
	}
	c.hits.Add(1)
	return &it
}

// stillMatches re-applies the parts of the lookup a cached row can stop
// satisfying. A top-level row keeps matching through a rename (that is what
// the folder and flexible lookups exist to tolerate). A parented row must
// still sit under the same parent and still be a sibling match — the music
// scanner re-resolves a track after another file claims its disc and relies
// on the second lookup NOT returning the same row.
func stillMatches(it *Item, kind byte, p CreateItemParams) bool {
	if it.LibraryID != p.LibraryID || it.Type != p.Type {
		return false
	}
	if kind == scanKindItem || p.ParentID == nil {
		return true // title lookups: the row is still a live item of this library and type
	}
	if it.ParentID == nil || *it.ParentID != *p.ParentID {
		return false
	}
	byIndex, byTitle := siblingMatch(it, p, titleKeyOf(p.Title), TrackDisc(p.DiscNumber))
	return byIndex || byTitle
}
