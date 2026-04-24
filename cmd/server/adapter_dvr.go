package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/livetv"
)

// dvrAdapter implements livetv.DVRQuerier on top of sqlc. Composed
// alongside livetvAdapter at wiring time — this lets us share the
// Queries handle without coupling the two interfaces.
type dvrAdapter struct {
	q    *gen.Queries
	live *livetvAdapter // reuse ListEPGProgramsInWindow + ListTunerDevices
}

func newDVRAdapter(q *gen.Queries, live *livetvAdapter) *dvrAdapter {
	return &dvrAdapter{q: q, live: live}
}

// ── Schedules ────────────────────────────────────────────────────────────────

func (a *dvrAdapter) CreateSchedule(ctx context.Context, p livetv.CreateScheduleParams) (livetv.Schedule, error) {
	row, err := a.q.CreateSchedule(ctx, gen.CreateScheduleParams{
		UserID:         p.UserID,
		Type:           string(p.Type),
		ProgramID:      uuidPtrToPg(p.ProgramID),
		ChannelID:      uuidPtrToPg(p.ChannelID),
		TitleMatch:     p.TitleMatch,
		NewOnly:        p.NewOnly,
		TimeStart:      p.TimeStart,
		TimeEnd:        p.TimeEnd,
		PaddingPreSec:  p.PaddingPreSec,
		PaddingPostSec: p.PaddingPostSec,
		Priority:       p.Priority,
		RetentionDays:  p.RetentionDays,
	})
	if err != nil {
		return livetv.Schedule{}, err
	}
	return scheduleFromGen(row), nil
}

func (a *dvrAdapter) GetSchedule(ctx context.Context, id uuid.UUID) (livetv.Schedule, error) {
	row, err := a.q.GetSchedule(ctx, id)
	if err != nil {
		return livetv.Schedule{}, err
	}
	return scheduleFromGen(row), nil
}

func (a *dvrAdapter) ListSchedulesForUser(ctx context.Context, userID uuid.UUID) ([]livetv.Schedule, error) {
	rows, err := a.q.ListSchedulesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]livetv.Schedule, len(rows))
	for i, r := range rows {
		out[i] = scheduleFromGen(r)
	}
	return out, nil
}

func (a *dvrAdapter) ListEnabledSchedules(ctx context.Context) ([]livetv.Schedule, error) {
	rows, err := a.q.ListEnabledSchedules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]livetv.Schedule, len(rows))
	for i, r := range rows {
		out[i] = scheduleFromGen(r)
	}
	return out, nil
}

func (a *dvrAdapter) DeleteSchedule(ctx context.Context, id uuid.UUID) error {
	return a.q.DeleteSchedule(ctx, id)
}

func (a *dvrAdapter) SetScheduleEnabled(ctx context.Context, id uuid.UUID, enabled bool) error {
	return a.q.SetScheduleEnabled(ctx, gen.SetScheduleEnabledParams{ID: id, Enabled: enabled})
}

// ── Recordings ───────────────────────────────────────────────────────────────

func (a *dvrAdapter) UpsertRecording(ctx context.Context, p livetv.UpsertRecordingParams) (livetv.Recording, error) {
	pid := p.ProgramID
	row, err := a.q.UpsertRecording(ctx, gen.UpsertRecordingParams{
		ScheduleID: uuidPtrToPg(p.ScheduleID),
		UserID:     p.UserID,
		ChannelID:  p.ChannelID,
		ProgramID:  uuidPtrToPg(&pid),
		Title:      p.Title,
		Subtitle:   p.Subtitle,
		SeasonNum:  p.SeasonNum,
		EpisodeNum: p.EpisodeNum,
		StartsAt:   pgtype.Timestamptz{Time: p.StartsAt, Valid: true},
		EndsAt:     pgtype.Timestamptz{Time: p.EndsAt, Valid: true},
	})
	if err != nil {
		return livetv.Recording{}, err
	}
	return recordingFromGen(row), nil
}

func (a *dvrAdapter) GetRecording(ctx context.Context, id uuid.UUID) (livetv.Recording, error) {
	row, err := a.q.GetRecording(ctx, id)
	if err != nil {
		return livetv.Recording{}, err
	}
	return recordingFromGen(row), nil
}

