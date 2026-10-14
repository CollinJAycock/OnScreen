package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/livetv"
)

// dvrPostProcessAdapter implements livetv.PostProcessStore over sqlc. It
// holds the pool (not just Queries) because the file swap and the marker
// replace are multi-statement transactions.
type dvrPostProcessAdapter struct {
	pool *pgxpool.Pool
	q    *gen.Queries
}

func newDVRPostProcessAdapter(pool *pgxpool.Pool) *dvrPostProcessAdapter {
	return &dvrPostProcessAdapter{pool: pool, q: gen.New(pool)}
}

var _ livetv.PostProcessStore = (*dvrPostProcessAdapter)(nil)

func (a *dvrPostProcessAdapter) GetRecording(ctx context.Context, id uuid.UUID) (livetv.Recording, error) {
	row, err := a.q.GetRecording(ctx, id)
	if err != nil {
		return livetv.Recording{}, err
	}
	return recordingFromGen(row), nil
}

func postProcessJobFromGen(r gen.DvrPostprocessJob) livetv.PostProcessJob {
	return livetv.PostProcessJob{
		RecordingID:      r.RecordingID,
		Kind:             livetv.PostProcessJobKind(r.Kind),
		Status:           livetv.PostProcessStatus(r.Status),
		OriginalFilePath: r.OriginalFilePath,
	}
}

func (a *dvrPostProcessAdapter) GetPostProcessJob(ctx context.Context, recordingID uuid.UUID) (livetv.PostProcessJob, bool, error) {
	row, err := a.q.GetDVRPostProcessJob(ctx, recordingID)
	if errors.Is(err, pgx.ErrNoRows) {
		return livetv.PostProcessJob{}, false, nil
	}
	if err != nil {
		return livetv.PostProcessJob{}, false, err
	}
	return postProcessJobFromGen(row), true, nil
}

func (a *dvrPostProcessAdapter) EnqueuePostProcess(ctx context.Context, recordingID uuid.UUID, kind livetv.PostProcessJobKind) (bool, error) {
	n, err := a.q.EnqueueDVRPostProcess(ctx, gen.EnqueueDVRPostProcessParams{
		RecordingID: recordingID, Kind: string(kind),
	})
	return n > 0, err
}

func (a *dvrPostProcessAdapter) ClaimNextPostProcess(ctx context.Context) (livetv.PostProcessJob, bool, error) {
	row, err := a.q.ClaimNextDVRPostProcess(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return livetv.PostProcessJob{}, false, nil
	}
	if err != nil {
		return livetv.PostProcessJob{}, false, err
	}
	return postProcessJobFromGen(row), true, nil
}

func (a *dvrPostProcessAdapter) SetPostProcessMessage(ctx context.Context, recordingID uuid.UUID, msg string) error {
	return a.q.SetDVRPostProcessMessage(ctx, gen.SetDVRPostProcessMessageParams{
		RecordingID: recordingID, Message: &msg,
	})
}

func (a *dvrPostProcessAdapter) FinishPostProcess(ctx context.Context, recordingID uuid.UUID, status livetv.PostProcessStatus, msg string) error {
	var m *string
	if msg != "" {
		m = &msg
	}
	return a.q.FinishDVRPostProcess(ctx, gen.FinishDVRPostProcessParams{
		RecordingID: recordingID, Status: string(status), Message: m,
	})
}

func (a *dvrPostProcessAdapter) FailInterruptedPostProcess(ctx context.Context) (int64, error) {
	return a.q.FailInterruptedDVRPostProcess(ctx)
}

func (a *dvrPostProcessAdapter) FindMediaFileID(ctx context.Context, path string) (uuid.UUID, error) {
	f, err := a.q.GetMediaFileByPath(ctx, path)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, err
	}
	return f.ID, nil
}

// SwapRecordingFile re-points the recording row, its media_files row and the
// kept-original path in one transaction, so the library never sees a
// half-swapped recording.
func (a *dvrPostProcessAdapter) SwapRecordingFile(ctx context.Context, p livetv.SwapFileParams) error {
	tx, err := a.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin swap tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)

	newPath := p.NewPath
	if err := qtx.SetRecordingFilePath(ctx, gen.SetRecordingFilePathParams{ID: p.RecordingID, FilePath: &newPath}); err != nil {
		return fmt.Errorf("recording path: %w", err)
	}
	if p.FileID != uuid.Nil {
		params := gen.RepointMediaFileParams{
			ID:       p.FileID,
			NewPath:  p.NewPath,
			FileSize: p.Size,
		}
		if p.Container != "" {
			params.Container = &p.Container
		}
		if p.VideoCodec != "" {
			params.VideoCodec = &p.VideoCodec
		}
		if p.AudioCodec != "" {
			params.AudioCodec = &p.AudioCodec
		}
		if p.DurationMS > 0 {
			params.DurationMs = &p.DurationMS
		}
		n, err := qtx.RepointMediaFile(ctx, params)
		if err != nil {
			return fmt.Errorf("media file path: %w", err)
		}
		if n == 0 {
			return errors.New("media file row disappeared")
		}
	}
	if err := qtx.SetDVRPostProcessOriginal(ctx, gen.SetDVRPostProcessOriginalParams{
		RecordingID: p.RecordingID, OriginalFilePath: p.OriginalPath,
	}); err != nil {
		return fmt.Errorf("original path: %w", err)
	}
	return tx.Commit(ctx)
}

func (a *dvrPostProcessAdapter) ReplaceCommercialMarkers(ctx context.Context, fileID uuid.UUID, source string, breaks []livetv.CommercialBreak) error {
	tx, err := a.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin markers tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := a.q.WithTx(tx)
	if err := qtx.DeleteCommercialMarkersByFile(ctx, fileID); err != nil {
		return err
	}
	for _, b := range breaks {
		if err := qtx.InsertCommercialMarker(ctx, gen.InsertCommercialMarkerParams{
			MediaFileID: fileID, StartMs: b.StartMS, EndMs: b.EndMS, Source: source,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// commercialMarkerLister serves the per-file commercials on the item detail.
type commercialMarkerLister struct{ q *gen.Queries }

func (c commercialMarkerLister) ListCommercialsByItem(ctx context.Context, itemID uuid.UUID) (map[uuid.UUID][]v1.CommercialMarkerJSON, error) {
	rows, err := c.q.ListCommercialMarkersByItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make(map[uuid.UUID][]v1.CommercialMarkerJSON)
	for _, r := range rows {
		out[r.MediaFileID] = append(out[r.MediaFileID], v1.CommercialMarkerJSON{
			StartMS: r.StartMs, EndMS: r.EndMs, Source: r.Source,
		})
	}
	return out, nil
}

// dvrPostProcessSettings adapts the stored DVR settings to the processor's
// view. Read per job, so a settings change applies to the next recording.
func dvrPostProcessSettings(svc *settings.Service) func(ctx context.Context) livetv.PostProcessSettings {
	return func(ctx context.Context) livetv.PostProcessSettings {
		c := svc.DVR(ctx)
		return livetv.PostProcessSettings{
			Remux:          c.Remux,
			Container:      c.RemuxContainer,
			Transcode:      c.Transcode,
			Codec:          c.TranscodeCodec,
			Quality:        c.TranscodeQuality,
			DeleteOriginal: c.DeleteOriginal,
			Commercials:    livetv.CommercialMode(c.Commercials),
		}
	}
}
