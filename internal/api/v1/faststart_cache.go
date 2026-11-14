package v1

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// faststartCacheMax bounds the memo. An entry is a few dozen bytes; this
// covers the MP4 files of every title a household opens between restarts
// many times over.
const faststartCacheMax = 8192

// faststartCacheTTL re-checks a file now and then even if its row never
// changes, so an answer read through a transient I/O error (the probe reads
// those as "faststart") doesn't stick for the life of the process.
const faststartCacheTTL = time.Hour

// faststartCache memoizes the moov-before-mdat probe per media file. Item
// detail used to open every MP4 version of the title and walk its top-level
// atoms on every GET (a network read for object-storage sources), for an
// answer that only changes when the file does. An entry is valid only for the
// size and scanned_at it was probed at: the scanner rewrites both when the
// file's content changes, so a replaced file is probed again on its next
// detail view. The zero value is ready to use.
type faststartCache struct {
	mu sync.Mutex
	m  map[uuid.UUID]faststartEntry
	// now is overridable in tests.
	now func() time.Time
}

type faststartEntry struct {
	size      int64
	scannedAt time.Time
	fast      bool
	at        time.Time
}

func (c *faststartCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// get returns the memoized answer for f, if one was probed at f's current
// size and scanned_at within the TTL.
func (c *faststartCache) get(f media.File) (fast, ok bool) {
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	e, hit := c.m[f.ID]
	if !hit || e.size != f.FileSize || !e.scannedAt.Equal(f.ScannedAt) || now.Sub(e.at) >= faststartCacheTTL {
		return false, false
	}
	return e.fast, true
}

// put records the probe result for f. When full it drops one arbitrary entry
// (map order is random), which is all a hint cache needs: a miss costs one
// probe, the same as before the cache existed.
func (c *faststartCache) put(f media.File, fast bool) {
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[uuid.UUID]faststartEntry)
	}
	if _, exists := c.m[f.ID]; !exists && len(c.m) >= faststartCacheMax {
		for k := range c.m {
			delete(c.m, k)
			break
		}
	}
	c.m[f.ID] = faststartEntry{size: f.FileSize, scannedAt: f.ScannedAt, fast: fast, at: now}
}
