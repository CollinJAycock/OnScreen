package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// sessionEpochAdapter implements middleware.SessionEpochReader. It MUST be
// built on the read-write (primary) pool: revocation is an epoch bump written
// to the primary, and reading it back from a replica delays the revocation by
// the replication lag — a logged-out / demoted / deleted user's token keeps
// working until the replica catches up. The auth middleware calls
// GetSessionEpoch on every authenticated request, so this has to stay cheap —
// a single indexed PK lookup on users.
type sessionEpochAdapter struct{ q *gen.Queries }

func (a *sessionEpochAdapter) GetSessionEpoch(ctx context.Context, userID uuid.UUID) (int64, error) {
	epoch, err := a.q.GetSessionEpoch(ctx, userID)
	if err != nil {
		// Translate "row gone" into the middleware sentinel so the
		// auth layer can fail closed on deleted users without
		// importing pgx.
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, middleware.ErrUserNotFound
		}
		return 0, err
	}
	return epoch, nil
}

// GetSessionState implements middleware.SessionStateReader: the epoch plus
// whether the token's refresh session still exists, in one primary-pool
// query (two PK probes). Called instead of GetSessionEpoch for tokens that
// carry a sid claim, so per-device revocation costs no extra round trip.
func (a *sessionEpochAdapter) GetSessionState(ctx context.Context, userID, sessionID uuid.UUID) (int64, bool, error) {
	row, err := a.q.GetSessionState(ctx, gen.GetSessionStateParams{UserID: userID, SessionID: sessionID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, middleware.ErrUserNotFound
		}
		return 0, false, err
	}
	return row.SessionEpoch, row.SessionLive, nil
}
