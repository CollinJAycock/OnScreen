package scanner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/ffsafe"
)

// Integrity verdict values stored in media_files.integrity_status. Aliased
// from the media domain (the single source, mirroring the
// media_files_integrity_status_check constraint in migration 00018) so
// probe-side code reads naturally as scanner.IntegrityOK.
const (
	IntegrityUnchecked = media.IntegrityUnchecked
	IntegrityOK        = media.IntegrityOK
	IntegrityDamaged   = media.IntegrityDamaged
)

const (
	// integrityFramesPerPoint — how many frames each spot-decode must produce.
	// Five frames past the seek point proves the advertised keyframe is a real
	// random-access point AND that inter-coded frames after it decode; one
	// frame would pass on a file whose keyframes are intact but whose slices
	// are garbage. Falling SHORT of the request is itself the primary damage
	// signal: a healthy file at any real frame rate fills 5 frames from a
	// 15 s window instantly, while a corrupt region often produces a partial
	// count with an EMPTY stderr — the TS/H.26x parsers silently discard
	// bytes with no start codes rather than logging errors (observed on the
	// synthetic fixture: 1 frame, zero stderr lines, exit 0).
	integrityFramesPerPoint = 5

	// integrityWindowSec bounds how much input each point reads (-t before
	// -i). Without it, a point where nothing decodes makes ffmpeg chew
	// through the rest of the file hunting for its 5 frames — on a damaged
	// 20 GB remux that's minutes of I/O per point instead of seconds. Must
	// comfortably exceed the longest real-world GOP (~12.5 s, x264 keyint
	// 300 at 24 fps): on index-less containers (MPEG-TS) the seek lands
	// mid-GOP and the decoder emits nothing until the next keyframe, so a
	// window shorter than a GOP would read a healthy file as damaged.
	integrityWindowSec = 15

	// integrityPointTimeout is the hard per-point budget: seek + a 12 s
	// demux window + 5 frames of software decode is single-digit seconds on
	// healthy local files; the headroom is for NAS latency and cold disks.
	// Hitting it is treated as a probe failure (fail open), not damage.
	integrityPointTimeout = 45 * time.Second

	// integrityErrorThreshold — decoder-error lines on stderr before a point
	// is called damaged even though some frames decoded. Real bitstream
	// damage spews errors per slice (the QA fake logs hundreds of "Failed to
	// parse header of NALU" lines); a healthy file at -loglevel error is
	// silent, but leave margin for a stray open-GOP seek complaint.
	integrityErrorThreshold = 8

	// integrityDetailMax caps the stored detail string; it's a diagnostic
	// summary for the admin UI, not a log archive.
	integrityDetailMax = 500
)

// integrityOffsets are the probe positions as fractions of the file's
// duration. The head is deliberately not probed: damaged/fake releases
// typically decode fine for the first seconds (the QA fake plays exactly
// 2 s) — it's the mid-stream regions, which playback only reaches after the
// user commits to watching, that expose them. 90% also catches truncated
// downloads whose container still advertises the full duration.
var integrityOffsets = []float64{0.10, 0.50, 0.90}

// IntegrityReport is the outcome of a CheckIntegrity pass.
type IntegrityReport struct {
	// Status is IntegrityOK or IntegrityDamaged. (IntegrityUnchecked is
	// never returned — a pass that can't reach a verdict returns an error.)
	Status string
	// Detail is the human-readable failure summary for a damaged verdict
	// (offset, frames decoded, first decoder error); nil when Status is ok.
	Detail *string
}

// integrityPoint is one spot-decode outcome.
type integrityPoint struct {
	offsetSec int64
	pct       int
	// minFrames is the decoded-frame count below which the point reads as
	// damaged. Offset probes require the full request (see
	// integrityFramesPerPoint); the head probe of a very short clip accepts
	// any output, since a sub-second file can legitimately hold fewer than
	// 5 frames.
	minFrames int
	frames    int
	errLines  int
	firstErr  string
	damaged   bool
}