func (a *dvrAdapter) ListRecordingsForUser(ctx context.Context, userID uuid.UUID, status *string, limit, offset int32) ([]livetv.RecordingWithChannel, error) {
	rows, err := a.q.ListRecordingsForUser(ctx, gen.ListRecordingsForUserParams{
		UserID: userID,
		Status: status,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]livetv.RecordingWithChannel, len(rows))
	for i, r := range rows {
		out[i] = livetv.RecordingWithChannel{
			Recording: livetv.Recording{
				ID:         r.ID,
				ScheduleID: pgUUIDToPtr(r.ScheduleID),
				UserID:     r.UserID,
				ChannelID:  r.ChannelID,
				ProgramID:  pgUUIDToPtr(r.ProgramID),
				Title:      r.Title,
				Subtitle:   r.Subtitle,
				SeasonNum:  r.SeasonNum,
				EpisodeNum: r.EpisodeNum,
				Status:     livetv.RecordingStatus(r.Status),
				StartsAt:   r.StartsAt.Time,
				EndsAt:     r.EndsAt.Time,
				FilePath:   r.FilePath,
				ItemID:     pgUUIDToPtr(r.ItemID),
				Error:      r.Error,
			},
			ChannelNumber: r.ChannelNumber,
			ChannelName:   r.ChannelName,
			ChannelLogo:   r.LogoUrl,
		}
		if r.CreatedAt.Valid {
			out[i].CreatedAt = r.CreatedAt.Time
		}
		if r.UpdatedAt.Valid {
			out[i].UpdatedAt = r.UpdatedAt.Time
		}
	}
	return out, nil
}

func (a *dvrAdapter) ListDueRecordings(ctx context.Context, upTo time.Time) ([]livetv.Recording, error) {
	rows, err := a.q.ListDueRecordings(ctx, pgtype.Timestamptz{Time: upTo, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]livetv.Recording, len(rows))
	for i, r := range rows {
		out[i] = recordingFromGen(r)
	}
	return out, nil
}

func (a *dvrAdapter) ListActiveRecordings(ctx context.Context) ([]livetv.Recording, error) {
	rows, err := a.q.ListActiveRecordings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]livetv.Recording, len(rows))
	for i, r := range rows {
		out[i] = recordingFromGen(r)
	}
	return out, nil
}

func (a *dvrAdapter) SetRecordingStatus(ctx context.Context, id uuid.UUID, status livetv.RecordingStatus) error {
	return a.q.SetRecordingStatus(ctx, gen.SetRecordingStatusParams{ID: id, Status: string(status)})
}

func (a *dvrAdapter) SetRecordingStartedFile(ctx context.Context, id uuid.UUID, filePath string) error {
	return a.q.SetRecordingStartedFile(ctx, gen.SetRecordingStartedFileParams{ID: id, FilePath: &filePath})
}

func (a *dvrAdapter) SetRecordingCompleted(ctx context.Context, id uuid.UUID, itemID uuid.UUID) error {
	return a.q.SetRecordingCompleted(ctx, gen.SetRecordingCompletedParams{ID: id, ItemID: uuidPtrToPg(&itemID)})
}

func (a *dvrAdapter) SetRecordingFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	return a.q.SetRecordingFailed(ctx, gen.SetRecordingFailedParams{ID: id, Error: &errMsg})
}

func (a *dvrAdapter) DeleteRecording(ctx context.Context, id uuid.UUID) error {
	return a.q.DeleteRecording(ctx, id)
}

func (a *dvrAdapter) ListExpiredRecordings(ctx context.Context) ([]livetv.ExpiredRecording, error) {
	rows, err := a.q.ListExpiredRecordings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]livetv.ExpiredRecording, 0, len(rows))
	for _, r := range rows {
		er := livetv.ExpiredRecording{ID: r.ID, FilePath: r.FilePath}
		if r.ItemID.Valid {
			id := uuid.UUID(r.ItemID.Bytes)
			er.ItemID = &id
		}
		out = append(out, er)
	}
	return out, nil
}

// ── Reuse from live adapter ─────────────────────────────────────────────────

func (a *dvrAdapter) ListEPGProgramsInWindow(ctx context.Context, from, to time.Time) ([]livetv.EPGProgram, error) {
	return a.live.ListEPGProgramsInWindow(ctx, from, to)
}

