package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/subtitles"
	"github.com/onscreen/onscreen/internal/subtitles/ocr"
)

// OCRConfig is the JSON payload for the ocr_subtitles task.
//
// LibraryID restricts the sweep to one library; "all" / empty walks every
// library on the server. SkipExisting (default true) skips streams whose
// OCR row already exists, so a nightly cron only processes newly added
// files. Set false to force re-OCR (useful after upgrading tesseract or
// changing the canvas size).
type OCRConfig struct {
	LibraryID    string `json:"library_id"`
	SkipExisting bool   `json:"skip_existing"`
}

// OCRMediaSource is the slice of media.Service the handler depends on:
// enumerate libraries' active files.
type OCRMediaSource interface {
	ListActiveFilesForLibrary(ctx context.Context, libraryID uuid.UUID) ([]media.File, error)
}

// OCRSubtitleService is the slice of subtitles.Service the handler calls.
// Defined here so tests can swap in a fake without spinning up the real
// engine.
type OCRSubtitleService interface {
	OCRStream(ctx context.Context, opts subtitles.OCROpts) (gen.ExternalSubtitle, error)
}

// ExistingOCRChecker reports whether a (file_id, source="ocr",
// source_id="stream_N") row already exists. Used by the SkipExisting
// branch so a nightly cron sweep doesn't re-OCR everything.
type ExistingOCRChecker interface {
	HasOCR(ctx context.Context, fileID uuid.UUID, streamIndex int) (bool, error)
}

// OCRHandler walks libraries and OCRs every image-based subtitle stream
// it finds. It is intentionally synchronous — the scheduler dispatches
// each task on its own goroutine, and a multi-hour OCR sweep is fine to
// hold one slot.
type OCRHandler struct {
	media   OCRMediaSource
	subs    OCRSubtitleService
	lister  LibraryLister
	checker ExistingOCRChecker
}

// NewOCRHandler builds an OCRHandler. lister supplies library IDs for the
// "all" variant. checker may be nil (every stream is treated as new).
func NewOCRHandler(m OCRMediaSource, subs OCRSubtitleService, lister LibraryLister, checker ExistingOCRChecker) *OCRHandler {
	return &OCRHandler{media: m, subs: subs, lister: lister, checker: checker}
}

// Run walks one or all libraries and OCRs every image-based subtitle
// stream it finds. Per-stream errors are logged into the output string
// and don't abort the sweep — one bad container shouldn't kill a 5000-
// file run.
func (h *OCRHandler) Run(ctx context.Context, rawCfg json.RawMessage) (string, error) {
	cfg := OCRConfig{SkipExisting: true}
	if len(rawCfg) > 0 {
		if err := json.Unmarshal(rawCfg, &cfg); err != nil {
			return "", fmt.Errorf("parse config: %w", err)
		}
	}

	libIDs, err := h.libraryIDs(ctx, cfg.LibraryID)
	if err != nil {
		return "", err
	}

	var processed, ocred, skipped, failed int
	for _, libID := range libIDs {
		if err := ctx.Err(); err != nil {
			return summary(processed, ocred, skipped, failed), err
		}
		files, err := h.media.ListActiveFilesForLibrary(ctx, libID)
		if err != nil {
			failed++
			continue
		}
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				return summary(processed, ocred, skipped, failed), err
			}
			processed++
			streams := imageBasedSubs(f.SubtitleStreams)
			for _, st := range streams {
				if cfg.SkipExisting && h.checker != nil {
					exists, err := h.checker.HasOCR(ctx, f.ID, st.Index)
					if err == nil && exists {
						skipped++
						continue
					}
				}
				_, err := h.subs.OCRStream(ctx, subtitles.OCROpts{
					FileID:         f.ID,
					InputPath:      f.FilePath,
					AbsStreamIndex: st.Index,
					Language:       st.Language,
					Title:          st.Title,
					Forced:         st.Forced,
				})
				if err != nil {
					failed++
					continue
				}
				ocred++
			}
		}
	}
	return summary(processed, ocred, skipped, failed), nil
}

func (h *OCRHandler) libraryIDs(ctx context.Context, raw string) ([]uuid.UUID, error) {
	if raw == "" || raw == "all" {
		ids, err := h.lister.ListLibraryIDs(ctx)
		if err != nil {
			return nil, fmt.Errorf("list libraries: %w", err)
		}
		return ids, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse library_id %q: %w", raw, err)
	}
	return []uuid.UUID{id}, nil
}

func summary(processed, ocred, skipped, failed int) string {
	return fmt.Sprintf("scanned %d files, OCR'd %d streams, skipped %d existing, %d errors",
		processed, ocred, skipped, failed)
}

// subtitleStream is the JSONB shape the scanner writes per subtitle track.
type subtitleStream struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Forced   bool   `json:"forced"`
}

// imageBasedSubs decodes the SubtitleStreams JSONB and returns only the
// bitmap-based entries (PGS, VOBSUB, DVB, XSUB) — text formats are direct-
// readable by the player and don't need OCR.
func imageBasedSubs(raw []byte) []subtitleStream {
	if len(raw) == 0 {
		return nil
	}
	var all []subtitleStream
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil
	}
	out := make([]subtitleStream, 0, len(all))
	for _, s := range all {
		if ocr.IsImageBased(s.Codec) {
			out = append(out, s)
		}
	}
	return out
}
