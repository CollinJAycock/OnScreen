package v1

import (
	"context"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
)

// SubtitleStreamOffsetReader reads the admin timing offsets of embedded
// subtitle streams (subtitle_stream_offsets, migration 00055). Satisfied by
// *gen.Queries.
type SubtitleStreamOffsetReader interface {
	GetSubtitleStreamOffset(ctx context.Context, arg gen.GetSubtitleStreamOffsetParams) (int32, error)
	ListSubtitleStreamOffsets(ctx context.Context, fileIDs []uuid.UUID) ([]gen.ListSubtitleStreamOffsetsRow, error)
}

// WithSubtitleOffsets makes ServeSubtitle shift embedded-stream cues by the
// stream's server offset and adds offset_ms to item details'
// subtitle_streams. Without it every offset reads as 0.
func (h *ItemHandler) WithSubtitleOffsets(r SubtitleStreamOffsetReader) *ItemHandler {
	h.subOffsets = r
	return h
}

// subtitleStreamOffset is one embedded stream's offset in milliseconds, 0
// when none is set or the lookup fails (served unshifted, logged: a broken
// offset lookup must not take subtitles down with it).
func (h *ItemHandler) subtitleStreamOffset(ctx context.Context, fileID uuid.UUID, streamIdx int) int {
	if h.subOffsets == nil {
		return 0
	}
	off, err := h.subOffsets.GetSubtitleStreamOffset(ctx, gen.GetSubtitleStreamOffsetParams{
		FileID: fileID, StreamIndex: int32(streamIdx),
	})
	if err != nil {
		h.logger.WarnContext(ctx, "subtitle stream offset", "file_id", fileID, "stream", streamIdx, "err", err)
		return 0
	}
	return int(off)
}

// applySubtitleStreamOffsets fills OffsetMS on the files' embedded subtitle
// streams with one query for all of them.
func (h *ItemHandler) applySubtitleStreamOffsets(ctx context.Context, files []ItemFileResponse) {
	if h.subOffsets == nil || len(files) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(files))
	for _, f := range files {
		if id, err := uuid.Parse(f.ID); err == nil && len(f.SubtitleStreams) > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := h.subOffsets.ListSubtitleStreamOffsets(ctx, ids)
	if err != nil {
		h.logger.WarnContext(ctx, "list subtitle stream offsets", "err", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	type key struct {
		file string
		idx  int
	}
	byKey := make(map[key]int32, len(rows))
	for _, row := range rows {
		byKey[key{row.FileID.String(), int(row.StreamIndex)}] = row.OffsetMs
	}
	for i := range files {
		for j := range files[i].SubtitleStreams {
			s := &files[i].SubtitleStreams[j]
			s.OffsetMS = byKey[key{files[i].ID, s.Index}]
		}
	}
}
