package digest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// Resizer writes a resized JPEG of an absolute artwork path, using the
// artwork cache. Satisfied by *artwork.Manager.
type Resizer interface {
	Resize(ctx context.Context, w io.Writer, sourcePath string, width, height int) error
}

// Statter reports whether an absolute path exists in the media store.
type Statter func(ctx context.Context, absPath string) bool

// ArtworkPosters loads posters for inline attachment the way the /artwork
// route finds them: a library-relative path under one of the owning
// library's scan paths, image extensions only, never escaping the root.
// Images are resized to a small poster so a digest stays a few hundred KB.
type ArtworkPosters struct {
	// ScanPaths returns the library's scan paths (nil when unknown).
	ScanPaths func(ctx context.Context, libraryID uuid.UUID) []string
	Exists    Statter
	Resizer   Resizer
}

// posterPixelsW is twice the rendered tile width so it stays sharp on high-DPI
// screens; the height follows the image's aspect ratio.
const posterPixelsW = 192

var errNoPoster = errors.New("digest: poster not found")

// imageExts are the artwork extensions a poster may have (the /artwork
// route's allowlist, which keeps media files out).
var imageExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true, ".tbn": true}

// cleanRel validates a stored artwork path: relative, inside its root, an
// image. Remote URLs are refused (the digest never fetches anything).
func cleanRel(rel string) (string, bool) {
	if rel == "" || strings.Contains(rel, "://") {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(rel, "/")))
	if clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", false
	}
	if !imageExts[strings.ToLower(filepath.Ext(clean))] {
		return "", false
	}
	return clean, true
}

// Poster implements PosterSource.
func (p ArtworkPosters) Poster(ctx context.Context, libraryID uuid.UUID, relPath string) ([]byte, error) {
	clean, ok := cleanRel(relPath)
	if !ok || p.ScanPaths == nil || p.Resizer == nil {
		return nil, errNoPoster
	}
	for _, root := range p.ScanPaths(ctx, libraryID) {
		abs := filepath.Join(root, clean)
		if p.Exists != nil && !p.Exists(ctx, abs) {
			continue
		}
		var buf bytes.Buffer
		if err := p.Resizer.Resize(ctx, &buf, abs, posterPixelsW, 0); err != nil {
			continue
		}
		return buf.Bytes(), nil
	}
	return nil, errNoPoster
}
