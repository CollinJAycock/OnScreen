// Package artwork handles downloading artwork from metadata agents, writing
// it alongside media files (ADR-006), and serving resize-cached variants
// via the /photo/:/transcode proxy endpoint.
package artwork

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/mediastore"
	"github.com/onscreen/onscreen/internal/safehttp"
)

// maxArtworkRedirects caps how many 3xx hops the artwork client will
// chase. Refusing redirects outright (as webhook.SafeClient does) would
// break Cover Art Archive, which 307s every release-art request from
// coverartarchive.org to archive.org. Capping at a small number gives
// defense-in-depth against a malicious source trying to spin the
// fetcher through an arbitrarily-long redirect chain — typical artwork
// providers chain 0 or 1 hops; 3 is comfortable headroom and rejects
// abusive depths. The SSRF policy still gates every intermediate hop.
const maxArtworkRedirects = 3

// DownloadHTTPError is returned when an artwork URL responds with a
// non-200 status. Callers that surface user-supplied URLs (the manual
// poster picker) check for this with errors.As to render a 4xx with
// the actual upstream status — generic 500s leave operators guessing
// whether the URL was wrong or the server itself broke.
type DownloadHTTPError struct {
	URL        string
	StatusCode int
}

func (e *DownloadHTTPError) Error() string {
	return fmt.Sprintf("artwork download: status %d", e.StatusCode)
}

// Manager downloads and caches artwork (ADR-006).
type Manager struct {
	cachePath  string // resize cache path
	httpClient *http.Client

	// resizeSem caps concurrent JPEG decode+resize operations. Each
	// decode allocates the full RGBA image into memory (a 2000×3000
	// poster is ~24 MB), so an unbounded burst (e.g. a TV row scroll
	// firing 30 cards at once across many clients) ratchets the Go
	// heap_sys high-watermark. Even after the burst settles and live
	// heap drains, Go retains the OS pages and the operator sees RSS
	// frozen at the burst peak. Bounded concurrency keeps the burst
	// peak proportional to GOMAXPROCS instead of client-driven.
	resizeSem chan struct{}

	// store reads source artwork bytes. Optional — nil defaults to
	// mediastore.Local, so artwork is read straight from disk as before. When
	// object storage is configured, artwork that lives next to media (ADR-006)
	// is read from the bucket. The resize cache stays local regardless (it's a
	// regenerable server-side cache, not media bytes).
	store mediastore.Store

	// logger surfaces cache-write failures (disk full, perm denied, missing
	// cache dir) so operators can diagnose "every artwork miss" symptoms
	// instead of guessing. Nil-safe: when unset, failures are silent (the
	// historical behaviour). Wired via WithLogger from the server.
	logger *slog.Logger
}

// New creates an artwork Manager. The default HTTP client uses
// safehttp's policy so a malicious or compromised metadata source
// (TMDB, Cover Art Archive, etc.) can't return URLs that pivot the
// fetch into the operator's internal network — every dial is gated
// post-resolution against the loopback / RFC1918 / link-local /
// metadata-service ranges. The CheckRedirect override caps the
// redirect chain at maxArtworkRedirects hops (see the comment on
// that constant); a malicious source can't spin the fetcher through
// an unbounded redirect chain to exhaust the request budget, but
// legitimate providers that chain through a single redirect (Cover
// Art Archive → archive.org) still resolve cleanly.
func New(cachePath string) *Manager {
	client := safehttp.NewClient(safehttp.DialPolicy{}, 30*time.Second)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxArtworkRedirects {
			first := ""
			if len(via) > 0 {
				first = via[0].URL.String()
			}
			slog.Default().Warn("artwork download: redirect chain capped",
				"first_url", first,
				"current_target", req.URL.String(),
				"hop_count", len(via)+1,
				"cap", maxArtworkRedirects)
			return http.ErrUseLastResponse
		}
		return nil
	}
	return &Manager{
		cachePath:  cachePath,
		httpClient: client,
		resizeSem:  make(chan struct{}, runtime.GOMAXPROCS(0)),
	}
}

