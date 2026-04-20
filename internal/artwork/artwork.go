// Package artwork handles downloading artwork from metadata agents, writing
// it alongside media files (ADR-006), and serving resize-cached variants
// via the /photo/:/transcode proxy endpoint.
package artwork

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"

	"github.com/google/uuid"
)

// Manager downloads and caches artwork (ADR-006).
type Manager struct {
	cachePath  string // resize cache path
	httpClient *http.Client
}

// New creates an artwork Manager.
func New(cachePath string) *Manager {
	return &Manager{
		cachePath:  cachePath,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
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

// DownloadThumb downloads an episode/track thumbnail into absDir/{uuid}.jpg.
func (m *Manager) DownloadThumb(ctx context.Context, itemID uuid.UUID, url string, absDir string) (string, error) {
	filename := itemID.String() + ".jpg"
	return m.download(ctx, url, filepath.Join(absDir, filename), false)
}

// ReplacePoster overwrites absDir/poster.jpg atomically. Used when the
// metadata enricher finds a confident match and should replace an existing
// poster (e.g. wrong embedded album art written during the initial scan).
func (m *Manager) ReplacePoster(ctx context.Context, _ uuid.UUID, url string, absDir string) (string, error) {
	return m.download(ctx, url, filepath.Join(absDir, "poster.jpg"), true)
}

// download fetches url and writes to absPath (absolute file path).
// Returns the absolute path. When force is false, skips re-download if a
// file already exists at absPath (the common cache-miss case). When force
// is true, writes the fresh bytes atomically over any existing file.
func (m *Manager) download(ctx context.Context, url, absPath string, force bool) (string, error) {
	if !force {
		if _, err := os.Stat(absPath); err == nil {
			return strings.ReplaceAll(absPath, `\`, "/"), nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("artwork download: build request: %w", err)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("artwork download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("artwork download: status %d", resp.StatusCode)
	}

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("artwork mkdir: %w", err)
	}

	// Write to a temp file in the same directory, then rename to the final path
	// atomically. This prevents corrupt partial files from blocking future downloads.
	tmp, err := os.CreateTemp(dir, ".artwork-*.tmp")
	if err != nil {
		return "", fmt.Errorf("artwork create temp: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 50*1024*1024)); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("artwork write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("artwork close temp: %w", err)
	}
	if err := os.Rename(tmpPath, absPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("artwork rename: %w", err)
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

	// Check if cache is valid (source mtime hasn't changed).
	if isCacheValid(sourcePath, cachePath) {
		cf, err := os.Open(cachePath)
		if err == nil {
			defer cf.Close()
			_, err = io.Copy(w, cf)
			return err
		}
	}

	// Decode source image.
	sf, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source image: %w", err)
	}
	defer sf.Close()

	src, _, err := image.Decode(sf)
	if err != nil {
		return fmt.Errorf("decode image: %w", err)
	}

	// Calculate target dimensions preserving aspect ratio.
	dst := resize(src, width, height)

	// Write to cache.
	if err := os.MkdirAll(m.cachePath, 0o755); err == nil {
		if cf, err := os.Create(cachePath); err == nil {
			_ = jpeg.Encode(cf, dst, &jpeg.Options{Quality: 90})
			cf.Close()
		}
	}

	return jpeg.Encode(w, dst, &jpeg.Options{Quality: 90})
}

func resize(src image.Image, maxW, maxH int) image.Image {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()

	if maxW == 0 && maxH == 0 {
		return src
	}

	var dstW, dstH int
	if maxW == 0 {
		// Constrain height only.
		dstH = maxH
		dstW = srcW * maxH / srcH
	} else if maxH == 0 {
		// Constrain width only.
		dstW = maxW
		dstH = srcH * maxW / srcW
	} else {
		// Fit within both constraints, preserve aspect ratio.
		if srcW*maxH > srcH*maxW {
			dstW = maxW
			dstH = srcH * maxW / srcW
		} else {
			dstH = maxH
			dstW = srcW * maxH / srcH
		}
	}

	if dstW <= 0 {
		dstW = 1
	}
	if dstH <= 0 {
		dstH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

func cacheKeyFor(path string, w, h int) string {
	h256 := sha256.Sum256([]byte(path + "|" + strconv.Itoa(w) + "x" + strconv.Itoa(h)))
	return hex.EncodeToString(h256[:16])
}

func isCacheValid(sourcePath, cachePath string) bool {
	srcInfo, err := os.Stat(sourcePath)
	if err != nil {
		return false
	}
	cacheInfo, err := os.Stat(cachePath)
	if err != nil {
		return false
	}
	// Cache is valid if it was written after the source was last modified.
	return cacheInfo.ModTime().After(srcInfo.ModTime())
}
