package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// apiKeyAdapter implements middleware.APIKeyResolver. Like
// sessionEpochAdapter it MUST be built on the read-write (primary) pool: a key
// deletion is a committed write there, and resolving keys from a lagging
// replica would let a just-deleted key keep working for the replication lag.
type apiKeyAdapter struct{ q *gen.Queries }

func (a *apiKeyAdapter) ResolveAPIKey(ctx context.Context, keyHash string) (middleware.APIKeyIdentity, error) {
	row, err := a.q.GetAPIKeyAuth(ctx, keyHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return middleware.APIKeyIdentity{}, middleware.ErrAPIKeyNotFound
		}
		return middleware.APIKeyIdentity{}, err
	}
	var rating string
	if row.MaxContentRating != nil {
		rating = *row.MaxContentRating
	}
	return middleware.APIKeyIdentity{
		KeyID:            row.ID,
		UserID:           row.UserID,
		Scope:            row.Scope,
		Username:         row.Username,
		IsAdmin:          row.IsAdmin,
		MaxContentRating: rating,
		BlockedLabels:    row.BlockedLabels,
		AllowedLabels:    row.AllowedLabels,
		SessionEpoch:     row.SessionEpoch,
	}, nil
}

func (a *apiKeyAdapter) APIKeyState(ctx context.Context, keyID, userID uuid.UUID) (int64, bool, error) {
	row, err := a.q.GetAPIKeyState(ctx, gen.GetAPIKeyStateParams{KeyID: keyID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, middleware.ErrUserNotFound
		}
		return 0, false, err
	}
	return row.SessionEpoch, row.KeyLive, nil
}

func (a *apiKeyAdapter) TouchAPIKey(ctx context.Context, keyID uuid.UUID) error {
	return a.q.TouchAPIKeyLastUsed(ctx, keyID)
}
