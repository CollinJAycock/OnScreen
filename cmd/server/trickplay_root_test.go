package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/onscreen/onscreen/internal/config"
)

func TestTrickplayRootDir_UnderCacheVolume(t *testing.T) {
	base := t.TempDir()
	cache := filepath.Join(base, "onscreen") // e.g. /var/cache/onscreen
	cfg := &config.Config{CachePath: cache}
	if got, want := trickplayRootDir(cfg), filepath.Join(cache, "trickplay"); got != want {
		t.Errorf("fresh install root = %q, want %q (must not climb above CACHE_PATH)", got, want)
	}
}

func TestTrickplayRootDir_KeepsExistingLegacyDir(t *testing.T) {
	base := t.TempDir()
	cache := filepath.Join(base, "cache", "artwork") // the ~/.onscreen default shape
	legacy := filepath.Join(base, "cache", "trickplay")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{CachePath: cache}
	if got := trickplayRootDir(cfg); got != legacy {
		t.Errorf("root = %q, want the existing legacy dir %q", got, legacy)
	}
	// Once the new location exists it wins.
	if err := os.MkdirAll(filepath.Join(cache, "trickplay"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := trickplayRootDir(cfg), filepath.Join(cache, "trickplay"); got != want {
		t.Errorf("root = %q, want %q", got, want)
	}
}

func TestTrickplayRootDir_EmptyCachePath(t *testing.T) {
	if got := trickplayRootDir(&config.Config{}); got != filepath.Join(os.TempDir(), "onscreen-trickplay") {
		t.Errorf("root = %q", got)
	}
}
