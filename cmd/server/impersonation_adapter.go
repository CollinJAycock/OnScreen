package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// impersonationAdapter wraps gen.Queries to implement
// middleware.ImpersonationLookup. The interface is intentionally
// narrow so the auth middleware doesn't pull in the whole gen
// surface.
type impersonationAdapter struct {
	q *gen.Queries
}

func (a *impersonationAdapter) GetUserForImpersonation(ctx context.Context, userID uuid.UUID) (middleware.ImpersonatedUser, error) {
	row, err := a.q.GetUserForImpersonation(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return middleware.ImpersonatedUser{}, middleware.ErrUserNotFound
		}
		return middleware.ImpersonatedUser{}, err
	}
	var rating string
	if row.MaxContentRating != nil {
		rating = *row.MaxContentRating
	}
	return middleware.ImpersonatedUser{
		ID:               row.ID,
		Username:         row.Username,
		IsAdmin:          row.IsAdmin,
		MaxContentRating: rating,
	}, nil
}
