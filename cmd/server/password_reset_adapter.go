package main

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// passwordResetAdapter wraps gen.Queries to implement v1.PasswordResetDB.
type passwordResetAdapter struct {
	q *gen.Queries
}

func (a *passwordResetAdapter) GetUserByEmail(ctx context.Context, email *string) (v1.PRUser, error) {
	if email == nil {
		return v1.PRUser{}, fmt.Errorf("email is nil")
	}
	u, err := a.q.GetUserByEmail(ctx, email)
	if err != nil {
		return v1.PRUser{}, err
	}
	return v1.PRUser{ID: u.ID, Username: u.Username, Email: u.Email}, nil
}

func (a *passwordResetAdapter) CreateResetToken(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	return a.q.CreatePasswordResetToken(ctx, gen.CreatePasswordResetTokenParams{
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
}

func (a *passwordResetAdapter) GetResetToken(ctx context.Context, tokenHash string) (v1.PRToken, error) {
	t, err := a.q.GetPasswordResetToken(ctx, tokenHash)
	if err != nil {
		return v1.PRToken{}, err
	}
	return v1.PRToken{ID: t.ID, UserID: t.UserID}, nil
}

func (a *passwordResetAdapter) MarkResetTokenUsed(ctx context.Context, id uuid.UUID) error {
	return a.q.MarkPasswordResetTokenUsed(ctx, id)
}

func (a *passwordResetAdapter) UpdatePassword(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	return a.q.UpdateUserPassword(ctx, gen.UpdateUserPasswordParams{
		ID:           userID,
		PasswordHash: &passwordHash,
	})
}

func (a *passwordResetAdapter) BumpSessionEpoch(ctx context.Context, userID uuid.UUID) error {
	return a.q.BumpSessionEpoch(ctx, userID)
}

func (a *passwordResetAdapter) DeleteSessionsForUser(ctx context.Context, userID uuid.UUID) error {
	return a.q.DeleteSessionsForUser(ctx, userID)
}
