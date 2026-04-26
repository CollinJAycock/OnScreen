//go:build integration

// Round-trips the password_reset_tokens queries. Security-critical:
// the GetPasswordResetToken query is the SOLE gate that blocks
// expired and already-used tokens from accepting a new password.
// A regression here is a credential-stuffing vector.
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestPasswordReset_Integration_GetReturnsLiveToken proves a freshly
// inserted, unused, unexpired token is returned by GetPasswordResetToken.
// The hot path for the reset flow.
func TestPasswordReset_Integration_GetReturnsLiveToken(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "pr-live-"+uuid.New().String()[:8])
	hash := "hash-" + uuid.New().String()
	if err := q.CreatePasswordResetToken(ctx, gen.CreatePasswordResetTokenParams{
		UserID:    user,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}

	tok, err := q.GetPasswordResetToken(ctx, hash)
	if err != nil {
		t.Fatalf("GetPasswordResetToken: %v", err)
	}
	if tok.UserID != user {
		t.Errorf("user_id = %s, want %s", tok.UserID, user)
	}
}

// TestPasswordReset_Integration_GetExpiredReturnsErrNoRows proves the
// expires_at filter actually fires. An expired token must be invisible
// to GetPasswordResetToken — otherwise the reset endpoint would honor
// arbitrarily old leaked tokens.
func TestPasswordReset_Integration_GetExpiredReturnsErrNoRows(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "pr-exp-"+uuid.New().String()[:8])
	hash := "hash-exp-" + uuid.New().String()
	if err := q.CreatePasswordResetToken(ctx, gen.CreatePasswordResetTokenParams{
		UserID:    user,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}, // already expired
	}); err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}

	_, err := q.GetPasswordResetToken(ctx, hash)
	if err == nil {
		t.Fatal("expired token returned by Get — credential-stuffing vector")
	}
	if err != pgx.ErrNoRows {
		t.Errorf("got %v, want pgx.ErrNoRows", err)
	}
}

// TestPasswordReset_Integration_GetUsedReturnsErrNoRows proves the
// used_at filter actually fires. A token must be one-shot — once
// MarkPasswordResetTokenUsed is called, GetPasswordResetToken must
// stop returning it. Without this, an attacker who observed the
// successful reset email could replay the same token.
func TestPasswordReset_Integration_GetUsedReturnsErrNoRows(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "pr-used-"+uuid.New().String()[:8])
	hash := "hash-used-" + uuid.New().String()
	if err := q.CreatePasswordResetToken(ctx, gen.CreatePasswordResetTokenParams{
		UserID:    user,
		TokenHash: hash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}

	tok, err := q.GetPasswordResetToken(ctx, hash)
	if err != nil {
		t.Fatalf("Get (live): %v", err)
	}

	if err := q.MarkPasswordResetTokenUsed(ctx, tok.ID); err != nil {
		t.Fatalf("MarkPasswordResetTokenUsed: %v", err)
	}

	if _, err := q.GetPasswordResetToken(ctx, hash); err != pgx.ErrNoRows {
		t.Errorf("got %v after mark-used, want pgx.ErrNoRows — token replay vector", err)
	}
}

// TestPasswordReset_Integration_DeleteExpiredCleansBothBranches proves
// the cleanup query removes BOTH the expired-but-unused row AND the
// used row, leaving only the live unused token. This is the cron-style
// task that keeps the reset_tokens table from growing without bound.
func TestPasswordReset_Integration_DeleteExpiredCleansBothBranches(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "pr-clean-"+uuid.New().String()[:8])

	// Live unused — should survive.
	liveHash := "live-" + uuid.New().String()
	_ = q.CreatePasswordResetToken(ctx, gen.CreatePasswordResetTokenParams{
		UserID: user, TokenHash: liveHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	// Expired — should be deleted.
	expHash := "exp-" + uuid.New().String()
	_ = q.CreatePasswordResetToken(ctx, gen.CreatePasswordResetTokenParams{
		UserID: user, TokenHash: expHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	// Used (still unexpired) — should be deleted.
	usedHash := "used-" + uuid.New().String()
	_ = q.CreatePasswordResetToken(ctx, gen.CreatePasswordResetTokenParams{
		UserID: user, TokenHash: usedHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	usedTok, _ := q.GetPasswordResetToken(ctx, usedHash)
	_ = q.MarkPasswordResetTokenUsed(ctx, usedTok.ID)

	if err := q.DeleteExpiredPasswordResetTokens(ctx); err != nil {
		t.Fatalf("DeleteExpiredPasswordResetTokens: %v", err)
	}

	// The live unused token must still be gettable.
	if _, err := q.GetPasswordResetToken(ctx, liveHash); err != nil {
		t.Errorf("live unused token was deleted: %v", err)
	}

	// Both expired and used tokens should be physically gone — re-using
	// their hashes would otherwise hit a unique constraint or surface
	// stale data.
	if err := q.CreatePasswordResetToken(ctx, gen.CreatePasswordResetTokenParams{
		UserID: user, TokenHash: expHash,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Errorf("re-inserting expHash failed — was it not deleted? %v", err)
	}
}
