package transcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/onscreen/onscreen/internal/ffsafe"
)

// ── Stacked (multi-part) movies ───────────────────────────────────────────────
//
// A movie split across files ("cd1" + "cd2") is streamed as ONE continuous
// movie so a client that knows nothing about parts still plays all of it: the
// API resolves the parts from the database (media.Stack), forces the session
// through this pipeline (no direct play), and hands the worker every part in
// order. The worker writes a concat list naming exactly those parts — never
// anything a request supplied — and feeds it to ffmpeg through
// ffsafe.ConcatInputArgs, the one sanctioned use of the concat demuxer. Library
// files that ARE concat lists are still refused at scan and playback.
//
// The combined timeline is the parts back to back: part N starts at the sum of
// the durations before it. The list carries those durations, which is what
// lets ffmpeg seek across parts at all.

// SourcePart is one file of a stacked movie, in play order.
type SourcePart struct {
	FileID   string `json:"file_id"`
	FilePath string `json:"file_path"`
	// SourceURL is this part's own HTTP fallback (/media/stream/{file_id}
	// with a stream token bound to that file), for a worker that can't
	// reach FilePath. Empty when none could be minted.
	SourceURL  string `json:"source_url,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// PartsDurationSec is the combined running time of parts, 0 when any part's
// length is unknown.
func PartsDurationSec(parts []SourcePart) float64 {
	var total int64
	for _, p := range parts {
		if p.DurationMS <= 0 {
			return 0
		}
		total += p.DurationMS
	}
	return float64(total) / 1000
}

// concatListDir holds the worker's concat lists: beside the session
// directories, never inside one, because a session directory is served to
// players and a list of part URLs carries their stream tokens.
var concatListDir = filepath.Join(os.TempDir(), "onscreen", "concat")

// errPartsUnreachable: a part is neither on this worker's filesystem nor
// available as a source URL.
var errPartsUnreachable = errors.New("stacked source: a part is neither reachable locally nor offered as a source URL")

// resolvePartInputs picks how this worker reads the parts: every local path
// when all of them are reachable here, else every source URL when all of them
// have one. Never a mix — ffsafe refuses a list that blends local paths and
// URLs, so a local part never runs under the network allowlist. remote
// reports which was chosen.
func resolvePartInputs(parts []SourcePart, reachable func(path string) bool) (entries []ffsafe.ConcatPart, remote bool, err error) {
	allLocal, allURL := true, true
	for _, p := range parts {
		if !reachable(p.FilePath) {
			allLocal = false
		}
		if p.SourceURL == "" {
			allURL = false
		}
	}
	if !allLocal && !allURL {
		return nil, false, errPartsUnreachable
	}
	for _, p := range parts {
		in := p.FilePath
		if !allLocal {
			in = p.SourceURL
		}
		entries = append(entries, ffsafe.ConcatPart{Input: in, DurationSec: float64(p.DurationMS) / 1000})
	}
	return entries, !allLocal, nil
}

// writeConcatList writes the job's concat list and returns its path.
func writeConcatList(name string, entries []ffsafe.ConcatPart) (string, error) {
	if err := os.MkdirAll(concatListDir, 0o700); err != nil {
		return "", fmt.Errorf("concat list dir: %w", err)
	}
	path := filepath.Join(concatListDir, name+".ffconcat")
	if err := ffsafe.WriteConcatList(path, entries); err != nil {
		return "", err
	}
	return path, nil
}

// FindPreviousKeyframeInParts is FindPreviousKeyframe on the combined
// timeline: the keyframe at or before targetSec inside the part it falls in,
// as a combined position. A copy session seeked to targetSec starts there —
// the concat demuxer seeks within that part exactly as it would the file
// alone. Returns targetSec when the parts' lengths are unknown.
func FindPreviousKeyframeInParts(ctx context.Context, parts []SourcePart, targetSec float64) float64 {
	if targetSec <= 0 {
		return 0
	}
	if PartsDurationSec(parts) <= 0 {
		return targetSec
	}
	start := 0.0
	for i, p := range parts {
		dur := float64(p.DurationMS) / 1000
		if targetSec < start+dur || i == len(parts)-1 {
			return start + FindPreviousKeyframe(ctx, p.FilePath, targetSec-start)
		}
		start += dur
	}
	return targetSec
}