// WithHTTPClient overrides the HTTP client. Intended for tests that
// hit a httptest.NewServer (loopback, otherwise blocked by safehttp);
// production callers should use the default New constructor.
func (m *Manager) WithHTTPClient(c *http.Client) *Manager {
	m.httpClient = c
	return m
}

// WithMediaStore sets the backend used to read source artwork bytes. When unset,
// reads come from the local filesystem (mediastore.Local) — unchanged behaviour.
// Returns the Manager for chaining.
func (m *Manager) WithMediaStore(s mediastore.Store) *Manager {
	m.store = s
	return m
}

// WithLogger wires a slog.Logger so resize-cache-write failures and unusual
// source-mtime states surface in admin/logs. Without a logger, those signals
// are silent — historically that masked "every artwork hit triggers a fresh
// resize" symptoms (full cache disk, EACCES, future-dated source mtime) until
// someone shell'd into the box and ran df / stat manually.
func (m *Manager) WithLogger(l *slog.Logger) *Manager {
	m.logger = l
	return m
}

// warn logs a warning if a logger is configured; no-op otherwise.
func (m *Manager) warn(msg string, args ...any) {
	if m.logger != nil {
		m.logger.Warn(msg, args...)
	}
}

// Store returns the configured source backend, defaulting to local-filesystem
// when none is wired. Exposed so the artwork HTTP route can read the
// existence-check + full-size serve through the same backend Resize uses.
func (m *Manager) Store() mediastore.Store {
	if m.store == nil {
		return mediastore.Local{}
	}
	return m.store
}

// DownloadPoster downloads a poster image into absDir/poster.jpg.
// absDir is the absolute directory path (e.g. the folder containing the media file).
// Returns the absolute path of the saved file. Skips re-download if a file
// already exists at the target path.
func (m *Manager) DownloadPoster(ctx context.Context, _ uuid.UUID, url string, absDir string) (string, error) {
	return m.download(ctx, url, filepath.Join(absDir, "poster.jpg"), false)
}

// DownloadFanart downloads a fanart/background image into absDir/fanart.jpg.
func (m *Manager) DownloadFanart(ctx context.Context, _ uuid.UUID, url string, absDir string) (string, error) {
	return m.download(ctx, url, filepath.Join(absDir, "fanart.jpg"), false)
}

// DownloadArtistPoster downloads an artist poster into absDir/{itemID}-poster.jpg.
// The ID-qualified filename prevents collisions in flat music layouts where
// multiple artists share the same parent directory (e.g., /Music/Artist/track.flac
// causes artDir to resolve to /Music for every artist).
func (m *Manager) DownloadArtistPoster(ctx context.Context, itemID uuid.UUID, url string, absDir string) (string, error) {
	return m.download(ctx, url, filepath.Join(absDir, itemID.String()+"-poster.jpg"), false)
}

// DownloadArtistFanart is the fanart counterpart to DownloadArtistPoster.
func (m *Manager) DownloadArtistFanart(ctx context.Context, itemID uuid.UUID, url string, absDir string) (string, error) {
	return m.download(ctx, url, filepath.Join(absDir, itemID.String()+"-fanart.jpg"), false)
}

// DownloadThumb downloads an episode/track thumbnail into absDir/{uuid}.jpg.
func (m *Manager) DownloadThumb(ctx context.Context, itemID uuid.UUID, url string, absDir string) (string, error) {
	filename := itemID.String() + ".jpg"
	return m.download(ctx, url, filepath.Join(absDir, filename), false)
}

// ReplacePoster overwrites absDir/{itemID}-poster.jpg atomically. Used
// when the metadata enricher finds a confident match and should replace
// an existing poster (e.g. wrong embedded album art written during the
// initial scan).
//
// The filename is qualified by item ID so that flat library layouts —
// where multiple albums under one artist store tracks directly in the
// artist's folder — don't collide on a single shared poster.jpg. Same
// rationale as DownloadArtistPoster.
func (m *Manager) ReplacePoster(ctx context.Context, itemID uuid.UUID, url string, absDir string) (string, error) {
	return m.download(ctx, url, filepath.Join(absDir, itemID.String()+"-poster.jpg"), true)
}

