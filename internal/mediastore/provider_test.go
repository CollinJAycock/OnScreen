package mediastore

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// taggedStore reports which backend answered, so a swap is observable.
type taggedStore struct{ tag string }

func (s taggedStore) Open(context.Context, string) (io.ReadSeekCloser, error) {
	return nil, ErrNotFound
}
func (s taggedStore) Stat(context.Context, string) (FileInfo, error) { return FileInfo{}, nil }
func (s taggedStore) SignedURL(context.Context, string, time.Duration) (string, error) {
	return s.tag, nil
}

func TestProvider_DelegatesAndSwaps(t *testing.T) {
	p := NewProvider(taggedStore{tag: "first"})

	got, _ := p.SignedURL(context.Background(), "k", time.Minute)
	if got != "first" {
		t.Fatalf("before swap: got %q, want first", got)
	}

	p.Set(taggedStore{tag: "second"})
	got, _ = p.SignedURL(context.Background(), "k", time.Minute)
	if got != "second" {
		t.Errorf("after swap: got %q, want second", got)
	}
}

func TestProvider_NilDefaultsToLocal(t *testing.T) {
	// NewProvider(nil) and Set(nil) must both fall back to Local, never nil-panic
	// the serve path.
	p := NewProvider(nil)
	if got, err := p.SignedURL(context.Background(), "/k", time.Minute); err != nil || got != "" {
		t.Errorf("nil provider: got (%q,%v), want (\"\",nil) — Local can't offload", got, err)
	}
	p.Set(taggedStore{tag: "x"})
	p.Set(nil) // reset to Local
	if got, _ := p.SignedURL(context.Background(), "/k", time.Minute); got != "" {
		t.Errorf("Set(nil): got %q, want \"\" (reset to Local)", got)
	}
}

func TestProvider_Walk_DelegatesToLister(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := NewProvider(Local{}) // Local is a Lister
	count := 0
	if err := p.Walk(context.Background(), root, func(ObjectInfo) error { count++; return nil }); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if count != 1 {
		t.Errorf("walked %d files, want 1", count)
	}
}

func TestProvider_Walk_ErrorsWhenBackendNotLister(t *testing.T) {
	// taggedStore implements Store but not Lister → Walk must error, not panic.
	p := NewProvider(taggedStore{tag: "x"})
	if err := p.Walk(context.Background(), "/", func(ObjectInfo) error { return nil }); err == nil {
		t.Error("expected an error when the backend can't list")
	}
}

func TestProvider_OpenStat_DelegateToCurrent(t *testing.T) {
	// Open/Stat must route to the active backend, not a stale one.
	p := NewProvider(taggedStore{tag: "first"})
	if _, err := p.Open(context.Background(), "k"); err == nil {
		t.Error("Open should surface the backend's error")
	}
	p.Set(Local{})
	// Local.Stat on a missing path returns ErrNotFound — proves delegation moved.
	if _, err := p.Stat(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("Stat should delegate to Local and report the missing file")
	}
}

func TestProvider_Put_DelegatesToPutter(t *testing.T) {
	dir := t.TempDir()
	p := NewProvider(Local{}) // Local is a Putter
	key := filepath.Join(dir, "poster.jpg")
	if err := p.Put(context.Background(), key, []byte("art")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if b, _ := os.ReadFile(key); string(b) != "art" {
		t.Errorf("Put did not write through the provider: %q", b)
	}
}

func TestProvider_Put_ErrorsWhenBackendNotPutter(t *testing.T) {
	// taggedStore implements Store but not Putter → Put must error, not panic.
	p := NewProvider(taggedStore{})
	if err := p.Put(context.Background(), "k", []byte("x")); err == nil {
		t.Error("expected an error when the backend can't write")
	}
}

func TestIsLocal(t *testing.T) {
	cases := []struct {
		name string
		s    Store
		want bool
	}{
		{"bare Local", Local{}, true},
		{"Local with remap", Local{Remap: []PathMapping{{From: "/a", To: "/b"}}}, true},
		{"provider wrapping Local", NewProvider(Local{}), true},
		{"provider wrapping non-local", NewProvider(taggedStore{}), false},
		{"bare non-local", taggedStore{}, false},
	}
	for _, tc := range cases {
		if got := IsLocal(tc.s); got != tc.want {
			t.Errorf("%s: IsLocal = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestProvider_ConcurrentReadsDuringSwap(t *testing.T) {
	// The -race detector should stay quiet: concurrent reads while a swap runs.
	p := NewProvider(taggedStore{tag: "a"})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _ = p.SignedURL(context.Background(), "k", time.Minute)
			}
		}()
	}
	for i := 0; i < 20; i++ {
		p.Set(taggedStore{tag: "b"})
	}
	wg.Wait()
}
