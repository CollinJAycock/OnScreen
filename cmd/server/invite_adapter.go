package main

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// inviteAdapter wraps gen.Queries to implement v1.InviteDB.
type inviteAdapter struct {
	q *gen.Queries
}

func (a *inviteAdapter) CreateInviteToken(ctx context.Context, createdBy uuid.UUID, tokenHash string, email *string, expiresAt time.Time) (uuid.UUID, error) {
	return a.q.CreateInviteToken(ctx, gen.CreateInviteTokenParams{
		CreatedBy: createdBy,
		TokenHash: tokenHash,
		Email:     email,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
}

func (a *inviteAdapter) GetInviteToken(ctx context.Context, tokenHash string) (v1.InviteTokenRow, error) {
	t, err := a.q.GetInviteToken(ctx, tokenHash)
	if err != nil {
		return v1.InviteTokenRow{}, err
	}
	return v1.InviteTokenRow{
		ID:        t.ID,
		CreatedBy: t.CreatedBy,
		Email:     t.Email,
	}, nil
}

func (a *inviteAdapter) MarkInviteTokenUsed(ctx context.Context, id uuid.UUID, usedBy uuid.UUID) error {
	return a.q.MarkInviteTokenUsed(ctx, gen.MarkInviteTokenUsedParams{
		ID:     id,
		UsedBy: pgtype.UUID{Bytes: usedBy, Valid: true},
	})
}

func (a *inviteAdapter) ListInviteTokens(ctx context.Context) ([]v1.InviteTokenSummaryRow, error) {
	rows, err := a.q.ListInviteTokens(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]v1.InviteTokenSummaryRow, len(rows))
	for i, r := range rows {
		out[i] = v1.InviteTokenSummaryRow{
			ID:        r.ID,
			CreatedBy: r.CreatedBy,
			Email:     r.Email,
			ExpiresAt: r.ExpiresAt.Time,
			CreatedAt: r.CreatedAt.Time,
		}
		if r.UsedAt.Valid {
			t := r.UsedAt.Time
			out[i].UsedAt = &t
		}
	}
	return out, nil
}

func (a *inviteAdapter) DeleteInviteToken(ctx context.Context, id uuid.UUID) error {
	return a.q.DeleteInviteToken(ctx, id)
}

func (a *inviteAdapter) CreateUser(ctx context.Context, username string, email *string, passwordHash string) (uuid.UUID, error) {
	user, err := a.q.CreateUser(ctx, gen.CreateUserParams{
		Username:     username,
		Email:        email,
		PasswordHash: &passwordHash,
		IsAdmin:      false,
	})
	if err != nil {
		return uuid.UUID{}, err
	}
	return user.ID, nil
}

func (a *inviteAdapter) GrantAutoLibrariesToUser(ctx context.Context, userID uuid.UUID) error {
	return a.q.GrantAutoLibrariesToUser(ctx, userID)
}
