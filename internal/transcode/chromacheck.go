package transcode

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Dead-chroma ("green frame") detection.
//
// The full-VRAM hardware pipelines (cuvid→scale_cuda→NVENC and friends) have a
// failure mode the exit code cannot see: on some GPU/driver/ffmpeg combos the
// chain runs to completion but the surfaces handed to the encoder carry no
// chroma, so every frame encodes as solid green (Y has structure, U and V are
// literally zero). ffmpeg exits 0, segments appear on schedule, and the first
// place the corruption becomes visible is the viewer's screen — observed on a
// Turing RTX 5000 whose probe had "passed", turning every SDR HEVC re-encode
// into a green movie.
//
// Real video can never look like this: neutral/grayscale chroma sits at 128
// (the unsigned midpoint), so even a pitch-black or pure-white frame averages
// U≈V≈128. An all-zero chroma plane on every sampled frame is unambiguous
// garbage, which makes it a safe kill signal with essentially no
// false-positive risk.

// chromaDeadCeiling is the per-frame UAVG/VAVG value below which chroma is
// considered absent. Real content sits near 128; a small allowance covers
// encoder dither around a genuinely zeroed plane.
const chromaDeadCeiling = 2.0

// chromaCheckTimeout bounds the analysis run. Decoding a handful of frames
// from a single local segment takes well under a second; the timeout only
// guards against a wedged ffmpeg.
const chromaCheckTimeout = 15 * time.Second

// SegmentChromaDead decodes the first few frames of an HLS segment and reports
// whether their chroma planes are dead (see the package comment above). For
// fMP4 segments pass initPath (the session's init.mp4) so the fragment is
// decodable; MPEG-TS segments are self-contained and take initPath "".
//
// The error return is for the CALLER to fail open on: an analysis failure
// (missing tool, undecodable file, no video frames) says nothing about the
// stream, so a live session should keep playing rather than be killed on a
// diagnostic hiccup.
func SegmentChromaDead(ctx context.Context, segPath, initPath string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, chromaCheckTimeout)
	defer cancel()
	input := segPath
	if initPath != "" {
		// An fMP4 fragment has no decoder config of its own; byte-concatenate
		// the init segment in front so ffmpeg sees a complete fragmented MP4.
		// (A plain temp file, not the concat: protocol — that mis-parses
		// Windows drive-letter colons.)
		joined, err := concatToTemp(initPath, segPath)
		if err != nil {
			return false, fmt.Errorf("chroma check concat: %w", err)
		}
		defer os.Remove(joined)
		input = joined
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-i", input,
		"-frames:v", "5",
		"-vf", "signalstats,metadata=print:file=-",
		"-f", "null", "-")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("chroma check ffmpeg: %w", err)
	}
	frames, dead := parseSignalstatsChroma(string(out))
	if frames == 0 {
		return false, fmt.Errorf("chroma check: no video frames analyzed")
	}
	return dead, nil
}

// concatToTemp writes the given files back-to-back into a temp file and
// returns its path; the caller removes it.
func concatToTemp(paths ...string) (string, error) {
	tmp, err := os.CreateTemp("", "chromacheck-*.mp4")
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			os.Remove(tmp.Name())
			return "", err
		}
		_, err = io.Copy(tmp, f)
		f.Close()
		if err != nil {
			os.Remove(tmp.Name())
			return "", err
		}
	}
	return tmp.Name(), nil
}

// parseSignalstatsChroma scans metadata=print output for the signalstats
// per-frame chroma averages. It returns how many frames reported chroma stats
// and whether EVERY one of them was dead (both UAVG and VAVG below the
// ceiling). A single live frame clears the stream — partial corruption is not
// this detector's job.
func parseSignalstatsChroma(out string) (frames int, dead bool) {
	var uVals, vVals []float64
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "lavfi.signalstats.UAVG="); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				uVals = append(uVals, f)
			}
		} else if v, ok := strings.CutPrefix(line, "lavfi.signalstats.VAVG="); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				vVals = append(vVals, f)
			}
		}
	}
	frames = len(uVals)
	if len(vVals) < frames {
		frames = len(vVals)
	}
	if frames == 0 {
		return 0, false
	}
	for i := 0; i < frames; i++ {
		if uVals[i] >= chromaDeadCeiling || vVals[i] >= chromaDeadCeiling {
			return frames, false
		}
	}
	return frames, true
}

// outputChromaAlive is the probe-side twin of SegmentChromaDead: it decodes a
// just-encoded probe artifact and reports whether its chroma survived the
// pipeline under test. Unlike the session check it fails CLOSED — the probe is
// deciding whether to ENABLE a fragile optimization, so "couldn't verify"
// means "don't enable".
func outputChromaAlive(ctx context.Context, path string) bool {
	dead, err := SegmentChromaDead(ctx, path, "")
	return err == nil && !dead
}
