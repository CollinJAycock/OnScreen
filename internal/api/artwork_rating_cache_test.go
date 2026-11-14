package api

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A poster grid asks for each image's owner rating; the cache answers
// repeats from memory for artworkRatingTTL, keeps libraries and exact paths
// apart, and never remembers a miss (which may have been a query error).
func TestArtworkRatingCache(t *testing.T) {
	ctx := context.Background()
	lib, other := uuid.New(), uuid.New()
	ratings := map[string]string{"Movie/poster.jpg": "R"}
	calls := 0
	c := newArtworkRatingCache(func(_ context.Context, l uuid.UUID, p string) (string, bool) {
		calls++
		if l != lib {
			return "", false
		}
		r, ok := ratings[p]
		return r, ok
	})
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if r, ok := c.rating(ctx, lib, "Movie/poster.jpg"); r != "R" || !ok {
			t.Fatalf("rating = %q, %v", r, ok)
		}
	}
	if calls != 1 {
		t.Fatalf("lookups = %d, want 1", calls)
	}

	// Another library, a case variant and an unknown path are separate
	// lookups; the misses are asked again every time.
	if _, ok := c.rating(ctx, other, "Movie/poster.jpg"); ok {
		t.Fatal("another library's path must not hit this library's entry")
	}
	_, _ = c.rating(ctx, lib, "movie/POSTER.jpg")
	_, _ = c.rating(ctx, lib, "extrafanart/1.jpg")
	_, _ = c.rating(ctx, lib, "extrafanart/1.jpg")
	if calls != 5 {
		t.Fatalf("lookups = %d, want 5 (misses are not cached)", calls)
	}

	// A rating change shows after the TTL.
	ratings["Movie/poster.jpg"] = "PG"
	now = now.Add(artworkRatingTTL)
	if r, _ := c.rating(ctx, lib, "Movie/poster.jpg"); r != "PG" {
		t.Fatalf("rating after TTL = %q, want PG", r)
	}
}

func TestArtworkRatingCache_NilLookup(t *testing.T) {
	if newArtworkRatingCache(nil) != nil {
		t.Fatal("no rating hook must stay nil so the route skips the gate as before")
	}
}

// Bounded: a flood of distinct paths can't grow the map without limit.
func TestArtworkRatingCache_Bounded(t *testing.T) {
	c := newArtworkRatingCache(func(context.Context, uuid.UUID, string) (string, bool) { return "PG", true })
	lib := uuid.New()
	for i := 0; i < artworkRatingMax+100; i++ {
		c.rating(context.Background(), lib, uuid.NewString())
	}
	if n := len(c.m); n > artworkRatingMax {
		t.Fatalf("cache holds %d entries, cap %d", n, artworkRatingMax)
	}
}
