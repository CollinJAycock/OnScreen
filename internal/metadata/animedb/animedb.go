// Package animedb is an offline title-→-AniList-ID lookup backed by
// the manami-project anime-offline-database. The dataset cross-
// references AniDB / AniList / MAL / Kitsu / Anime-Planet IDs and
// carries every alternate title (synonyms list) per entry, which is
// exactly the data we need to recover from AniList's live fuzzy
// search misses.
//
// Why this exists: AniList's GraphQL `Media(search:$q)` is heuristic
// and routinely misses fansub-style folder names like
// "Akame ga Kill Theater" (the canonical title is
// "Akame ga Kill! Gaiden: Theater"). The community-curated synonyms
// list in this dataset captures those variants, so we can resolve
// a folder name to an AniList ID without an exact title match.
//
// Update cadence: the upstream is regenerated weekly. We cache to
// disk and refresh on a 7-day TTL. First boot of a fresh deployment
// takes one network round-trip + ~300 ms to build the index;
// subsequent boots hit the cached file.
//
// Why not Fribb anime-lists: Fribb gives ID-only mappings (anilist_id
// → mal_id → tvdb_id …). It's smaller, but it doesn't carry the
// per-entry synonyms list — the part we actually need for the
// title-resolution path. Manami's dataset is the superset.
package animedb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// ManamiURL is the upstream JSON the cache fetches from. Tests
// override via NewWithSource so they can point at a fixture path.
const ManamiURL = "https://raw.githubusercontent.com/manami-project/anime-offline-database/master/anime-offline-database.json"

// CacheTTL is how long the on-disk JSON stays "fresh" before the
// next Open() refetches. Manami releases weekly, so 7 days lines up
// without churn.
const CacheTTL = 7 * 24 * time.Hour

// Entry is one normalized record from the manami dataset, exposing
// only the fields the enricher consumes. Other manami columns
// (status, animeSeason, picture, …) are dropped on parse to keep
// the in-memory footprint reasonable.
type Entry struct {
	Title     string
	Type      string // "TV" | "MOVIE" | "OVA" | "ONA" | "SPECIAL" | "MUSIC" | "UNKNOWN"
	Year      int    // 0 when the upstream entry omits animeSeason.year
	AniListID int    // 0 when the entry has no AniList source
	MALID     int
	AniDBID   int
	KitsuID   int
	Synonyms  []string
}

// DB is the in-memory title index. Read-mostly: built once on Open,
// then only the rwmutex's read path is exercised by Lookup. Reload
// rebuilds atomically.
type DB struct {
	cacheDir   string
	source     string // network URL — overridable for tests
	httpClient *http.Client
	logger     *slog.Logger

	mu      sync.RWMutex
	entries []Entry
	// byNorm maps the normalized title or synonym to the indices in
	// entries that contain that key. Two entries can collide on the
	// same normalized title (e.g., "Pokemon" maps to many series);
	// callers receive the first match — operators wanting tighter
	// disambiguation supply year via SearchAnime instead.
	byNorm map[string][]int
}

// New returns an unloaded DB. Call Open(ctx) before Lookup.
func New(cacheDir string, logger *slog.Logger) *DB {
	if logger == nil {
		logger = slog.Default()
	}
	return &DB{
		cacheDir:   cacheDir,
		source:     ManamiURL,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		logger:     logger,
	}
}

// NewWithSource overrides both the upstream URL and the HTTP client.
// Used by tests that point at a local file: prefix or want to inject
// a stub roundtripper.
func NewWithSource(cacheDir, source string, c *http.Client, logger *slog.Logger) *DB {
	db := New(cacheDir, logger)
	db.source = source
	if c != nil {
		db.httpClient = c
	}
	return db
}

// Open loads the dataset, downloading + caching when the on-disk copy
// is missing or stale. Safe to call repeatedly (each call refreshes
// if needed), but cheap callers prefer Lookup which is read-only.
func (db *DB) Open(ctx context.Context) error {
	cachePath := db.cachePath()
	if cachePath != "" {
		if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
			return fmt.Errorf("animedb: make cache dir: %w", err)
		}
	}

	// Decide whether to fetch.
	stale := true
	if cachePath != "" {
		info, err := os.Stat(cachePath)
		if err == nil && time.Since(info.ModTime()) < CacheTTL {
			stale = false
		}
	}

	if stale {
		if err := db.fetchToCache(ctx); err != nil {
			// Hard-fail only when there's no fallback. A stale local
			// copy is strictly better than no DB at all — log + use it.
			if cachePath == "" {
				return fmt.Errorf("animedb: fetch (no cache): %w", err)
			}
			if _, statErr := os.Stat(cachePath); statErr != nil {
				return fmt.Errorf("animedb: fetch (no cached fallback): %w", err)
			}
			db.logger.WarnContext(ctx, "animedb: refresh failed; using stale cache",
				"err", err, "path", cachePath)
		}
	}

	return db.loadFromDisk()
}

