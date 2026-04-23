package main

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// sessionEpochAdapter implements middleware.SessionEpochReader against
// the read-only pool. The auth middleware calls GetSessionEpoch on
// every authenticated request, so this has to be cheap — a single
// indexed PK lookup on users.
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
