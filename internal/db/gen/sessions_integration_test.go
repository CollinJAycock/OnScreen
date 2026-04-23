//go:build integration

// Integration coverage for the refresh-token session queries. These
// back the POST /auth/refresh flow: the old token_hash must stop
// working the instant a new one is issued, and expired sessions
// must be excluded from GetSessionByTokenHash / ListUserSessions.
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func mustCreateSession(ctx context.Context, t *testing.T, q *gen.Queries, userID uuid.UUID, tokenHash string, expires time.Time) gen.Session {
	t.Helper()
	sess, err := q.CreateSession(ctx, gen.CreateSessionParams{
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}

// TestRotateSession_Integration_OldHashStopsWorking is the whole point
// of rotation: after the UPDATE, a lookup by the old token_hash must
// return pgx.ErrNoRows. If RotateSession ever inserts a new row instead
// of updating, or if the WHERE filter on GetSessionByTokenHash drops
// the expires_at check, this fails.
func TestRotateSession_Integration_OldHashStopsWorking(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	userID := seedUser(ctx, t, q, "rot-"+uuid.New().String()[:8])
	oldHash := "old-" + uuid.New().String()
	sess := mustCreateSession(ctx, t, q, userID, oldHash, time.Now().Add(time.Hour))

	newHash := "new-" + uuid.New().String()
	rotated, err := q.RotateSession(ctx, gen.RotateSessionParams{
		ID:        sess.ID,
		TokenHash: newHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(2 * time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("RotateSession: %v", err)
	}
	if rotated.ID != sess.ID {
		t.Errorf("rotation created a new row: id %s != %s", rotated.ID, sess.ID)
	}
	if rotated.TokenHash != newHash {
		t.Errorf("rotated.TokenHash = %q, want %q", rotated.TokenHash, newHash)
	}

	if _, err := q.GetSessionByTokenHash(ctx, oldHash); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("old hash still works after rotation: err = %v", err)
	}

	found, err := q.GetSessionByTokenHash(ctx, newHash)
	if err != nil {
		t.Fatalf("GetSessionByTokenHash new: %v", err)
	}
	if found.ID != sess.ID {
		t.Errorf("new hash resolves to wrong session: %s vs %s", found.ID, sess.ID)
	}
}

// TestGetSessionByTokenHash_Integration_FiltersExpired verifies the
// `expires_at > NOW()` clause — an expired refresh token must surface
// as pgx.ErrNoRows even though the row is still physically present
// (reaper is async).
func TestGetSessionByTokenHash_Integration_FiltersExpired(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	userID := seedUser(ctx, t, q, "exp-"+uuid.New().String()[:8])
	expiredHash := "expired-" + uuid.New().String()
	_ = mustCreateSession(ctx, t, q, userID, expiredHash, time.Now().Add(-time.Minute))

	if _, err := q.GetSessionByTokenHash(ctx, expiredHash); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expired session still resolves: err = %v", err)
	}
}

// TestListUserSessions_Integration_ExcludesExpired matches the view
// used by /users/me/sessions — stale rows from past devices must not
// appear in the list.
func TestListUserSessions_Integration_ExcludesExpired(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	userID := seedUser(ctx, t, q, "list-"+uuid.New().String()[:8])
	_ = mustCreateSession(ctx, t, q, userID, "alive-"+uuid.New().String(), time.Now().Add(time.Hour))
	_ = mustCreateSession(ctx, t, q, userID, "dead-"+uuid.New().String(), time.Now().Add(-time.Hour))

	sessions, err := q.ListUserSessions(ctx, userID)
	if err != nil {
		t.Fatalf("ListUserSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("got %d sessions, want 1 (expired should be filtered)", len(sessions))
	}
}

// TestTouchSession_Integration_UpdatesLastSeen confirms TouchSession
// bumps last_seen without mutating the token — used by the /auth/me
// path to mark a refresh token's associated session as active.
func TestTouchSession_Integration_UpdatesLastSeen(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	userID := seedUser(ctx, t, q, "touch-"+uuid.New().String()[:8])
	hash := "touch-" + uuid.New().String()
	sess := mustCreateSession(ctx, t, q, userID, hash, time.Now().Add(time.Hour))

	originalLastSeen := sess.LastSeen.Time
	time.Sleep(50 * time.Millisecond) // ensure NOW() has advanced past the original

	if err := q.TouchSession(ctx, sess.ID); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	after, err := q.GetSessionByTokenHash(ctx, hash)
	if err != nil {
		t.Fatalf("GetSessionByTokenHash: %v", err)
	}
	if !after.LastSeen.Time.After(originalLastSeen) {
		t.Errorf("last_seen did not advance: before=%s after=%s", originalLastSeen, after.LastSeen.Time)
	}
	if after.TokenHash != hash {
		t.Error("TouchSession mutated token_hash")
	}
}

// TestDeleteSession_Integration_RemovesRow proves logout wipes the row —
// not a soft-delete, not an expiry bump. The next GetSessionByTokenHash
// returns pgx.ErrNoRows.
func TestDeleteSession_Integration_RemovesRow(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	userID := seedUser(ctx, t, q, "del-"+uuid.New().String()[:8])
	hash := "del-" + uuid.New().String()
	sess := mustCreateSession(ctx, t, q, userID, hash, time.Now().Add(time.Hour))

	if err := q.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	if _, err := q.GetSessionByTokenHash(ctx, hash); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("deleted session still resolves: err = %v", err)
	}
}
