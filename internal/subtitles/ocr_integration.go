package subtitles

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/subtitles/ocr"
)

// OCREngine is the contract the service needs from the OCR pipeline. The
// real implementation is *ocr.Engine; tests substitute a fake.
type OCREngine interface {
	Run(ctx context.Context, inputPath string, absStreamIndex int, lang string, workDir string) ([]ocr.Cue, error)
}

// OCROpts identifies which subtitle stream of which file to OCR.
//
// Language is the ISO-639-1/2 code of the source subtitle (used to pick the
// tesseract trained-data pack and recorded on the resulting row). Title is
// optional — surfaces in the picker when set.
type OCROpts struct {
	FileID         uuid.UUID
	InputPath      string // absolute path to the media file
	AbsStreamIndex int    // ffmpeg stream index of the subtitle to OCR
	Language       string
	Title          string
	Forced         bool
	SDH            bool
}

// SetOCR wires an OCR engine onto the service. nil disables OCR — callers
// will get ErrNoOCR back from OCRStream.
func (s *Service) SetOCR(engine OCREngine) { s.ocr = engine }

// ErrNoOCR is returned from OCRStream when no engine is wired.
var ErrNoOCR = errors.New("ocr engine not configured")

// OCRStream extracts the requested subtitle stream, OCRs every cue, writes
// a sidecar WebVTT file under the cache dir, and inserts/updates the
// matching external_subtitles row (source="ocr", source_id="stream_{N}").
//
// The DB row's UNIQUE (file_id, source, source_id) constraint plus the
// INSERT … ON CONFLICT DO UPDATE in the underlying query mean re-running
// OCR for the same stream overwrites the existing row in place — no
// orphaned files, no duplicated picker entries.
func (s *Service) OCRStream(ctx context.Context, opts OCROpts) (gen.ExternalSubtitle, error) {
	if s.ocr == nil {
		return gen.ExternalSubtitle{}, ErrNoOCR
	}
	if opts.FileID == uuid.Nil || opts.InputPath == "" {
		return gen.ExternalSubtitle{}, errors.New("file_id and input_path are required")
	}
	if opts.Language == "" {
		opts.Language = "en"
	}

	dir := filepath.Join(s.cacheDir, opts.FileID.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return gen.ExternalSubtitle{}, fmt.Errorf("mkdir cache: %w", err)
	}
	// Per-stream workdir keeps frame_*.png files from one OCR job out of
	// another's way (e.g. concurrent OCR of two streams in the same file).
	workDir := filepath.Join(dir, fmt.Sprintf("ocr_work_stream%d", opts.AbsStreamIndex))
	defer os.RemoveAll(workDir)

	cues, err := s.ocr.Run(ctx, opts.InputPath, opts.AbsStreamIndex, opts.Language, workDir)
	if err != nil {
		return gen.ExternalSubtitle{}, fmt.Errorf("ocr run: %w", err)
	}
	if len(cues) == 0 {
		return gen.ExternalSubtitle{}, fmt.Errorf("ocr produced no cues for stream %d", opts.AbsStreamIndex)
	}

	vtt := ocr.CuesToVTT(cues)
	filename := fmt.Sprintf("ocr_stream%d_%s.vtt", opts.AbsStreamIndex, opts.Language)
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, vtt, 0o644); err != nil {
		return gen.ExternalSubtitle{}, fmt.Errorf("write vtt: %w", err)
	}

	srcID := "stream_" + strconv.Itoa(opts.AbsStreamIndex)
	titlePtr := nilIfEmpty(opts.Title)
	srcIDPtr := &srcID
	row, err := s.store.InsertExternalSubtitle(ctx, gen.InsertExternalSubtitleParams{
		FileID:      opts.FileID,
		Language:    opts.Language,
		Title:       titlePtr,
		Forced:      opts.Forced,
		Sdh:         opts.SDH,
		Source:      "ocr",
		SourceID:    srcIDPtr,
		StoragePath: path,
	})
	if err != nil {
		_ = os.Remove(path)
		return gen.ExternalSubtitle{}, fmt.Errorf("persist row: %w", err)
	}
	s.logger.InfoContext(ctx, "ocr complete",
		"file_id", opts.FileID, "stream", opts.AbsStreamIndex,
		"lang", opts.Language, "cues", len(cues), "path", path)
	return row, nil
}
