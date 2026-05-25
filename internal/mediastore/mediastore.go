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
	"path/filepath"
	"strings"
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

// ObjectInfo identifies one stored object during a Walk: a Store key plus its
// size and modtime, so a caller (the scanner) can apply its mtime+size fast-skip
// without a second Stat.
type ObjectInfo struct {
	Key     string
	Size    int64
	ModTime time.Time
}

// Lister is an optional Store capability: enumerating stored objects under a
// prefix. The scanner (file discovery) and static-ABR pre-encode (enumerating
// popular titles) use it. It's separate from Store because the serving /
// transcode / artwork read paths don't need it, and a backend that genuinely
// can't list shouldn't be forced to fake it.
type Lister interface {
	// Walk calls fn for every object under prefix, recursively, in unspecified
	// order. A non-nil error from fn (or context cancellation) stops the walk and
	// is returned. Keys yielded are in the same namespace Open/Stat accept, so a
	// yielded ObjectInfo.Key can be passed straight back to Open.
	Walk(ctx context.Context, prefix string, fn func(ObjectInfo) error) error
}

// Putter is an optional Store capability: writing derived artifacts back to the
// store — extracted cover art today, pre-encoded static-ABR segments later.
// Separate from Store because the read paths (serve / transcode / scan source)
// don't need it.
type Putter interface {
	// Put writes data at key, overwriting any existing object. For Local the key
	// is a filesystem path (parent directories are created); for S3 it maps to the
	// bucket object key.
	Put(ctx context.Context, key string, data []byte) error
}

// PathMapping rewrites a key prefix. It's the content-addressing primitive for
// multi-site active/passive DR: a standby site resolves the primary's absolute
// FilePaths (carried verbatim in the replicated Postgres) against its own mount
// point. From is matched as a literal path prefix; To replaces it.
type PathMapping struct {
	From string
	To   string
}

// Local serves bytes from the local filesystem; the key is an absolute path.
// This is the default backend and preserves the pre-abstraction behaviour.
//
// Remap is empty by default (identity — the local-disk default every install
// uses). At a multi-site secondary, set it so a replicated DB's primary-site
// paths resolve against the local mount; see PathMapping.
type Local struct {
	Remap []PathMapping
}

// resolve applies the first matching prefix remap, or returns key unchanged.
func (l Local) resolve(key string) string {
	for _, m := range l.Remap {
		if m.From != "" && strings.HasPrefix(key, m.From) {
			return m.To + strings.TrimPrefix(key, m.From)
		}
	}
	return key
}

// Open implements Store.
func (l Local) Open(_ context.Context, key string) (io.ReadSeekCloser, error) {
	f, err := os.Open(l.resolve(key))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// Stat implements Store.
func (l Local) Stat(_ context.Context, key string) (FileInfo, error) {
	fi, err := os.Stat(l.resolve(key))
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
func (l Local) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

// Walk implements Lister by walking the local directory tree rooted at prefix.
// Directories are descended but only files are yielded; the key is the file's
// path (the same value Open/Stat accept). Walk does not apply Remap — scanning
// happens at the primary site, where paths are already local.
func (l Local) Walk(ctx context.Context, prefix string, fn func(ObjectInfo) error) error {
	return filepath.WalkDir(prefix, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fn(ObjectInfo{Key: path, Size: info.Size(), ModTime: info.ModTime()})
	})
}

// Put implements Putter, writing atomically (temp file + rename) so a crash or
// concurrent reader never sees a partial file. Parent directories are created.
// Honours Remap so a multi-site standby writes to its own mount.
func (l Local) Put(_ context.Context, key string, data []byte) error {
	p := l.resolve(key)
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mediastore-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, p); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
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
