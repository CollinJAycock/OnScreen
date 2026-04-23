package main

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/watchevent"
)

type watchEventAdapter struct{ q *gen.Queries }

func (a *watchEventAdapter) InsertWatchEvent(ctx context.Context, p watchevent.InsertWatchEventParams) (watchevent.InsertWatchEventRow, error) {
	r, err := a.q.InsertWatchEvent(ctx, gen.InsertWatchEventParams{
		UserID:     p.UserID,
		MediaID:    p.MediaID,
		FileID:     uuidPtrToPgtype(p.FileID),
		SessionID:  uuidPtrToPgtype(p.SessionID),
		EventType:  p.EventType,
		PositionMs: p.PositionMS,
		DurationMs: p.DurationMS,
		ClientID:   p.ClientID,
		ClientName: p.ClientName,
		ClientIp:   p.ClientIP,
		OccurredAt: pgtype.Timestamptz{Time: p.OccurredAt, Valid: true},
	})
	if err != nil {
		return watchevent.InsertWatchEventRow{}, err
	}
	return watchevent.InsertWatchEventRow{
		ID:         r.ID,
		OccurredAt: r.OccurredAt.Time,
	}, nil
}

func (a *watchEventAdapter) RefreshWatchState(ctx context.Context) error {
	return a.q.RefreshWatchState(ctx)
}

func (a *watchEventAdapter) GetWatchState(ctx context.Context, userID, mediaID uuid.UUID) (watchevent.WatchState, error) {
	r, err := a.q.GetWatchState(ctx, gen.GetWatchStateParams{
		UserID:  userID,
		MediaID: mediaID,
	})
	if err != nil {
		return watchevent.WatchState{}, err
	}
	return watchevent.WatchState{
		UserID:         r.UserID,
		MediaID:        r.MediaID,
		PositionMS:     r.PositionMs,
		DurationMS:     r.DurationMs,
		Status:         r.Status,
		LastWatchedAt:  r.LastWatchedAt.Time,
		LastClientID:   r.LastClientID,
		LastClientName: r.LastClientName,
	}, nil
}

func (a *watchEventAdapter) ListWatchStateForUser(ctx context.Context, userID uuid.UUID) ([]watchevent.WatchState, error) {
	rows, err := a.q.ListWatchStateForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]watchevent.WatchState, len(rows))
	for i, r := range rows {
		out[i] = watchevent.WatchState{
			UserID:         r.UserID,
			MediaID:        r.MediaID,
			PositionMS:     r.PositionMs,
			DurationMS:     r.DurationMs,
			Status:         r.Status,
			LastWatchedAt:  r.LastWatchedAt.Time,
			LastClientID:   r.LastClientID,
			LastClientName: r.LastClientName,
		}
	}
	return out, nil
}
