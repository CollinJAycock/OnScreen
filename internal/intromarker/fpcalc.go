// Package intromarker detects intro and credits ranges in episodic media.
// Intros are located by audio fingerprint alignment across episodes in a
// season. Credits are located by black-frame detection at the end of a
// single file. The detector shells out to fpcalc (chromaprint) and ffmpeg.
package intromarker

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/onscreen/onscreen/internal/ffsafe"
)

// fingerprintSecondsPerFrame is the chromaprint default: one fingerprint
// integer covers ~124ms of audio. Used to convert frame indices back to ms.
const fingerprintSecondsPerFrame = 0.1238

// fingerprint runs `fpcalc -raw` on filePath starting at startSec and
// covering durationSec seconds. Returns the raw 32-bit fingerprint frames.
//
// Requires fpcalc (chromaprint-tools) to be on PATH. Errors include "binary
// not found", "file unreadable", or parse failures — caller should log and
// skip the episode rather than fail the whole run.
func fingerprint(ctx context.Context, filePath string, startSec, durationSec int) ([]uint32, error) {
	// Always decode through ffmpeg first and hand fpcalc only the temp WAV.
	// Running fpcalc directly on the library file let fpcalc's own bundled
	// libavformat demux it with no protocol allowlist — the same playlist-
	// container exposure every ffmpeg call site now closes via ffsafe. (It was
	// also the less reliable path: fpcalc lacks codecs such as AC3/DTS on some
	// builds and already fell back to this decode.)
	return fingerprintWithFFmpegSeek(ctx, filePath, startSec, durationSec)
}

// fingerprintWithFFmpegSeek decodes filePath via ffmpeg into a temporary
// wav file, then fingerprints the temp file with fpcalc. The temp file
// hop is slower than piping, but fpcalc on Windows doesn't reliably
// parse WAV from stdin and many DVDRip codecs aren't bundled with the
// standalone fpcalc build.
func fingerprintWithFFmpegSeek(ctx context.Context, filePath string, startSec, durationSec int) ([]uint32, error) {
	tmp, err := os.CreateTemp("", "onscreen-fp-*.wav")
	if err != nil {
		return nil, fmt.Errorf("tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-ss", strconv.Itoa(startSec),
		"-t", strconv.Itoa(durationSec),
		// Confine the demuxer to this input's protocols (must precede -i).
		"-protocol_whitelist", ffsafe.Whitelist(filePath),
		"-i", filePath,
		"-vn", "-ac", "1", "-ar", "11025", "-f", "wav",
		tmpPath,
	}
	if out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode %s: %w: %s", filePath, err, strings.TrimSpace(string(out)))
	}

	out, err := exec.CommandContext(ctx, "fpcalc", "-raw", "-length", strconv.Itoa(durationSec), tmpPath).Output()
	if err != nil {
		return nil, fmt.Errorf("fpcalc %s: %w", filePath, err)
	}
	return parseFpcalcOutput(string(out))
}

// parseFpcalcOutput extracts the FINGERPRINT=... line from fpcalc's key=value
// output and decodes it into a slice of uint32s. Tolerates either line order
// and trims leading/trailing whitespace.
func parseFpcalcOutput(s string) ([]uint32, error) {
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "FINGERPRINT=") {
			continue
		}
		raw := strings.TrimPrefix(line, "FINGERPRINT=")
		parts := strings.Split(raw, ",")
		out := make([]uint32, 0, len(parts))
		for _, p := range parts {
			n, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("parse fingerprint int %q: %w", p, err)
			}
			out = append(out, uint32(n))
		}
		return out, nil
	}
	return nil, fmt.Errorf("no FINGERPRINT line in fpcalc output")
}

// framesToMs converts a frame index to milliseconds.
func framesToMs(frames int) int64 {
	return int64(float64(frames) * fingerprintSecondsPerFrame * 1000)
}
