package trickplay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/ffsafe"
)

// ── Chapter thumbnails ────────────────────────────────────────────────────────
//
// One JPEG per chapter of the file the sprites were cut from, written next to
// the sprites as chapter_000.jpg, chapter_001.jpg, … (index = the chapter's
// position in the file's chapter list) and served by the same handler, with
// the same auth, as the sprites.
//
// Why a separate pass and not a crop of the nearest sprite tile: a sprite
// frame is up to IntervalSec/2 (5 s) away from the chapter's point, which is
// often the wrong side of a scene cut, and tiles are letterboxed into a fixed
// 320×180 box. One input-seeked single-frame decode per chapter (-ss before
// -i lands on the nearest keyframe and decodes a frame or two) is cheap —
// tens of milliseconds to well under a second per chapter, against minutes
// for the sprite pass — and gives the exact moment at the source's aspect.
//
// The frame is taken at the chapter start plus min(5 s, 10% of the chapter's
// length): past the fade-in or black frame chapters usually open on, but
// still inside a short chapter.

// Chapter is one chapter marker of a media file, in milliseconds.
type Chapter struct {
	StartMS int64
	EndMS   int64
}

// MaxChapterThumbs bounds the thumbnails for one file; past it the remaining
// chapters get none (a file with that many chapters is an outlier).
const MaxChapterThumbs = 500

// ChapterThumbWidth is the thumbnails' width; height follows the aspect.
const ChapterThumbWidth = 320

// ChapterThumbName is the file name of chapter i's thumbnail.
func ChapterThumbName(i int) string { return fmt.Sprintf("chapter_%03d.jpg", i) }

// ChapterThumbTimeSec is where in the file chapter c's thumbnail is taken.
func ChapterThumbTimeSec(c Chapter) float64 {
	offset := int64(5000)
	if length := c.EndMS - c.StartMS; length > 0 {
		offset = min(offset, length/10)
	} else {
		offset = 0
	}
	return float64(c.StartMS+offset) / 1000
}

// ParseChapters reads a media file's chapters JSONB ([{start_ms, end_ms,
// title}], as the scanner stores it).
func ParseChapters(data []byte) []Chapter {
	if len(data) == 0 {
		return nil
	}
	var raw []struct {
		StartMS float64 `json:"start_ms"`
		EndMS   float64 `json:"end_ms"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	out := make([]Chapter, 0, len(raw))
	for _, c := range raw {
		out = append(out, Chapter{StartMS: int64(c.StartMS), EndMS: int64(c.EndMS)})
	}
	return out
}

// ChapterLookup is the optional MediaLookup extension for chapter
// thumbnails: a file's path and chapters, by file id.
type ChapterLookup interface {
	FileChapters(ctx context.Context, fileID uuid.UUID) (path string, chapters []Chapter, err error)
}

// ChapterThumbStore is the optional Store extension recording how many
// chapter thumbnails an item has (trickplay_status.chapter_thumb_count).
type ChapterThumbStore interface {
	SetChapterThumbs(ctx context.Context, itemID uuid.UUID, count int) error
}

// GenerateChapterThumbs (re)writes the chapter thumbnails of fileID — the
// file the item's sprites come from — into the item's trickplay directory
// and records how many there are. Zero chapters records 0. A failure records
// 0 too (no thumb_url is advertised for half a set) and is returned.
func (g *Generator) GenerateChapterThumbs(ctx context.Context, itemID, fileID uuid.UUID) (int, error) {
	cl, ok := g.lookup.(ChapterLookup)
	if !ok || fileID == uuid.Nil {
		return 0, nil
	}
	path, chapters, err := cl.FileChapters(ctx, fileID)
	if err != nil {
		return 0, fmt.Errorf("chapter lookup: %w", err)
	}
	outDir := g.ItemDir(itemID)
	n, genErr := generateChapterThumbs(ctx, path, chapters, outDir)
	if genErr != nil {
		removeChapterThumbs(outDir)
		n = 0
	}
	if cs, ok := g.store.(ChapterThumbStore); ok {
		if err := cs.SetChapterThumbs(ctx, itemID, n); err != nil {
			return n, fmt.Errorf("record chapter thumbs: %w", err)
		}
	}
	return n, genErr
}

// generateChapterThumbs writes one thumbnail per chapter (up to
// MaxChapterThumbs) and returns how many it wrote.
func generateChapterThumbs(ctx context.Context, inputPath string, chapters []Chapter, outDir string) (int, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, fmt.Errorf("chapter thumbs mkdir: %w", err)
	}
	removeChapterThumbs(outDir)
	if len(chapters) > MaxChapterThumbs {
		chapters = chapters[:MaxChapterThumbs]
	}
	for i, c := range chapters {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		out := filepath.Join(outDir, ChapterThumbName(i))
		at := ChapterThumbTimeSec(c)
		err := extractFrame(ctx, inputPath, at, out)
		if err != nil && at > float64(c.StartMS)/1000 {
			// The offset point decoded nothing (a chapter list longer than
			// the file, a truncated rip): the chapter's first frame will do.
			err = extractFrame(ctx, inputPath, float64(c.StartMS)/1000, out)
		}
		if err != nil {
			return 0, fmt.Errorf("chapter %d: %w", i+1, err)
		}
	}
	return len(chapters), nil
}

// extractFrame writes the frame at atSec as a JPEG.
func extractFrame(ctx context.Context, inputPath string, atSec float64, out string) error {
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-ss", strconv.FormatFloat(atSec, 'f', 3, 64)}
	// Confine the demuxer to this input's protocols (must precede -i).
	args = append(args, ffsafe.InputProtocolArgs(inputPath)...)
	args = append(args,
		"-i", inputPath,
		"-frames:v", "1",
		"-an", "-sn", "-dn",
		// Full-range for the mjpeg encoder (see spriteFilter).
		"-vf", fmt.Sprintf("scale=%d:-2,format=yuvj420p", ChapterThumbWidth),
		"-qscale:v", "4",
		out,
	)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		// Seeking past the end exits 0 having encoded nothing.
		_ = os.Remove(out)
		return errors.New("no frame at that position")
	}
	return nil
}

func removeChapterThumbs(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, "chapter_*.jpg"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// FileChapters satisfies ChapterLookup for the media-service adapter.
func (m mediaServiceLookup) FileChapters(ctx context.Context, fileID uuid.UUID) (string, []Chapter, error) {
	f, err := m.svc.GetFile(ctx, fileID)
	if err != nil {
		return "", nil, err
	}
	return f.FilePath, ParseChapters(f.Chapters), nil
}
