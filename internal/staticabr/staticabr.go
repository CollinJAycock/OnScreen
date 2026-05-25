// Package staticabr plans and locates pre-encoded ("static") ABR ladders for
// popular titles (HA roadmap §5). Live ABR segments are per-session and never
// cache; pre-encoding the ladder once for the hottest titles lets their segments
// serve from object storage / a CDN forever, so the live-transcode fleet only
// handles the cold tail — "scales with cache" instead of "scales with GPUs".
//
// This package is the shared foundation: the media-store key scheme used by both
// the (future) encoder that Puts segments and the server that hands out their
// SignedURLs, plus the popularity-driven plan that decides what to (re)encode.
// Encode execution (fleet jobs that write segments) and serve-from-static (the
// ABR handler redirecting to these keys) build on top of it.
package staticabr

import (
	"context"
	"errors"
	"io"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/mediastore"
)

// rootPrefix namespaces every pre-encoded ladder within the media store, so a
// single Walk(rootPrefix) enumerates or prunes all static output.
const rootPrefix = "static-abr"

// Prefix is the media-store key prefix holding a file's entire pre-encoded
// ladder — use it to list or delete the ladder (e.g. on invalidation).
func Prefix(fileID uuid.UUID) string {
	return path.Join(rootPrefix, fileID.String())
}

// MasterKey is the multivariant master playlist key for a file's ladder.
func MasterKey(fileID uuid.UUID) string {
	return path.Join(Prefix(fileID), "master.m3u8")
}

// RungPlaylistKey is the media playlist key for one rung (rung is a
// transcode.Rendition.Label, e.g. "1080p").
func RungPlaylistKey(fileID uuid.UUID, rung string) string {
	return path.Join(Prefix(fileID), rung, "index.m3u8")
}

// SegmentKey is the key for a single segment file within a rung (name is the
// segment filename, e.g. "seg00007.m4s").
func SegmentKey(fileID uuid.UUID, rung, name string) string {
	return path.Join(Prefix(fileID), rung, name)
}

// HashKey is the sidecar key recording the source content hash the ladder was
// built from. The server/planner compares it to the file's current hash so a
// replaced or re-encoded source invalidates (and re-triggers) the ladder.
func HashKey(fileID uuid.UUID) string {
	return path.Join(Prefix(fileID), "source.hash")
}

// Title is a pre-encode candidate: a source file, its current content hash (for
// invalidation), and how often it has been played.
type Title struct {
	FileID     uuid.UUID
	SourceHash string
	PlayCount  int
}

// EncodedChecker reports whether a file's static ladder already exists and was
// built from sourceHash. A missing ladder, or one built from a different hash
// (source replaced / re-encoded), counts as not-encoded so Plan re-triggers it.
type EncodedChecker interface {
	IsEncoded(ctx context.Context, fileID uuid.UUID, sourceHash string) (bool, error)
}

// StoreChecker is an EncodedChecker backed by the media store: a ladder counts as
// encoded only when its master playlist exists AND the recorded source hash
// matches. A master without a hash sidecar is treated as stale (re-encode), so a
// half-written ladder self-heals.
//
// Prefix is prepended to every static key — "" for bucket-relative object storage,
// or a directory for a local static root. It must match the encoder's keyPrefix
// and the server's static root (all from STATIC_ABR_ROOT).
type StoreChecker struct {
	Store  mediastore.Store
	Prefix string
}

// IsEncoded implements EncodedChecker.
func (c StoreChecker) IsEncoded(ctx context.Context, fileID uuid.UUID, sourceHash string) (bool, error) {
	if _, err := c.Store.Stat(ctx, path.Join(c.Prefix, MasterKey(fileID))); err != nil {
		if errors.Is(err, mediastore.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	f, err := c.Store.Open(ctx, path.Join(c.Prefix, HashKey(fileID)))
	if err != nil {
		if errors.Is(err, mediastore.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	stored, err := io.ReadAll(io.LimitReader(f, 256))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(stored)) == sourceHash, nil
}

// Plan returns the candidates that need (re)encoding: those clearing minPlays
// whose ladder is absent or stale. Candidates are assumed pre-sorted by
// popularity (most-played first); limit caps the result (0 = no cap) so a run
// pre-encodes the hottest titles first and bounds fleet load. A candidate with
// no source hash is skipped — invalidation can't be tracked without one.
func Plan(ctx context.Context, candidates []Title, checker EncodedChecker, minPlays, limit int) ([]Title, error) {
	var out []Title
	for _, c := range candidates {
		if c.PlayCount < minPlays || c.SourceHash == "" {
			continue
		}
		encoded, err := checker.IsEncoded(ctx, c.FileID, c.SourceHash)
		if err != nil {
			return nil, err
		}
		if encoded {
			continue
		}
		out = append(out, c)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
