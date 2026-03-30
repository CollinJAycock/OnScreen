package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// sampleSize is the number of bytes read from the start, middle, and end of a
// file to compute a fast partial hash. 4 MiB per region gives a 12 MiB read
// regardless of file size, making hashing of large media files sub-second
// while still being collision-resistant enough for move detection (ADR-011).
const sampleSize = 4 * 1024 * 1024 // 4 MiB

// fileInfo caches mtime + size to avoid recomputing hashes unnecessarily (ADR-011).
type fileInfo struct {
	mtime time.Time
	size  int64
	hash  string
}

const maxHashCacheEntries = 50000

var (
	hashCacheMu sync.Mutex
	hashCache   = map[string]fileInfo{}
)

// HashFile computes a fast partial hash of a file using samples from the
// beginning, middle, and end (plus the file size). The result is cached
// keyed by (path, mtime, size) so re-scanning an unchanged file is free.
func HashFile(ctx context.Context, path string, info os.FileInfo) (*string, error) {
	hashCacheMu.Lock()
	if cached, ok := hashCache[path]; ok {
		if cached.mtime.Equal(info.ModTime()) && cached.size == info.Size() {
			hashCacheMu.Unlock()
			s := cached.hash
			return &s, nil
		}
	}
	hashCacheMu.Unlock()

	hash, err := computeHash(ctx, path, info.Size())
	if err != nil {
		return nil, err
	}

	hashCacheMu.Lock()
	if len(hashCache) >= maxHashCacheEntries {
		clear(hashCache)
	}
	hashCache[path] = fileInfo{
		mtime: info.ModTime(),
		size:  info.Size(),
		hash:  hash,
	}
	hashCacheMu.Unlock()

	return &hash, nil
}

// computeHash reads up to sampleSize bytes from three regions of the file
// (start, middle, end) and hashes them together with the file size.
// For files smaller than 3×sampleSize the entire file is hashed.
func computeHash(ctx context.Context, path string, size int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("hash cancelled: %w", err)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open for hash: %w", err)
	}
	defer f.Close()

	h := sha256.New()

	// Mix in the file size so that truncated files produce different hashes.
	var sizeBuf [8]byte
	binary.LittleEndian.PutUint64(sizeBuf[:], uint64(size))
	h.Write(sizeBuf[:])

	if size <= int64(3*sampleSize) {
		// Small file: hash the whole thing.
		if _, err := io.Copy(h, f); err != nil {
			return "", fmt.Errorf("read for hash: %w", err)
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}

	// Large file: sample start, middle, and end.
	regions := []int64{
		0,
		size/2 - int64(sampleSize)/2,
		size - int64(sampleSize),
	}

	buf := make([]byte, sampleSize)
	for _, offset := range regions {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("hash cancelled: %w", err)
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return "", fmt.Errorf("seek for hash: %w", err)
		}
		n, err := io.ReadFull(f, buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err != nil && err != io.ErrUnexpectedEOF {
			return "", fmt.Errorf("read for hash: %w", err)
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
