// Package mediastore abstracts where media bytes live so the streaming and scan
// tiers stop assuming a local filesystem path. It is the HA roadmap's storage
// step (docs/ha-roadmap.md §3): the local backend wraps os.Open and is the
// default, while a future object-storage backend (S3/GCS) can either range-read
// or — better — hand back a signed URL a CDN/client fetches directly, taking the
// bytes off the app tier entirely.
//
// The introduction is deliberately non-breaking: today the only backend is
// Local, whose SignedURL returns "" (can't offload), so Serve streams through
// the app via http.ServeContent exactly as the previous http.ServeFile did.
//
// Keys: a key is whatever locates the bytes for a backend. For Local the key is
// the absolute FilePath the scanner already stores, so no schema change is
// needed now. A stable content key resolved per-site (for multi-site DR) is a
// later step — see the roadmap's "Content addressing" gap.
package mediastore

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"time"
)

// ErrNotFound is returned by a backend when the key does not exist, so callers
// (HTTP handlers, the scanner) can map it to a 404 / skip instead of a 500.
var ErrNotFound = errors.New("mediastore: not found")

// FileInfo is the backend-agnostic subset of os.FileInfo the serving and scan
// tiers actually need: Size for Content-Length, ModTime for caching /
// If-Modified-Since and the scanner's mtime+size hash-skip (ADR-011).
type FileInfo struct {
	Size    int64
	ModTime time.Time
}

// Store abstracts media-byte storage. Implementations must be safe for
// concurrent use.
type Store interface {
	// Open returns a range-seekable reader for playback input (direct play,
	// remux/transcode source, scan probe). The reader backs HTTP Range via Seek.
	// Returns ErrNotFound when key is absent.
	Open(ctx context.Context, key string) (io.ReadSeekCloser, error)

	// Stat returns size + modtime. Returns ErrNotFound when key is absent.
	Stat(ctx context.Context, key string) (FileInfo, error)

	// SignedURL returns a short-lived URL a CDN or client can fetch directly,
	// bypassing the app tier. Returns "" (nil error) when the backend can't
	// offload — e.g. local FS — so the caller falls back to streaming through
	// the app via Serve.
	SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Local serves bytes from the local filesystem; the key is an absolute path.
// This is the default backend and preserves the pre-abstraction behaviour.
type Local struct{}

// Open implements Store.
func (Local) Open(_ context.Context, key string) (io.ReadSeekCloser, error) {
	f, err := os.Open(key)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// Stat implements Store.
func (Local) Stat(_ context.Context, key string) (FileInfo, error) {
	fi, err := os.Stat(key)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return FileInfo{}, ErrNotFound
		}
		return FileInfo{}, err
	}
	return FileInfo{Size: fi.Size(), ModTime: fi.ModTime()}, nil
}

// SignedURL implements Store. Local FS can't offload to a CDN, so it returns ""
// and the caller streams through the app.
func (Local) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

// Serve writes the media identified by key to w, honouring HTTP Range,
// If-Modified-Since, and content-type sniffing (via name's extension) — the same
// semantics as http.ServeFile. If the store can hand back a signed URL (object
// storage / CDN), it 302-redirects the client there and takes the bytes off the
// app tier; otherwise it streams through the app via http.ServeContent.
//
// name is used only for content-type detection and should be the basename of the
// key. ttl bounds the signed URL's validity when offloading.
//
// The caller is responsible for any auth/ACL checks and for response headers it
// wants set before calling (Content-Disposition, write-deadline reset, etc.).
func Serve(w http.ResponseWriter, r *http.Request, store Store, key, name string, ttl time.Duration) error {
	if signed, err := store.SignedURL(r.Context(), key, ttl); err == nil && signed != "" {
		http.Redirect(w, r, signed, http.StatusFound)
		return nil
	}

	info, err := store.Stat(r.Context(), key)
	if err != nil {
		return err
	}
	f, err := store.Open(r.Context(), key)
	if err != nil {
		return err
	}
	defer f.Close()

	http.ServeContent(w, r, name, info.ModTime, f)
	return nil
}
