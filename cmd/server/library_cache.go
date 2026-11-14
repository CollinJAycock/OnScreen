package main

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/library"
)

// libraryListTTL backstops invalidation. This process drops the cached list
// itself whenever it creates, updates or deletes a library (see
// libraryChangeNotifier); the TTL covers what it can't see: another server
// instance changing a library, or a read replica that answered the refill
// before it had caught up with the change.
const libraryListTTL = 30 * time.Second

// libraryListCache holds the live library list in memory for the per-request
// callers that only need roots: the artwork route asked the database for
// every library on every image (ArtworkRoots), and the enricher's
// path resolution did the same per artwork write (scanPathsFn). Libraries
// change a few times a year; images are served hundreds at a time.
type libraryListCache struct {
	list func(ctx context.Context) ([]library.Library, error)

	mu   sync.Mutex
	libs []library.Library
	at   time.Time
	ok   bool
	// gen counts invalidations, so a refill that started before one can't
	// store the list it read over the invalidation.
	gen uint64
	now func() time.Time // overridable in tests
}

func newLibraryListCache(list func(ctx context.Context) ([]library.Library, error)) *libraryListCache {
	return &libraryListCache{list: list}
}

func (c *libraryListCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// Libraries returns the cached list, refilling it when it was invalidated or
// is older than libraryListTTL. Callers must not modify the result (it is
// shared); a failed refill is returned as an error and not cached.
func (c *libraryListCache) Libraries(ctx context.Context) ([]library.Library, error) {
	now := c.clock()
	c.mu.Lock()
	if c.ok && now.Sub(c.at) < libraryListTTL {
		libs := c.libs
		c.mu.Unlock()
		return libs, nil
	}
	gen := c.gen
	c.mu.Unlock()

	libs, err := c.list(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.gen == gen {
		c.libs, c.at, c.ok = libs, now, true
	}
	c.mu.Unlock()
	return libs, nil
}

// Invalidate drops the cached list; the next Libraries call reads it again.
func (c *libraryListCache) Invalidate() {
	c.mu.Lock()
	c.gen++
	c.ok = false
	c.libs = nil
	c.mu.Unlock()
}

// libraryChangeNotifier wraps the read-write library querier and calls
// onChange after every statement that changes what ListLibraries returns.
// Wrapping the querier rather than the handler catches every path into the
// library service, present and future. It fires on errors too: a statement
// whose client gave up (a cancelled request) may still have committed, and
// an extra refill is cheap.
type libraryChangeNotifier struct {
	library.Querier
	onChange func()
}

func (n libraryChangeNotifier) CreateLibrary(ctx context.Context, p library.CreateLibraryParams) (library.Library, error) {
	lib, err := n.Querier.CreateLibrary(ctx, p)
	n.onChange()
	return lib, err
}

func (n libraryChangeNotifier) UpdateLibrary(ctx context.Context, p library.UpdateLibraryParams) (library.Library, error) {
	lib, err := n.Querier.UpdateLibrary(ctx, p)
	n.onChange()
	return lib, err
}

func (n libraryChangeNotifier) SoftDeleteLibrary(ctx context.Context, id uuid.UUID) error {
	err := n.Querier.SoftDeleteLibrary(ctx, id)
	n.onChange()
	return err
}
