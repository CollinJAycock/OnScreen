package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// IntegrityFileStore is the media-service surface the integrity probe task
// needs: the work queue of unchecked video files plus the verdict writer.
// Kept local so the scheduler doesn't drag in db/gen.
type IntegrityFileStore interface {
	ListFilesForIntegrityCheck(ctx context.Context, libraryID *uuid.UUID, limit int32) ([]media.File, error)
	CountFilesForIntegrityCheck(ctx context.Context, libraryID *uuid.UUID) (int64, error)
	SetFileIntegrity(ctx context.Context, id uuid.UUID, status string, detail *string) error
}

// IntegrityProbe spot-decodes one file and returns the verdict
// ("ok" | "damaged") plus the failure detail for damaged files. An error
// means the probe couldn't run (tool missing, file unreachable, timeout) —
// the file must stay unchecked, never be branded damaged. Wired to
// scanner.CheckIntegrity in main; a func type so the scheduler doesn't
// import the scanner package.
type IntegrityProbe func(ctx context.Context, path string, durationMS int64) (status string, detail *string, err error)

// IntegrityProbeConfig is the optional JSON payload for the
// integrity_probe task.
type IntegrityProbeConfig struct {
	// Limit caps how many files one run spot-decodes. Healthy local files
	// cost ~2-10 s each (three seeks + 5 software-decoded frames per
	// offset), so the default keeps a nightly run under ~an hour worst
	// case; crank it up via the Tasks UI for a first-pass sweep of an
	// existing library.
	Limit int32 `json:"limit"`
	// LibraryID optionally scopes the run to one library (UUID string).
	LibraryID string `json:"library_id"`
}

// IntegrityProbeHandler runs the opt-in deep-decode integrity sweep: it
// pulls active video files whose integrity_status is still 'unchecked',
// spot-decodes a few frames at several offsets with the software decoder,
// and records ok/damaged on the media_files row. Damaged files then get a
// clear "this file is damaged" playback verdict instead of users retrying
// an unplayable title until the player gives up (the QA fake release
// pattern: header probes pass, hardware decoders emit green frames,
// browsers stall — nothing ever marked the FILE).
//
// Seeded DISABLED (see cmd/server seedSystemTasks): spot-decoding costs
// real CPU+I/O per file, which is an operator's call on a large library.
// Each run drains up to Limit files and picks up where it left off, so
// enabling it eventually converges; new/changed files re-enter the queue
// as 'unchecked' via the scanner's technical-metadata reset.
type IntegrityProbeHandler struct {
	media  IntegrityFileStore
	probe  IntegrityProbe
	logger *slog.Logger
}

// NewIntegrityProbeHandler constructs the handler.
func NewIntegrityProbeHandler(media IntegrityFileStore, probe IntegrityProbe, logger *slog.Logger) *IntegrityProbeHandler {
	return &IntegrityProbeHandler{media: media, probe: probe, logger: logger}
}

// Run is the scheduler entry point.
func (h *IntegrityProbeHandler) Run(ctx context.Context, rawCfg json.RawMessage) (string, error) {
	cfg := IntegrityProbeConfig{Limit: 250}
	if len(rawCfg) > 0 {
		if err := json.Unmarshal(rawCfg, &cfg); err != nil {
			return "", fmt.Errorf("parse config: %w", err)
		}
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 250
	}
	if cfg.Limit > 1000 {
		cfg.Limit = 1000
	}
	var libraryID *uuid.UUID
	if cfg.LibraryID != "" {
		id, err := uuid.Parse(cfg.LibraryID)
		if err != nil {
			return "", fmt.Errorf("parse library_id: %w", err)
		}
		libraryID = &id
	}

	files, err := h.media.ListFilesForIntegrityCheck(ctx, libraryID, cfg.Limit)
	if err != nil {
		return "", fmt.Errorf("list files for integrity check: %w", err)
	}
	if len(files) == 0 {
		return "no files awaiting integrity check", nil
	}

	var ok, damaged, failed int
	for _, f := range files {
		// Server shutdown / task cancellation: stop cleanly; everything
		// not yet probed stays 'unchecked' for the next run.
		if ctx.Err() != nil {
			break
		}
		var durationMS int64
		if f.DurationMS != nil {
			durationMS = *f.DurationMS
		}
		status, detail, err := h.probe(ctx, f.FilePath, durationMS)
		if err != nil {
			// Probe couldn't run — leave the file unchecked (fail open;
			// a broken tool or unreachable share must not brand files).
			h.logger.WarnContext(ctx, "integrity_probe: probe failed",
				"file_id", f.ID, "path", f.FilePath, "err", err)
			failed++
			continue
		}
		if err := h.media.SetFileIntegrity(ctx, f.ID, status, detail); err != nil {
			h.logger.WarnContext(ctx, "integrity_probe: record verdict failed",
				"file_id", f.ID, "err", err)
			failed++
			continue
		}
		if status == media.IntegrityDamaged {
			d := ""
			if detail != nil {
				d = *detail
			}
			h.logger.InfoContext(ctx, "integrity_probe: file damaged",
				"file_id", f.ID, "path", f.FilePath, "detail", d)
			damaged++
			continue
		}
		ok++
	}

	remaining, err := h.media.CountFilesForIntegrityCheck(ctx, libraryID)
	if err != nil {
		// Progress count is best-effort garnish; the sweep itself succeeded.
		remaining = -1
	}
	return fmt.Sprintf("checked=%d ok=%d damaged=%d failed=%d remaining=%d",
		ok+damaged, ok, damaged, failed, remaining), nil
}
