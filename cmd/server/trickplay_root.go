package main

import (
	"os"
	"path/filepath"

	"github.com/onscreen/onscreen/internal/config"
)

// trickplayRootDir picks the on-disk root for generated trickplay sprites.
//
// It roots through cfg.CacheSubdir like every other cache. The original
// wiring used filepath.Dir(CachePath)/trickplay — the exact mistake
// CacheSubdir exists to prevent: in the container image CACHE_PATH is
// /var/cache/onscreen (the writable volume), so that climbed to the
// root-owned /var/cache and every generation failed with "mkdir … permission
// denied". Harmless while generation was a rarely-used admin action; fatal
// once it runs automatically after every scan.
//
// A legacy directory that already exists is kept in use (and the new one
// isn't), so installs where the old location WAS writable — the default
// ~/.onscreen/cache layout — keep serving the sprites their 'done' rows point
// at instead of every item 404ing until regenerated.
func trickplayRootDir(cfg *config.Config) string {
	if cfg.CachePath == "" {
		return filepath.Join(os.TempDir(), "onscreen-trickplay")
	}
	root := cfg.CacheSubdir("trickplay")
	if dirExists(root) {
		return root
	}
	legacy := filepath.Join(filepath.Dir(cfg.CachePath), "trickplay")
	if dirExists(legacy) {
		return legacy
	}
	return root
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
