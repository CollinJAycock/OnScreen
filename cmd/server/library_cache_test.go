package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/library"
)

// The artwork route reads library roots on every image; the cache serves
// them from memory, refills after an invalidation or the TTL, and never
// caches a failed read.
func TestLibraryListCache(t *testing.T) {
	ctx := context.Background()
	reads := 0
	var fail error
	libs := []library.Library{{ID: uuid.New(), Paths: []string{"/m"}}}
	c := newLibraryListCache(func(context.Context) ([]library.Library, error) {
		reads++
		if fail != nil {
			return nil, fail
		}
		return libs, nil
	})
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		got, err := c.Libraries(ctx)
		if err != nil || len(got) != 1 {
			t.Fatalf("Libraries = %v, %v", got, err)
		}
	}
	if reads != 1 {
		t.Fatalf("reads = %d, want 1", reads)
	}

	libs = append(libs, library.Library{ID: uuid.New()})
	c.Invalidate()
	if got, _ := c.Libraries(ctx); len(got) != 2 || reads != 2 {
		t.Fatalf("after Invalidate: %d libraries, %d reads; want 2, 2", len(got), reads)
	}

	now = now.Add(libraryListTTL)
	if _, _ = c.Libraries(ctx); reads != 3 {
		t.Fatalf("reads = %d after the TTL, want 3", reads)
	}

	fail = errors.New("db down")
	c.Invalidate()
	if _, err := c.Libraries(ctx); err == nil {
		t.Fatal("a failed read must surface its error")
	}
	fail = nil
	if got, err := c.Libraries(ctx); err != nil || len(got) != 2 || reads != 5 {
		t.Fatalf("after a failed read: %d libraries, err %v, %d reads; want 2, nil, 5", len(got), err, reads)
	}
}

// A refill that started before an invalidation must not store the list it
// read over the invalidation.
func TestLibraryListCache_InvalidateDuringRefill(t *testing.T) {
	ctx := context.Background()
	var c *libraryListCache
	reads := 0
	c = newLibraryListCache(func(context.Context) ([]library.Library, error) {
		reads++
		if reads == 1 {
			c.Invalidate() // a library changed while this read was in flight
		}
		return []library.Library{{ID: uuid.New()}}, nil
	})
	_, _ = c.Libraries(ctx)
	_, _ = c.Libraries(ctx)
	if reads != 2 {
		t.Fatalf("reads = %d, want 2 (the stale refill must not be cached)", reads)
	}
}

type fakeLibraryQuerier struct {
	library.Querier
	err error
}

func (f fakeLibraryQuerier) CreateLibrary(context.Context, library.CreateLibraryParams) (library.Library, error) {
	return library.Library{}, f.err
}

func (f fakeLibraryQuerier) UpdateLibrary(context.Context, library.UpdateLibraryParams) (library.Library, error) {
	return library.Library{}, f.err
}

func (f fakeLibraryQuerier) SoftDeleteLibrary(context.Context, uuid.UUID) error { return f.err }

// Every library mutation reports itself, so this process never serves a
// stale root after its own change.
func TestLibraryChangeNotifier(t *testing.T) {
	ctx := context.Background()
	for _, err := range []error{nil, errors.New("timeout")} {
		changes := 0
		n := libraryChangeNotifier{Querier: fakeLibraryQuerier{err: err}, onChange: func() { changes++ }}
		_, _ = n.CreateLibrary(ctx, library.CreateLibraryParams{})
		_, _ = n.UpdateLibrary(ctx, library.UpdateLibraryParams{})
		_ = n.SoftDeleteLibrary(ctx, uuid.New())
		if changes != 3 {
			t.Fatalf("err=%v: onChange fired %d times, want 3", err, changes)
		}
	}
}
