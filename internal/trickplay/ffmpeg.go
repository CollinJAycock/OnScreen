package trickplay

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onscreen/onscreen/internal/ffsafe"
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
//
// parts, when it names two or more files, is a stacked movie: the sprites are
// cut from all of them in order, through a concat list written to a private
// temp file (never into outDir, which is served).
func generateSprites(ctx context.Context, inputPath string, parts []ffsafe.ConcatPart, outDir string, spec Spec) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("trickplay mkdir %s: %w", outDir, err)
	}

	vf := spriteFilter(spec)

	// %03d in the output template: ffmpeg emits sprite_000.jpg,
	// sprite_001.jpg, … which sort lexicographically.
	outPattern := filepath.Join(outDir, "sprite_%03d.jpg")

	// Confine the demuxer to this input's protocols so a crafted source
	// can't reach the network (must precede -i). See internal/ffsafe.
	inArgs := []string{"-protocol_whitelist", ffsafe.Whitelist(inputPath)}
	if len(parts) > 1 {
		list, err := os.CreateTemp("", "onscreen-trickplay-*.ffconcat")
		if err != nil {
			return nil, fmt.Errorf("trickplay concat list: %w", err)
		}
		listPath := list.Name()
		_ = list.Close()
		defer func() { _ = os.Remove(listPath) }()
		if err := ffsafe.WriteConcatList(listPath, parts); err != nil {
			return nil, fmt.Errorf("trickplay concat list: %w", err)
		}
		inputPath = listPath
		inArgs = ffsafe.ConcatInputArgs(parts)
	}

	args := append([]string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-y",
	}, inArgs...)
	args = append(args,
		"-i", inputPath,
		"-an", "-sn",
		"-vf", vf,
		"-qscale:v", "5",
		outPattern,
	)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
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

// spriteFilter is the -vf graph for one sprite run. fps=1/N samples one frame
// every N seconds. scale+pad letterboxes each thumb into a fixed WxH box so
// the VTT #xywh coordinates line up regardless of the source video's aspect
// ratio. tile packs them into a CxR grid per output file. format=yuvj420p
// converts to full range: ffmpeg 8's mjpeg encoder refuses the limited-range
// YUV nearly all video uses ("Non full-range YUV is non-standard") unless
// -strict unofficial, which writes JPEGs browsers render washed out.
func spriteFilter(spec Spec) string {
	return fmt.Sprintf(
		"fps=1/%d,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,tile=%dx%d,format=yuvj420p",
		spec.IntervalSec,
		spec.ThumbWidth, spec.ThumbHeight,
		spec.ThumbWidth, spec.ThumbHeight,
		spec.GridCols, spec.GridRows,
	)
}
