package main

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/faces"
	"github.com/onscreen/onscreen/internal/scheduler"
)

// photoFaceStore adapts the generated queries to scheduler.FaceStore.
type photoFaceStore struct{ q *gen.Queries }

func uuidArg(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

func (s photoFaceStore) ListPhotosForFaceScan(ctx context.Context, libraryID *uuid.UUID, limit int32) ([]scheduler.FacePhoto, error) {
	rows, err := s.q.ListPhotosForFaceScan(ctx, gen.ListPhotosForFaceScanParams{Limit: limit, LibraryID: uuidArg(libraryID)})
	if err != nil {
		return nil, err
	}
	out := make([]scheduler.FacePhoto, len(rows))
	for i, r := range rows {
		out[i] = scheduler.FacePhoto{ItemID: r.ID, LibraryID: r.LibraryID}
	}
	return out, nil
}

func (s photoFaceStore) CountPhotosForFaceScan(ctx context.Context, libraryID *uuid.UUID) (int64, error) {
	return s.q.CountPhotosForFaceScan(ctx, uuidArg(libraryID))
}

// SaveDetectedFaces replaces the photo's unnamed automatic faces. A box
// landing on a face that was already named or drawn by hand is skipped, so
// a re-detection never doubles a named face.
func (s photoFaceStore) SaveDetectedFaces(ctx context.Context, p scheduler.FacePhoto, boxes []faces.Box) error {
	if err := s.q.DeleteUnnamedAutoPhotoFaces(ctx, p.ItemID); err != nil {
		return err
	}
	kept, err := s.q.ListPhotoFacesForItem(ctx, p.ItemID)
	if err != nil {
		return err
	}
	saved := 0
	for _, b := range boxes {
		dup := false
		for _, k := range kept {
			if faces.IoU(b, faces.Box{X: float64(k.X), Y: float64(k.Y), W: float64(k.W), H: float64(k.H)}) > 0.4 {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		if _, err := s.q.CreatePhotoFace(ctx, gen.CreatePhotoFaceParams{
			ItemID: p.ItemID, LibraryID: p.LibraryID,
			X: float32(b.X), Y: float32(b.Y), W: float32(b.W), H: float32(b.H),
			Score: float32(b.Score), Source: "auto",
		}); err != nil {
			return err
		}
		saved++
	}
	return s.q.RecordPhotoFaceScan(ctx, gen.RecordPhotoFaceScanParams{
		ItemID: p.ItemID, LibraryID: p.LibraryID, Faces: int32(saved + len(kept)), Status: "ok",
	})
}

func (s photoFaceStore) RecordFaceScanFailure(ctx context.Context, p scheduler.FacePhoto) error {
	return s.q.RecordPhotoFaceScan(ctx, gen.RecordPhotoFaceScanParams{
		ItemID: p.ItemID, LibraryID: p.LibraryID, Faces: 0, Status: "failed",
	})
}