// CheckIntegrity spot-decodes a few frames at several offsets of a video
// file with the SOFTWARE decoder and reports whether the bitstream is
// actually decodable. It exists because a damaged or fake release can pass
// every header-level check (VerifySource, ProbeFile) and still be
// unplayable: the container advertises mid-stream keyframes that are not
// decodable random-access points, software decode fails NALU parsing past
// the first seconds, and hardware decoders emit dead-chroma green frames
// instead of erroring (see internal/transcode/chromacheck.go for the
// playback-side symptom this catches at scan time).
//
// durationMS is the container duration from the scan-time probe (used to
// place the offsets); <= 0 falls back to a single spot-decode at the head.
//
// Verdict contract, mirroring chromacheck's fail-open stance: a "damaged"
// report requires positive evidence (a point where ffmpeg ran and could not
// decode). Anything that prevents a point from running — missing file,
// missing ffmpeg, timeout, no video stream — returns an error so the caller
// leaves the file unchecked rather than branding it damaged; a broken tool
// must never condemn a library. Damage at ANY point wins over errors at
// others: one undecodable region is enough to make playback fail.
//
// Note: paths are read directly, so object-store libraries (whose scan-time
// probe goes through mediastore SignedURLs, scanner.go probeURLTTL) fail
// os.Stat here and stay unchecked. Wire a URL source before enabling the
// probe for those.
func CheckIntegrity(ctx context.Context, path string, durationMS int64) (IntegrityReport, error) {
	if _, err := os.Stat(path); err != nil {
		return IntegrityReport{}, fmt.Errorf("integrity: stat source: %w", err)
	}

	points := integrityProbePoints(durationMS)
	var (
		failed   []integrityPoint
		probeErr error
	)
	for _, p := range points {
		res, err := integrityProbeAt(ctx, path, p)
		if err != nil {
			// Remember the first tool-level failure but keep probing: a
			// damaged verdict from a later point still stands on its own.
			if probeErr == nil {
				probeErr = err
			}
			continue
		}
		if res.damaged {
			failed = append(failed, res)
		}
	}

	if len(failed) > 0 {
		detail := integrityDetail(failed)
		return IntegrityReport{Status: IntegrityDamaged, Detail: &detail}, nil
	}
	if probeErr != nil {
		return IntegrityReport{}, probeErr
	}
	return IntegrityReport{Status: IntegrityOK}, nil
}

// integrityProbePoints places the spot-decode offsets for a file of the
// given duration. Files too short for distinct offsets (or with unknown
// duration) get a single head probe — on a 10 s clip the three fractions
// would land inside the same GOP anyway.
func integrityProbePoints(durationMS int64) []integrityPoint {
	durSec := durationMS / 1000
	if durSec < 3*integrityWindowSec {
		return []integrityPoint{{offsetSec: 0, pct: 0, minFrames: 1}}
	}
	pts := make([]integrityPoint, 0, len(integrityOffsets))
	for _, f := range integrityOffsets {
		pts = append(pts, integrityPoint{
			offsetSec: int64(float64(durSec) * f),
			pct:       int(f * 100),
			minFrames: integrityFramesPerPoint,
		})
	}
	return pts
}

