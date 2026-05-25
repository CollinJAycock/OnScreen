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