// Lookup returns the best entry for `title` based on normalized
// title + synonym match. The second return value is false when no
// entry matched. Year is currently ignored — synonyms collisions are
// rare enough on the dataset that title alone resolves cleanly; year
// disambiguation is a follow-up.
func (db *DB) Lookup(title string) (Entry, bool) {
	if title == "" {
		return Entry{}, false
	}
	key := normalizeTitle(title)
	if key == "" {
		return Entry{}, false
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	idxs, ok := db.byNorm[key]
	if !ok || len(idxs) == 0 {
		return Entry{}, false
	}
	// First match wins. The dataset is curated; collisions are
	// either rare (different anime sharing one English title across
	// languages) or by-design (alternate language names of the same
	// franchise). On the rare ambiguous case we'd rather pick a
	// stable ID than guess wrong via year-based scoring.
	return db.entries[idxs[0]], true
}

// LookupByAniListID returns the entry with the given AniList ID, if
// indexed. Linear scan — the by-ID path is only used by tests + the
// manual-match admin flow, both rare. Avoiding a second index keeps
// the build footprint smaller.
func (db *DB) LookupByAniListID(id int) (Entry, bool) {
	if id <= 0 {
		return Entry{}, false
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	for _, e := range db.entries {
		if e.AniListID == id {
			return e, true
		}
	}
	return Entry{}, false
}

// Size returns the number of entries currently indexed. Useful for
// liveness checks + logging.
func (db *DB) Size() int {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return len(db.entries)
}

// ── Internal ──────────────────────────────────────────────────────

func (db *DB) cachePath() string {
	if db.cacheDir == "" {
		return ""
	}
	return filepath.Join(db.cacheDir, "anime-offline-database.json")
}

func (db *DB) fetchToCache(ctx context.Context) error {
	cachePath := db.cachePath()
	if cachePath == "" {
		return errors.New("no cache dir configured")
	}

	// Allow file:// for tests + airgapped deploys that hand-place
	// a bundled copy alongside the binary.
	if strings.HasPrefix(db.source, "file://") {
		src := strings.TrimPrefix(db.source, "file://")
		raw, err := os.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read source: %w", err)
		}
		if err := os.WriteFile(cachePath, raw, 0o644); err != nil {
			return fmt.Errorf("write cache: %w", err)
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, db.source, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := db.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", db.source, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch %s: status %d", db.source, resp.StatusCode)
	}

	// Atomic write: stream to a tempfile in the same dir, then rename.
	// Otherwise a partial download mid-fetch leaves a corrupt cache
	// the next loadFromDisk fails on.
	tmp, err := os.CreateTemp(filepath.Dir(cachePath), ".animedb-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("copy body: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, cachePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// rawEntry mirrors the manami JSON shape. Only the fields we keep
// are listed; the rest of the dataset (animeSeason.season, status,
// picture, …) is dropped at decode time.
type rawEntry struct {
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Sources     []string `json:"sources"`
	Synonyms    []string `json:"synonyms"`
	AnimeSeason struct {
		Year int `json:"year"`
	} `json:"animeSeason"`
}

type rawFile struct {
	Data []rawEntry `json:"data"`
}

func (db *DB) loadFromDisk() error {
	cachePath := db.cachePath()
	if cachePath == "" {
		return errors.New("animedb: no cache path")
	}
	f, err := os.Open(cachePath)
	if err != nil {
		return fmt.Errorf("animedb: open cache: %w", err)
	}
	defer f.Close()

	var raw rawFile
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return fmt.Errorf("animedb: parse cache: %w", err)
	}

	entries := make([]Entry, 0, len(raw.Data))
	byNorm := make(map[string][]int, len(raw.Data)*4)
	for _, r := range raw.Data {
		e := Entry{
			Title:    r.Title,
			Type:     r.Type,
			Year:     r.AnimeSeason.Year,
			Synonyms: r.Synonyms,
		}
		// Extract IDs from each source URL. Source URLs are stable
		// enough that simple prefix matching is robust — no regex,
		// no allocator pressure on the hot path.
		for _, src := range r.Sources {
			switch {
			case strings.HasPrefix(src, "https://anilist.co/anime/"):
				e.AniListID = parseTrailingID(src, "https://anilist.co/anime/")
			case strings.HasPrefix(src, "https://myanimelist.net/anime/"):
				e.MALID = parseTrailingID(src, "https://myanimelist.net/anime/")
			case strings.HasPrefix(src, "https://anidb.net/anime/"):
				e.AniDBID = parseTrailingID(src, "https://anidb.net/anime/")
			case strings.HasPrefix(src, "https://kitsu.io/anime/"):
				// Kitsu URLs sometimes carry a slug instead of a numeric
				// ID — parseTrailingID returns 0 in that case, which is
				// the right "no Kitsu ID for this entry" sentinel.
				e.KitsuID = parseTrailingID(src, "https://kitsu.io/anime/")
			}
		}
		idx := len(entries)
		entries = append(entries, e)

		// Index every alias the entry carries. The primary title is
		// indexed even when it appears in synonyms (manami's curation
		// isn't fully consistent there) — addNormalized dedupes per
		// (key, idx) so a duplicate doesn't double the slot count.
		addNormalized(byNorm, idx, r.Title)
		for _, s := range r.Synonyms {
			addNormalized(byNorm, idx, s)
		}
	}

	db.mu.Lock()
	db.entries = entries
	db.byNorm = byNorm
	db.mu.Unlock()

	db.logger.Info("animedb loaded",
		"entries", len(entries),
		"unique_keys", len(byNorm),
		"path", cachePath,
	)
	return nil
}

func addNormalized(byNorm map[string][]int, idx int, raw string) {
	key := normalizeTitle(raw)
	if key == "" {
		return
	}
	existing := byNorm[key]
	for _, e := range existing {
		if e == idx {
			return
		}
	}
	byNorm[key] = append(existing, idx)
}

// parseTrailingID picks the integer immediately following `prefix`,
// stopping at the next non-digit. Returns 0 when there's no digit
// (e.g., Kitsu slug URLs).
func parseTrailingID(src, prefix string) int {
	tail := src[len(prefix):]
	end := 0
	for end < len(tail) && tail[end] >= '0' && tail[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, _ := strconv.Atoi(tail[:end])
	return n
}

// normalizeTitle produces the lookup key a folder name and a manami
// synonym must agree on to match. Steps:
//
//  1. NFKC normalize so half-width / full-width Japanese characters
//     compare equal.
//  2. Lowercase via Unicode case folding.
//  3. Strip combining marks (decompose, then drop M*) so accents on
//     romanizations don't divide entries that should match.
//  4. Drop everything that isn't a letter, digit, or space.
//  5. Collapse whitespace runs to a single space.
//  6. Trim.
//
// We intentionally do NOT strip "trailing-word" qualifiers ("OVA",
// "Special", "Movie", season indicators). Those carry meaning the
// dataset uses to disambiguate distinct entries — stripping them
// would alias real distinctions away.
func normalizeTitle(s string) string {
	if s == "" {
		return ""
	}
	// NFKC handles full-width vs half-width Japanese characters and
	// composes/decomposes Latin diacritics consistently before the
	// case-fold pass.
	nfkc := norm.NFKC.String(s)
	// Decompose so we can strip combining marks individually.
	nfd := norm.NFD.String(nfkc)

	var b strings.Builder
	b.Grow(len(nfd))
	prevSpace := true // suppress leading whitespace
	for _, r := range nfd {
		switch {
		case unicode.Is(unicode.Mn, r):
			// Combining mark — drop. "Pokémon" → "Pokemon".
			continue
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			prevSpace = false
		case unicode.IsSpace(r):
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		default:
			// Punctuation, dashes, hyphens, etc. → soft separator.
			// Two adjacent punctuations don't double-space because
			// of the prevSpace guard.
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		}
	}
	out := b.String()
	// Trim trailing space the loop may have written before the last
	// run of punctuation.
	return strings.TrimRight(out, " ")
}
