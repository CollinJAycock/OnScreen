package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/faces"
)

// FacePhoto is one photo awaiting face detection.
type FacePhoto struct {
	ItemID, LibraryID uuid.UUID
}

// FaceStore is the work queue and result writer of the face-detection task.
type FaceStore interface {
	ListPhotosForFaceScan(ctx context.Context, libraryID *uuid.UUID, limit int32) ([]FacePhoto, error)
	CountPhotosForFaceScan(ctx context.Context, libraryID *uuid.UUID) (int64, error)
	// SaveDetectedFaces replaces the photo's unnamed automatic faces with
	// boxes and records the photo as scanned.
	SaveDetectedFaces(ctx context.Context, p FacePhoto, boxes []faces.Box) error
	// RecordFaceScanFailure records a photo that couldn't be decoded, so
	// the queue moves past it.
	RecordFaceScanFailure(ctx context.Context, p FacePhoto) error
}

// FaceDetect finds the faces on one photo item (its display file, upright,
// downscaled). Wired in main to photoimage + faces; a func type so the
// scheduler doesn't import the image pipeline.
type FaceDetect func(ctx context.Context, itemID uuid.UUID) ([]faces.Box, error)

// FaceDetectionConfig is the optional JSON payload of the face_detection
// task.
type FaceDetectionConfig struct {
	// Limit caps the photos one run looks at. Default 500.
	Limit int32 `json:"limit"`
	// BudgetMinutes stops a run after this long even with photos left.
	// Default 30.
	BudgetMinutes int `json:"budget_minutes"`
	// LibraryID optionally scopes the run to one library (UUID string).
	LibraryID string `json:"library_id"`
}

// FaceDetectionHandler runs the opt-in face detection sweep over photo
// libraries: each photo not yet looked at is decoded (downscaled), searched
// for faces, and its face boxes stored for people to be named on. Seeded
// DISABLED (see cmd/server seedSystemTasks): face data is personal, so
// collecting it is the operator's decision, and the decode costs CPU. Each
// run drains up to Limit photos within the time budget and continues the
// next night.
type FaceDetectionHandler struct {
	store  FaceStore
	detect FaceDetect
	logger *slog.Logger
	now    func() time.Time
}

// NewFaceDetectionHandler constructs the handler.
func NewFaceDetectionHandler(store FaceStore, detect FaceDetect, logger *slog.Logger) *FaceDetectionHandler {
	return &FaceDetectionHandler{store: store, detect: detect, logger: logger, now: time.Now}
}

// Run is the scheduler entry point.
func (h *FaceDetectionHandler) Run(ctx context.Context, rawCfg json.RawMessage) (string, error) {
	cfg := FaceDetectionConfig{Limit: 500, BudgetMinutes: 30}
	if len(rawCfg) > 0 {
		if err := json.Unmarshal(rawCfg, &cfg); err != nil {
			return "", fmt.Errorf("parse config: %w", err)
		}
	}
	if cfg.Limit <= 0 {
		cfg.Limit = 500
	}
	if cfg.Limit > 5000 {
		cfg.Limit = 5000
	}
	if cfg.BudgetMinutes <= 0 {
		cfg.BudgetMinutes = 30
	}
	if cfg.BudgetMinutes > 240 {
		cfg.BudgetMinutes = 240
	}
	var libraryID *uuid.UUID
	if cfg.LibraryID != "" {
		id, err := uuid.Parse(cfg.LibraryID)
		if err != nil {
			return "", fmt.Errorf("parse library_id: %w", err)
		}
		libraryID = &id
	}

	photos, err := h.store.ListPhotosForFaceScan(ctx, libraryID, cfg.Limit)
	if err != nil {
		return "", fmt.Errorf("list photos for face detection: %w", err)
	}
	if len(photos) == 0 {
		return "no photos awaiting face detection", nil
	}

	deadline := h.now().Add(time.Duration(cfg.BudgetMinutes) * time.Minute)
	var scanned, found, failed int
	for _, p := range photos {
		if ctx.Err() != nil || h.now().After(deadline) {
			break
		}
		boxes, err := h.detect(ctx, p.ItemID)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			h.logger.WarnContext(ctx, "face_detection: photo failed", "item_id", p.ItemID, "err", err)
			if rerr := h.store.RecordFaceScanFailure(ctx, p); rerr != nil {
				h.logger.WarnContext(ctx, "face_detection: record failure", "item_id", p.ItemID, "err", rerr)
			}
			failed++
			continue
		}
		if err := h.store.SaveDetectedFaces(ctx, p, boxes); err != nil {
			h.logger.WarnContext(ctx, "face_detection: save faces", "item_id", p.ItemID, "err", err)
			failed++
			continue
		}
		scanned++
		found += len(boxes)
	}

	remaining, err := h.store.CountPhotosForFaceScan(ctx, libraryID)
	if err != nil {
		remaining = -1
	}
	return fmt.Sprintf("photos=%d faces=%d failed=%d remaining=%d", scanned, found, failed, remaining), nil
}
