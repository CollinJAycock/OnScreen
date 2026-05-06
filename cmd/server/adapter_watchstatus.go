package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/watchstatus"
)

// watchStatusAdapter bridges sqlc → watchstatus.Querier.
type watchStatusAdapter struct{ q *gen.Queries }

func (a *watchStatusAdapter) GetUserWatchStatus(ctx context.Context, userID, mediaItemID uuid.UUID) (watchstatus.Status, error) {
	r, err := a.q.GetUserWatchStatus(ctx, gen.GetUserWatchStatusParams{
		UserID:      userID,
		MediaItemID: mediaItemID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return watchstatus.Status{}, watchstatus.ErrNotFound
		}
		return watchstatus.Status{}, err
	}
	return watchstatus.Status{
		UserID:      r.UserID,
		MediaItemID: r.MediaItemID,
		Status:      r.Status,
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}, nil
}

func (a *watchStatusAdapter) UpsertUserWatchStatus(ctx context.Context, userID, mediaItemID uuid.UUID, status string) (watchstatus.Status, error) {
	r, err := a.q.UpsertUserWatchStatus(ctx, gen.UpsertUserWatchStatusParams{
		UserID:      userID,
		MediaItemID: mediaItemID,
		Status:      status,
	})
	if err != nil {
		return watchstatus.Status{}, err
	}
	return watchstatus.Status{
		UserID:      r.UserID,
		MediaItemID: r.MediaItemID,
		Status:      r.Status,
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}, nil
}

func (a *watchStatusAdapter) DeleteUserWatchStatus(ctx context.Context, userID, mediaItemID uuid.UUID) error {
	return a.q.DeleteUserWatchStatus(ctx, gen.DeleteUserWatchStatusParams{
		UserID:      userID,
		MediaItemID: mediaItemID,
	})
}
