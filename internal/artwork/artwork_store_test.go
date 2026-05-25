package artwork

import (
	"bytes"
	"context"
	"image/jpeg"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/onscreen/onscreen/internal/mediastore"
)

type nopSeekCloser struct{ io.ReadSeeker }

func (nopSeekCloser) Close() error { return nil }

// memArtStore serves a fixed image from memory and counts Open/Stat calls, so a
// test can prove Resize reads the source through the store (not os.Open) and that
// a cache hit skips the store Open.
type memArtStore struct {
	data    []byte
	modTime time.Time

	mu          sync.Mutex
	opens, stat int
}

func (s *memArtStore) Open(context.Context, string) (io.ReadSeekCloser, error) {
	s.mu.Lock()
	s.opens++
	s.mu.Unlock()
	return nopSeekCloser{bytes.NewReader(s.data)}, nil
}

func (s *memArtStore) Stat(context.Context, string) (mediastore.FileInfo, error) {
	s.mu.Lock()
	s.stat++
	s.mu.Unlock()
	return mediastore.FileInfo{Size: int64(len(s.data)), ModTime: s.modTime}, nil
}

func (s *memArtStore) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func TestResize_ReadsSourceThroughStore(t *testing.T) {
	// Source mtime in the past so the freshly-written cache is considered valid.
	store := &memArtStore{data: makeJPEG(t, 80, 40), modTime: time.Now().Add(-time.Hour)}
	m := New(t.TempDir()).WithMediaStore(store)

	// The key never exists on the local filesystem — only the store can serve it,
	// so a successful resize proves Resize reads through the store.
	const key = "/objstore/Movies/poster.jpg"

	var buf bytes.Buffer
	if err := m.Resize(context.Background(), &buf, key, 40, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if store.opens == 0 {
		t.Fatal("source was not opened through the store")
	}
	got, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatalf("decode resized: %v", err)
	}
	if b := got.Bounds(); b.Dx() != 40 || b.Dy() != 20 {
		t.Errorf("dims = %dx%d, want 40x20 (2:1 fit into 40x40)", b.Dx(), b.Dy())
	}

	// Second call should hit the local cache → another Stat (mtime check) but no
	// second store Open.
	opensAfterFirst := store.opens
	var buf2 bytes.Buffer
	if err := m.Resize(context.Background(), &buf2, key, 40, 40); err != nil {
		t.Fatalf("Resize 2: %v", err)
	}
	if store.opens != opensAfterFirst {
		t.Errorf("cache hit re-opened source: opens %d → %d", opensAfterFirst, store.opens)
	}
	if store.stat < 2 {
		t.Errorf("expected a Stat per call for cache validation, got %d", store.stat)
	}
}