// ReplaceShowPoster overwrites absDir/poster.jpg atomically. Used by
// the manual Fix Match flow on shows + movies to swap out the on-disk
// poster.jpg the previous match downloaded — without force, the
// existing-file short-circuit in download() would keep the old image
// even though poster_path now points at the same filename.
func (m *Manager) ReplaceShowPoster(ctx context.Context, _ uuid.UUID, url string, absDir string) (string, error) {
	return m.download(ctx, url, filepath.Join(absDir, "poster.jpg"), true)
}

// ReplaceShowFanart is the fanart counterpart of ReplaceShowPoster —
// overwrites absDir/fanart.jpg atomically.
func (m *Manager) ReplaceShowFanart(ctx context.Context, _ uuid.UUID, url string, absDir string) (string, error) {
	return m.download(ctx, url, filepath.Join(absDir, "fanart.jpg"), true)
}

// download fetches url and writes to absPath (absolute file path).
// Returns the absolute path. When force is false, skips re-download if a
// file already exists at absPath (the common cache-miss case). When force
// is true, writes the fresh bytes atomically over any existing file.
func (m *Manager) download(ctx context.Context, url, absPath string, force bool) (string, error) {
	if !force {
		if _, err := m.Store().Stat(ctx, absPath); err == nil {
			return strings.ReplaceAll(absPath, `\`, "/"), nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("artwork download: build request: %w", err)
	}
	// Wikimedia (and a few other hosts) reject the Go default
	// "Go-http-client/1.1" UA — set a real one so user-pasted poster
	// URLs from those sources work.
	req.Header.Set("User-Agent", "OnScreen/1.0 (+https://github.com/CollinJAycock/OnScreen)")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("artwork download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", &DownloadHTTPError{URL: url, StatusCode: resp.StatusCode}
	}

	// Buffer the image (capped) and write it through the store, so artwork lands
	// next to media on disk or in object storage. Local.Put still writes
	// atomically (temp file + rename) and creates parent dirs.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	if err != nil {
		return "", fmt.Errorf("artwork read: %w", err)
	}
	putter, ok := m.Store().(mediastore.Putter)
	if !ok {
		return "", fmt.Errorf("artwork: media store is read-only, cannot write %s", absPath)
	}
	if err := putter.Put(ctx, absPath, data); err != nil {
		return "", fmt.Errorf("artwork put: %w", err)
	}

	// Normalize to forward slashes so paths work in URLs on all platforms.
	return strings.ReplaceAll(absPath, `\`, "/"), nil
}

// Resize serves a resized artwork image, writing the result to the cache.
// If the cache entry exists and the source hasn't changed (mtime), the cached
// version is served directly (ADR-006).
//
// sourcePath is absolute. width/height of 0 means unconstrained.
func (m *Manager) Resize(ctx context.Context, w io.Writer, sourcePath string, width, height int) error {
	// Cache key: SHA-256 of source path + WxH.
	cacheKey := cacheKeyFor(sourcePath, width, height)
	cachePath := filepath.Join(m.cachePath, cacheKey+".jpg")

	// Source mtime gates cache validity (ADR-006). Read it through the media
	// store so artwork stored next to media in object storage is reachable; for
	// the default local backend this is a plain stat.
	srcInfo, err := m.Store().Stat(ctx, sourcePath)
	if err != nil {
		return fmt.Errorf("stat source image: %w", err)
	}

	// Check if cache is valid (source mtime hasn't changed). Cache hits
	// are pure local file IO (small JPEG → response writer); they bypass the
	// resize semaphore so a TV row of cached cards never waits on
	// in-flight decodes.
	if cacheFreshSince(srcInfo.ModTime, cachePath) {
		cf, err := os.Open(cachePath)
		if err == nil {
			defer cf.Close()
			_, err = io.Copy(w, cf)
			return err
		}
	}

	// Cache miss → bound concurrent decodes. See Manager.resizeSem.
	// Honour the request context so a navigated-away client (TV scroll
	// past a card) doesn't sit waiting for a slot.
	if m.resizeSem != nil {
		select {
		case m.resizeSem <- struct{}{}:
			defer func() { <-m.resizeSem }()
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Decode + resize + encode through the build-tagged backend. Default
	// build uses pure-Go image/jpeg + golang.org/x/image/draw.BiLinear.
	// `go build -tags vips` swaps in libvips via govips, which is roughly
	// 10–20× faster on cold-cache hits and uses a tiny fraction of the
	// per-decode memory (no full RGBA materialisation). See
	// resize_stdlib.go / resize_vips.go for the two implementations.
	sf, err := m.Store().Open(ctx, sourcePath)
	if err != nil {
		return fmt.Errorf("open source image: %w", err)
	}
	defer sf.Close()
	resized, err := decodeResizeEncodeJPEG(sf, width, height, jpegQuality)
	if err != nil {
		return fmt.Errorf("resize: %w", err)
	}

	// Write the cache entry via a temp file + atomic rename so concurrent
	// requests for the same poster can't observe a half-written JPEG (each
	// writer creates its own .tmp.<rand>, and the last Rename wins).
	// Failures used to be silent — operators with a full cache disk or a
	// EACCES on the cache dir would see "every hit re-resizes" and have no
	// breadcrumb in the logs. The warn() calls fix that.
	cacheWritten := false
	if err := os.MkdirAll(m.cachePath, 0o755); err != nil {
		m.warn("artwork cache: mkdir failed (resize result not cached)",
			"cache_path", m.cachePath, "err", err)
	} else if cf, err := os.CreateTemp(m.cachePath, "resize-*.tmp"); err != nil {
		m.warn("artwork cache: temp-file create failed (resize result not cached)",
			"cache_path", m.cachePath, "err", err)
	} else {
		tmpPath := cf.Name()
		_, writeErr := cf.Write(resized)
		closeErr := cf.Close()
		switch {
		case writeErr != nil:
			m.warn("artwork cache: write to temp file failed (resize result not cached)",
				"source", sourcePath, "tmp", tmpPath, "err", writeErr)
			_ = os.Remove(tmpPath)
		case closeErr != nil:
			m.warn("artwork cache: temp-file close failed (resize result not cached)",
				"tmp", tmpPath, "err", closeErr)
			_ = os.Remove(tmpPath)
		default:
			if rerr := os.Rename(tmpPath, cachePath); rerr != nil {
				m.warn("artwork cache: rename failed (resize result not cached)",
					"tmp", tmpPath, "target", cachePath, "err", rerr)
				_ = os.Remove(tmpPath)
			} else {
				cacheWritten = true
			}
		}
	}
	if !cacheWritten {
		// Flag a single warn for the source-mtime-is-future case — that
		// silently invalidates the cache on every request. mtimes from a
		// recent metadata refresh that downloaded a fresh poster are
		// expected; an mtime materially in the future (clock skew, file
		// touched by a sync utility) is not.
		if skew := time.Until(srcInfo.ModTime); skew > 5*time.Minute {
			m.warn("artwork cache: source mtime is in the future — cache will never be considered fresh until clock catches up",
				"source", sourcePath, "src_mtime", srcInfo.ModTime, "skew", skew.String())
		}
	}

	_, err = w.Write(resized)
	return err
}

func cacheKeyFor(path string, w, h int) string {
	h256 := sha256.Sum256([]byte(path + "|" + strconv.Itoa(w) + "x" + strconv.Itoa(h)))
	return hex.EncodeToString(h256[:16])
}

// cacheFreshSince reports whether the local cache file was written after the
// source's last modification. The source mtime is supplied by the caller (read
// via the media store) so the source need not be on the local filesystem; the
// cache always is.
func cacheFreshSince(srcModTime time.Time, cachePath string) bool {
	cacheInfo, err := os.Stat(cachePath)
	if err != nil {
		return false
	}
	return cacheInfo.ModTime().After(srcModTime)
}
