package api

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// artworkRatingTTL is how long a resolved owner rating is reused. A grid of
// posters asks for each one at once and again on every scroll back, so a
// capped profile turned each image into a database round trip; the rating
// belongs to the item, not the viewer, so one answer serves every profile.
// An admin changing an item's rating takes effect within this window.
const artworkRatingTTL = 30 * time.Second

// artworkRatingMax bounds the cache. Entries are a path and a short string;
// this covers several libraries' worth of posters, fanart and thumbs.
const artworkRatingMax = 16384

type artworkRatingKey struct {
	library uuid.UUID
	// The request's exact relative path, not a lowercased one. The lookup
	// itself is case-insensitive, but Postgres lower() and strings.ToLower
	// disagree outside ASCII (a C-locale database leaves "É" alone), so a
	// folded key could hand one item's rating to another item's path.
	// Case variants of one path just get separate entries.
	path string
}

type artworkRatingEntry struct {
	rating string
	at     time.Time
}

// artworkRatingCache memoizes Handlers.ArtworkContentRating. Only found
// answers are kept: found=false also stands for a failed query, and the
// route already treats it as the most restrictive rating, so there is
// nothing to gain from remembering it and a blip shouldn't outlive itself.
type artworkRatingCache struct {
	lookup func(ctx context.Context, libraryID uuid.UUID, relPath string) (string, bool)

	mu  sync.Mutex
	m   map[artworkRatingKey]artworkRatingEntry
	now func() time.Time // overridable in tests
}

// newArtworkRatingCache wraps lookup; nil stays nil so the route's "no rating
// hook wired" check is unchanged.
func newArtworkRatingCache(lookup func(ctx context.Context, libraryID uuid.UUID, relPath string) (string, bool)) *artworkRatingCache {
	if lookup == nil {
		return nil
	}
	return &artworkRatingCache{lookup: lookup, m: map[artworkRatingKey]artworkRatingEntry{}}
}

func (c *artworkRatingCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// rating has ArtworkContentRating's contract: the owning item's rating, and
// whether the path belongs to a known item in the library.
func (c *artworkRatingCache) rating(ctx context.Context, libraryID uuid.UUID, relPath string) (string, bool) {
	key := artworkRatingKey{library: libraryID, path: relPath}
	now := c.clock()
	c.mu.Lock()
	if e, ok := c.m[key]; ok && now.Sub(e.at) < artworkRatingTTL {
		c.mu.Unlock()
		return e.rating, true
	}
	c.mu.Unlock()

	// Looked up outside the lock: a slow query mustn't stall every other
	// image; two concurrent misses for one path just both ask.
	rating, found := c.lookup(ctx, libraryID, relPath)
	if !found {
		return rating, false
	}
	c.mu.Lock()
	if len(c.m) >= artworkRatingMax {
		// Drop the expired entries; if that frees nothing, start over. A
		// cleared cache costs one lookup per image, as before it existed.
		for k, e := range c.m {
			if now.Sub(e.at) >= artworkRatingTTL {
				delete(c.m, k)
			}
		}
		if len(c.m) >= artworkRatingMax {
			c.m = map[artworkRatingKey]artworkRatingEntry{}
		}
	}
	c.m[key] = artworkRatingEntry{rating: rating, at: now}
	c.mu.Unlock()
	return rating, true
}