// integrityProbeAt runs one spot-decode. The returned error means the probe
// itself could not run or finish (tool missing, timeout, no video stream) —
// never "the file is damaged"; that's the damaged flag on the result.
func integrityProbeAt(ctx context.Context, path string, p integrityPoint) (integrityPoint, error) {
	res := p
	offsetSec := p.offsetSec

	ctx, cancel := context.WithTimeout(ctx, integrityPointTimeout)
	defer cancel()

	// -ss before -i: demuxer-level seek to the nearest advertised keyframe at
	// or before the offset — exactly what a player does on a mid-stream jump,
	// so a keyframe the index lies about fails here the way it fails there.
	// -t (input option) bounds the demux window so a point where nothing
	// decodes can't read to EOF. signalstats+metadata=print emits one
	// guaranteed stats block per DECODED frame on stdout (same channel
	// chromacheck parses), which is how we count success; the software
	// decoder's complaints land on stderr, which is how we count failure.
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-ss", fmt.Sprintf("%d", offsetSec),
		"-t", fmt.Sprintf("%d", integrityWindowSec),
		// Confine the demuxer to this input's protocols (must precede -i).
		"-protocol_whitelist", ffsafe.Whitelist(path),
		"-i", path,
		"-map", "0:v:0", "-an", "-sn",
		"-frames:v", fmt.Sprintf("%d", integrityFramesPerPoint),
		"-vf", "signalstats,metadata=print:file=-",
		"-f", "null", "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	if ctx.Err() != nil {
		return res, fmt.Errorf("integrity: probe at %ds timed out", offsetSec)
	}

	res.frames = countAnalyzedFrames(stdout.String())
	var noStreams bool
	res.errLines, res.firstErr, noStreams = summarizeDecodeErrors(stderr.String())

	if noStreams {
		// No video stream to map — the caller's filter should have excluded
		// this file; not evidence about bitstream health.
		return res, fmt.Errorf("integrity: no video stream in %s", path)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			// ffmpeg never ran (not on PATH, exec failure) — tool problem.
			return res, fmt.Errorf("integrity: run ffmpeg: %w", runErr)
		}
		// ffmpeg ran and bailed (demux/decode gave up entirely). With the
		// source stat-checked and a video stream present, that is evidence
		// of damage, not a tool failure.
		res.damaged = true
		return res, nil
	}

	// ffmpeg exited 0 — the common shape for bitstream damage: it decodes
	// what it can (sometimes silently discarding garbage without a single
	// stderr line), and reports success regardless. Coming up short of the
	// requested frames, or an error spew, are both damage.
	res.damaged = res.frames < res.minFrames || res.errLines >= integrityErrorThreshold
	return res, nil
}

// countAnalyzedFrames counts decoded frames in signalstats metadata=print
// stdout. One YAVG line is emitted per analyzed frame; frames the decoder
// could not produce never reach the filter, so this is a decode-success
// count, not a demux count.
func countAnalyzedFrames(out string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "lavfi.signalstats.YAVG=") {
			n++
		}
	}
	return n
}

// summarizeDecodeErrors classifies ffmpeg stderr at -loglevel error: how
// many complaint lines, the first one (for the stored detail), and whether
// the failure is "stream map matches no streams" (a caller bug / audio-only
// file, not damage).
func summarizeDecodeErrors(errOut string) (count int, first string, noStreams bool) {
	for _, line := range strings.Split(errOut, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "matches no streams") {
			noStreams = true
			continue
		}
		count++
		if first == "" {
			first = line
		}
	}
	return count, first, noStreams
}

// integrityDetail renders failing points into the bounded human-readable
// summary stored in media_files.integrity_detail.
func integrityDetail(failed []integrityPoint) string {
	parts := make([]string, 0, len(failed))
	for _, p := range failed {
		s := fmt.Sprintf("at %d%% (t=%ds): %d/%d frames decoded, %d decoder errors",
			p.pct, p.offsetSec, p.frames, integrityFramesPerPoint, p.errLines)
		if p.firstErr != "" {
			s += " — " + p.firstErr
		}
		parts = append(parts, s)
	}
	detail := "spot-decode failed " + strings.Join(parts, "; ")
	if len(detail) > integrityDetailMax {
		cut := integrityDetailMax - len("…")
		for cut > 0 && !utf8.RuneStart(detail[cut]) {
			cut-- // don't slice mid-rune (decoder messages can carry UTF-8)
		}
		detail = detail[:cut] + "…"
	}
	return detail
}
