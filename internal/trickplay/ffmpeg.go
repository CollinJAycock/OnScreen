package trickplay

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// generateSprites extracts thumbnails from inputPath every spec.IntervalSec
// seconds, scales them into spec.ThumbWidth × spec.ThumbHeight boxes
// (letterboxed to preserve aspect), and packs them into sprite sheets of
// spec.GridCols × spec.GridRows using ffmpeg's tile filter. Each sheet is
// written as sprite_%d.jpg in outDir. Returns the sorted list of produced
// sprite filenames (basenames, not full paths).
//
// ffmpeg is shelled out with a single command; we trust its tile filter to
// handle tail padding for the last partial sheet.
func generateSprites(ctx context.Context, inputPath, outDir string, spec Spec) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("trickplay mkdir %s: %w", outDir, err)
	}

	// fps=1/N samples one frame every N seconds. scale+pad letterboxes each
	// thumb into a fixed WxH box so the VTT #xywh coordinates line up
	// regardless of the source video's aspect ratio. tile packs them into
	// a CxR grid per output file.
	vf := fmt.Sprintf(
		"fps=1/%d,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,tile=%dx%d",
		spec.IntervalSec,
		spec.ThumbWidth, spec.ThumbHeight,
		spec.ThumbWidth, spec.ThumbHeight,
		spec.GridCols, spec.GridRows,
	)

	// %03d in the output template: ffmpeg emits sprite_000.jpg,
	// sprite_001.jpg, … which sort lexicographically.
	outPattern := filepath.Join(outDir, "sprite_%03d.jpg")

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-y",
		"-i", inputPath,
		"-an", "-sn",
		"-vf", vf,
		"-qscale:v", "5",
		outPattern,
	)
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("trickplay ffmpeg %s: %w (stderr: %s)", inputPath, err, strings.TrimSpace(stderr.String()))
	}

	entries, err := os.ReadDir(outDir)
	if err != nil {
		return nil, fmt.Errorf("trickplay readdir %s: %w", outDir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, "sprite_") && strings.HasSuffix(n, ".jpg") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("trickplay ffmpeg produced no sprites for %s", inputPath)
	}
	return names, nil
}
