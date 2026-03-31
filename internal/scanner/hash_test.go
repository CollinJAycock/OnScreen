package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// writeTestFile creates a temporary file with the given content and returns its path and os.FileInfo.
func writeTestFile(t *testing.T, dir, name, content string) (string, os.FileInfo) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat test file: %v", err)
	}
	return path, info
}

func resetCache() {
	hashCacheMu.Lock()
	hashCache = make(map[string]fileInfo)
	hashCacheOld = make(map[string]fileInfo)
	hashCacheMu.Unlock()
}

// ── HashFile ─────────────────────────────────────────────────────────────────

func TestHashFile_SmallFile(t *testing.T) {
	resetCache()
	dir := t.TempDir()
	path, info := writeTestFile(t, dir, "small.txt", "hello world")

	hash, err := HashFile(context.Background(), path, info)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if hash == nil || *hash == "" {
		t.Fatal("expected non-empty hash")
	}
}

func TestHashFile_Deterministic(t *testing.T) {
	resetCache()
	dir := t.TempDir()
	path, info := writeTestFile(t, dir, "det.txt", "deterministic content")

	h1, err := HashFile(context.Background(), path, info)
	if err != nil {
		t.Fatalf("HashFile 1: %v", err)
	}

	resetCache()
	h2, err := HashFile(context.Background(), path, info)
	if err != nil {
		t.Fatalf("HashFile 2: %v", err)
	}
	if *h1 != *h2 {
		t.Errorf("hashes differ for identical content: %q vs %q", *h1, *h2)
	}
}

func TestHashFile_DifferentContentDifferentHash(t *testing.T) {
	resetCache()
	dir := t.TempDir()
	p1, i1 := writeTestFile(t, dir, "a.txt", "content A")
	p2, i2 := writeTestFile(t, dir, "b.txt", "content B")

	h1, err := HashFile(context.Background(), p1, i1)
	if err != nil {
		t.Fatalf("HashFile a: %v", err)
	}
	h2, err := HashFile(context.Background(), p2, i2)
	if err != nil {
		t.Fatalf("HashFile b: %v", err)
	}
	if *h1 == *h2 {
		t.Error("expected different hashes for different content")
	}
}

// ── Cache behavior ───────────────────────────────────────────────────────────

func TestHashFile_CacheHit(t *testing.T) {
	resetCache()
	dir := t.TempDir()
	path, info := writeTestFile(t, dir, "cached.txt", "cached data")

	h1, err := HashFile(context.Background(), path, info)
	if err != nil {
		t.Fatalf("HashFile 1: %v", err)
	}

	// Second call with same mtime+size should return cached value.
	h2, err := HashFile(context.Background(), path, info)
	if err != nil {
		t.Fatalf("HashFile 2: %v", err)
	}
	if *h1 != *h2 {
		t.Errorf("cache miss: hashes should be identical")
	}
}

func TestHashFile_CacheInvalidatedOnMtimeChange(t *testing.T) {
	resetCache()
	dir := t.TempDir()
	path, info1 := writeTestFile(t, dir, "mtime.txt", "version 1")

	_, err := HashFile(context.Background(), path, info1)
	if err != nil {
		t.Fatalf("HashFile 1: %v", err)
	}

	// Ensure different mtime.
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(path, []byte("version 2"), 0644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	info2, _ := os.Stat(path)

	h2, err := HashFile(context.Background(), path, info2)
	if err != nil {
		t.Fatalf("HashFile 2: %v", err)
	}
	if h2 == nil || *h2 == "" {
		t.Fatal("expected non-empty hash after rewrite")
	}
}

// ── Generation-based eviction ────────────────────────────────────────────────

func TestHashFile_GenerationEviction(t *testing.T) {
	resetCache()
	// Temporarily set a low cache limit.
	origMax := maxHashCacheEntries
	defer func() {
		// Can't reassign const, so just reset cache.
		resetCache()
		_ = origMax
	}()

	dir := t.TempDir()

	// Fill the cache beyond maxHashCacheEntries by writing many files.
	// Since we can't change the const, simulate by directly manipulating the cache.
	hashCacheMu.Lock()
	for i := 0; i < maxHashCacheEntries; i++ {
		hashCache[filepath.Join(dir, "fake", fmt.Sprintf("%d", i))] = fileInfo{hash: "x"}
	}
	hashCacheMu.Unlock()

	// Next insert should trigger rotation.
	path, info := writeTestFile(t, dir, "overflow.txt", "overflow")
	_, err := HashFile(context.Background(), path, info)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}

	hashCacheMu.Lock()
	newSize := len(hashCache)
	oldSize := len(hashCacheOld)
	hashCacheMu.Unlock()

	// After rotation: old cache should have the previous entries,
	// new cache should be small (just the new entry).
	if newSize > 10 {
		t.Errorf("expected small new cache after rotation, got %d entries", newSize)
	}
	if oldSize < maxHashCacheEntries {
		t.Errorf("expected old cache to hold previous entries, got %d", oldSize)
	}
}

func TestHashFile_OldGenerationPromoted(t *testing.T) {
	resetCache()
	dir := t.TempDir()
	path, info := writeTestFile(t, dir, "promote.txt", "promote me")

	// Put entry directly into old generation.
	hash := "abc123"
	hashCacheMu.Lock()
	hashCacheOld[path] = fileInfo{
		mtime: info.ModTime(),
		size:  info.Size(),
		hash:  hash,
	}
	hashCacheMu.Unlock()

	got, err := HashFile(context.Background(), path, info)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if *got != hash {
		t.Errorf("expected promoted hash %q, got %q", hash, *got)
	}

	// Verify it was promoted to current generation.
	hashCacheMu.Lock()
	_, inCurrent := hashCache[path]
	hashCacheMu.Unlock()
	if !inCurrent {
		t.Error("expected entry to be promoted to current generation")
	}
}

// ── Context cancellation ─────────────────────────────────────────────────────

func TestHashFile_CancelledContext(t *testing.T) {
	resetCache()
	dir := t.TempDir()
	path, info := writeTestFile(t, dir, "cancel.txt", "cancel")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := HashFile(ctx, path, info)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

// ── Concurrent access ────────────────────────────────────────────────────────

func TestHashFile_ConcurrentSafe(t *testing.T) {
	resetCache()
	dir := t.TempDir()
	path, info := writeTestFile(t, dir, "concurrent.txt", "concurrent data for hashing test")

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := HashFile(context.Background(), path, info)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent HashFile error: %v", err)
	}
}
