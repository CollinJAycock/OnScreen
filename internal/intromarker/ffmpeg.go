package intromarker

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// blackdetect runs ffmpeg's blackdetect filter over the tail of filePath and
// returns the earliest sustained black region's start offset (in ms from the
// start of the file). Returns 0 with a nil error when nothing qualifies —
// callers treat that as "no credits marker".
//
// tailSec controls how much of the file's end we scan; fileDurationSec is the
// file's total duration (ffprobe output or media_items.duration_ms). We seek
// to max(0, fileDurationSec-tailSec) so we never analyse the whole film.
func blackdetect(ctx context.Context, filePath string, fileDurationSec, tailSec int) (int64, error) {
	startSec := fileDurationSec - tailSec
	if startSec < 0 {
		startSec = 0
	}
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-nostdin", "-hide_banner", "-loglevel", "info",
		"-ss", strconv.Itoa(startSec),
		"-i", filePath,
		"-an",
		"-vf", "blackdetect=d=0.4:pic_th=0.98",
		"-f", "null", "-",
	)
	// blackdetect writes to stderr. Collect everything and scan after.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, fmt.Errorf("blackdetect stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("blackdetect start: %w", err)
	}

	var earliestRel float64 = -1
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		line := sc.Text()
		idx := strings.Index(line, "black_start:")
		if idx < 0 {
			continue
		}
		rest := line[idx+len("black_start:"):]
		// token is space-terminated, e.g. "black_start:1803.45 black_end:..."
		tok := rest
		if sp := strings.IndexAny(rest, " \t"); sp >= 0 {
			tok = rest[:sp]
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(tok), 64)
		if err != nil {
			continue
		}
		if earliestRel < 0 || v < earliestRel {
			earliestRel = v
		}
	}
	if err := cmd.Wait(); err != nil {
		return 0, fmt.Errorf("blackdetect run %s: %w", filePath, err)
	}
	if earliestRel < 0 {
		return 0, nil
	}
	return int64((float64(startSec) + earliestRel) * 1000), nil
}