func (a *dvrAdapter) ListTunerDevices(ctx context.Context) ([]livetv.TunerDevice, error) {
	return a.live.ListTunerDevices(ctx)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// dvrMediaCreator wires livetv.DVRWorker into media.Service so a
// finalized recording lands in a media_items row (+ media_files row for
// its on-disk .mp4) that the existing player/library paths can surface.
type dvrMediaCreator struct {
	svc *media.Service
}

func (c *dvrMediaCreator) CreateDVRMediaItem(ctx context.Context, p livetv.DVRMediaItemParams) (uuid.UUID, error) {
	mediaType := "movie"
	if p.SeasonNum != nil && p.EpisodeNum != nil {
		mediaType = "episode"
	}
	// Convert pointer-to-int32 → *int for media.CreateItemParams.
	var year *int
	if p.AiredAt != nil {
		y := p.AiredAt.Year()
		year = &y
	}
	// FindOrCreateItem is idempotent on title/type/library so a second
	// finalize after retry doesn't produce duplicate media_items rows.
	item, err := c.svc.FindOrCreateItem(ctx, media.CreateItemParams{
		LibraryID:             p.LibraryID,
		Type:                  mediaType,
		Title:                 p.Title,
		SortTitle:             p.Title,
		Year:                  year,
		OriginallyAvailableAt: p.AiredAt,
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("create dvr media item: %w", err)
	}
	// File row points at the captured MP4. Size + container come from
	// statting the file — scheduled scanner will re-probe codecs on its
	// next pass so we don't shell out to ffprobe in the hot path.
	fi, err := os.Stat(p.FilePath)
	if err != nil {
		return uuid.Nil, fmt.Errorf("stat capture: %w", err)
	}
	container := "mp4"
	if _, _, err := c.svc.CreateOrUpdateFile(ctx, media.CreateFileParams{
		MediaItemID: item.ID,
		FilePath:    p.FilePath,
		FileSize:    fi.Size(),
		Container:   &container,
	}); err != nil {
		return uuid.Nil, fmt.Errorf("create dvr media file: %w", err)
	}
	_ = filepath.Base(p.FilePath) // filepath stays in imports for future use
	return item.ID, nil
}

// uuidPtrToPg converts a *uuid.UUID (nil = NULL) to the pgtype.UUID
// that sqlc emits for nullable uuid columns.
func uuidPtrToPg(p *uuid.UUID) pgtype.UUID {
	if p == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *p, Valid: true}
}

// pgUUIDToPtr is the inverse — nullable pgtype.UUID → *uuid.UUID.
func pgUUIDToPtr(v pgtype.UUID) *uuid.UUID {
	if !v.Valid {
		return nil
	}
	u := uuid.UUID(v.Bytes)
	return &u
}


func scheduleFromGen(r gen.Schedule) livetv.Schedule {
	s := livetv.Schedule{
		ID:             r.ID,
		UserID:         r.UserID,
		Type:           livetv.ScheduleType(r.Type),
		ProgramID:      pgUUIDToPtr(r.ProgramID),
		ChannelID:      pgUUIDToPtr(r.ChannelID),
		TitleMatch:     r.TitleMatch,
		NewOnly:        r.NewOnly,
		TimeStart:      r.TimeStart,
		TimeEnd:        r.TimeEnd,
		PaddingPreSec:  r.PaddingPreSec,
		PaddingPostSec: r.PaddingPostSec,
		Priority:       r.Priority,
		RetentionDays:  r.RetentionDays,
		Enabled:        r.Enabled,
	}
	if r.CreatedAt.Valid {
		s.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		s.UpdatedAt = r.UpdatedAt.Time
	}
	return s
}

// recordingFromGen maps a gen.Recording → domain Recording.
func recordingFromGen(r gen.Recording) livetv.Recording {
	rec := livetv.Recording{
		ID:         r.ID,
		ScheduleID: pgUUIDToPtr(r.ScheduleID),
		UserID:     r.UserID,
		ChannelID:  r.ChannelID,
		ProgramID:  pgUUIDToPtr(r.ProgramID),
		Title:      r.Title,
		Subtitle:   r.Subtitle,
		SeasonNum:  r.SeasonNum,
		EpisodeNum: r.EpisodeNum,
		Status:     livetv.RecordingStatus(r.Status),
		StartsAt:   r.StartsAt.Time,
		EndsAt:     r.EndsAt.Time,
		FilePath:   r.FilePath,
		ItemID:     pgUUIDToPtr(r.ItemID),
		Error:      r.Error,
	}
	if r.CreatedAt.Valid {
		rec.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		rec.UpdatedAt = r.UpdatedAt.Time
	}
	return rec
}
