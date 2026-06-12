package tmdb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Disk-backed TMDB response cache.
//
// TMDB metadata is near-static and the enricher's access pattern is brutally
// repetitive: every full-library pass re-asks the same questions, and the
// refresh_missing_art task re-searches the same never-matching titles on
// every run. That repeat traffic is what got a real key revoked ("Invalid
// API key" after a 48k-episode enrichment pass) — the same reason Jellyfin
// and Plex cache aggressively in front of their metadata sources.
//
// Two TTLs:
//   - positive (200) responses: metadata for a matched title barely changes;
//     a week-long TTL still re-syncs ratings/art regularly. Note this also
//     covers 200-with-empty-results search responses — the "TMDB has nothing
//     for this query" case — which is the biggest repeat offender.
//   - negative (404) responses: shorter, so an id that appears on TMDB later
//     (new release, freshly-created entry) isn't invisible for long.
//
// Entries are sharded two-hex-chars deep to keep directory fan-out sane on
// big libraries. Reads and writes are best-effort: a broken cache dir
// degrades to live calls, never to an error.
const (
	cachePosTTL = 7 * 24 * time.Hour
	cacheNegTTL = 3 * 24 * time.Hour
)

type diskCache struct{ dir string }

// cacheEnvelope is the on-disk shape. Body is the verbatim TMDB response
// body for positive entries; empty for negative (404) entries.
type cacheEnvelope struct {
	Negative  bool            `json:"negative,omitempty"`
	FetchedAt time.Time       `json:"fetched_at"`
	Body      json.RawMessage `json:"body,omitempty"`
}

func newDiskCache(dir string) *diskCache {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil // read-only cache root — run uncached
	}
	return &diskCache{dir: dir}
}

func cacheKey(pathAndParams string) string {
	sum := sha256.Sum256([]byte(pathAndParams))
	return hex.EncodeToString(sum[:])
}

func (d *diskCache) file(key string) string {
	return filepath.Join(d.dir, key[:2], key+".json")
}

// lookup returns the cached body (nil for negative entries) when a fresh
// entry exists.
func (d *diskCache) lookup(key string) (body []byte, negative, ok bool) {
	raw, err := os.ReadFile(d.file(key))
	if err != nil {
		return nil, false, false
	}
	var env cacheEnvelope
	if json.Unmarshal(raw, &env) != nil {
		return nil, false, false
	}
	ttl := cachePosTTL
	if env.Negative {
		ttl = cacheNegTTL
	}
	if time.Since(env.FetchedAt) > ttl {
		return nil, false, false
	}
	return env.Body, env.Negative, true
}

// store persists an entry. Best-effort — failures are ignored (next call
// just goes to the network again).
func (d *diskCache) store(key string, negative bool, body []byte) {
	env := cacheEnvelope{Negative: negative, FetchedAt: time.Now(), Body: body}
	raw, err := json.Marshal(env)
	if err != nil {
		return
	}
	path := d.file(key)
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	// Write-then-rename so a concurrent reader never sees a torn file.
	tmp := path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
