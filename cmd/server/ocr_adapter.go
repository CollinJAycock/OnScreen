package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// ocrExistsChecker implements scheduler.ExistingOCRChecker on top of the
// generated query layer. The scheduler asks per (file, stream) whether an
// ocr-source row already exists so SkipExisting sweeps don't re-OCR.
type ocrExistsChecker struct {
	q *gen.Queries
}

func (c *ocrExistsChecker) HasOCR(ctx context.Context, fileID uuid.UUID, streamIndex int) (bool, error) {
	srcID := fmt.Sprintf("stream_%d", streamIndex)
	srcIDPtr := &srcID
	return c.q.HasOCRForStream(ctx, gen.HasOCRForStreamParams{
		FileID:   fileID,
		SourceID: srcIDPtr,
	})
}
