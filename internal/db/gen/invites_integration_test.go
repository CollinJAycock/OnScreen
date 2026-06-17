//go:build integration

// Round-trips the invite_tokens queries. Same security rationale as
// password_reset: the GetInviteToken query is the SOLE filter blocking
// expired and already-used invites from creating new accounts.
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

func createInvite(t *testing.T, q *gen.Queries, createdBy uuid.UUID, hash string, expiresAt time.Time, email *string) uuid.UUID {
	t.Helper()
	id, err := q.CreateInviteToken(context.Background(), gen.CreateInviteTokenParams{
		CreatedBy: createdBy,
		TokenHash: hash,
		Email:     email,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateInviteToken: %v", err)
	}
	return id
}

func TestInvites_Integration_GetReturnsLiveInvite(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	admin := seedUser(ctx, t, q, "inv-admin-"+uuid.New().String()[:8])
	hash := "live-" + uuid.New().String()
	em := "alice@example.com"
	id := createInvite(t, q, admin, hash, time.Now().Add(time.Hour), &em)

	got, err := q.GetInviteToken(ctx, hash)
	if err != nil {
		t.Fatalf("GetInviteToken: %v", err)
	}
	if got.ID != id {
		t.Errorf("id = %s, want %s", got.ID, id)
	}
	if got.Email == nil || *got.Email != "alice@example.com" {
		t.Errorf("email = %v, want alice@example.com", got.Email)
	}
}

// TestInvites_Integration_ExpiredInviteIsInvisible — a leaked-but-stale
// invite link must not create a fresh account.
func TestInvites_Integration_ExpiredInviteIsInvisible(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	admin := seedUser(ctx, t, q, "inv-exp-admin-"+uuid.New().String()[:8])
	hash := "exp-" + uuid.New().String()
	createInvite(t, q, admin, hash, time.Now().Add(-time.Hour), nil)

	if _, err := q.GetInviteToken(ctx, hash); err != pgx.ErrNoRows {
		t.Errorf("expired invite returned by Get: %v — should be ErrNoRows", err)
	}
}

// TestInvites_Integration_UsedInviteCannotBeReplayed — once an invite
// successfully provisioned a user, replaying the same link must fail.
func TestInvites_Integration_UsedInviteCannotBeReplayed(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	admin := seedUser(ctx, t, q, "inv-rpl-admin-"+uuid.New().String()[:8])
	hash := "used-" + uuid.New().String()
	id := createInvite(t, q, admin, hash, time.Now().Add(time.Hour), nil)

	consumer := seedUser(ctx, t, q, "inv-rpl-user-"+uuid.New().String()[:8])
	rows, err := q.ClaimInviteToken(ctx, id)
	if err != nil {
		t.Fatalf("ClaimInviteToken: %v", err)
	}
	if rows != 1 {
		t.Fatalf("first claim affected %d rows, want 1", rows)
	}
	if err := q.SetInviteTokenUsedBy(ctx, gen.SetInviteTokenUsedByParams{
		ID:     id,
		UsedBy: pgtype.UUID{Bytes: consumer, Valid: true},
	}); err != nil {
		t.Fatalf("SetInviteTokenUsedBy: %v", err)
	}

	if _, err := q.GetInviteToken(ctx, hash); err != pgx.ErrNoRows {
		t.Errorf("used invite still gettable: %v — replay vector", err)
	}
}

// TestInvites_Integration_ClaimIsSingleUse — the atomic guard that prevents two
// concurrent accepts from both minting an account off one invite. The first
// ClaimInviteToken flips used_at and affects 1 row; the second sees used_at
// already set and affects 0, so the racing handler is rejected.
func TestInvites_Integration_ClaimIsSingleUse(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	admin := seedUser(ctx, t, q, "inv-clm-admin-"+uuid.New().String()[:8])
	hash := "claim-" + uuid.New().String()
	id := createInvite(t, q, admin, hash, time.Now().Add(time.Hour), nil)

	first, err := q.ClaimInviteToken(ctx, id)
	if err != nil {
		t.Fatalf("first ClaimInviteToken: %v", err)
	}
	if first != 1 {
		t.Fatalf("first claim affected %d rows, want 1", first)
	}

	second, err := q.ClaimInviteToken(ctx, id)
	if err != nil {
		t.Fatalf("second ClaimInviteToken: %v", err)
	}
	if second != 0 {
		t.Errorf("second claim affected %d rows, want 0 — double-accept race", second)
	}
}

// TestInvites_Integration_ReleaseAllowsRetry — when account creation fails after
// a claim, ReleaseInviteToken puts the invite back so the user can retry (e.g.
// after a username collision) without the admin re-issuing it.
func TestInvites_Integration_ReleaseAllowsRetry(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	admin := seedUser(ctx, t, q, "inv-rel-admin-"+uuid.New().String()[:8])
	hash := "release-" + uuid.New().String()
	id := createInvite(t, q, admin, hash, time.Now().Add(time.Hour), nil)

	if rows, err := q.ClaimInviteToken(ctx, id); err != nil || rows != 1 {
		t.Fatalf("ClaimInviteToken: rows=%d err=%v", rows, err)
	}
	if err := q.ReleaseInviteToken(ctx, id); err != nil {
		t.Fatalf("ReleaseInviteToken: %v", err)
	}

	// After release the invite is live again and claimable.
	if _, err := q.GetInviteToken(ctx, hash); err != nil {
		t.Errorf("released invite not gettable: %v — retry blocked", err)
	}
	if rows, err := q.ClaimInviteToken(ctx, id); err != nil || rows != 1 {
		t.Errorf("re-claim after release: rows=%d err=%v, want 1", rows, err)
	}
}

// TestInvites_Integration_ListShowsAllStates proves ListInviteTokens
// is the admin-facing query and includes used + expired rows so the
// admin can see who-was-invited-when. Different filter than Get on
// purpose: Get is for token redemption (active only), List is for
// the admin invitation history view.
func TestInvites_Integration_ListShowsAllStates(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	admin := seedUser(ctx, t, q, "inv-lst-admin-"+uuid.New().String()[:8])
	createInvite(t, q, admin, "lst-live-"+uuid.New().String(), time.Now().Add(time.Hour), nil)
	createInvite(t, q, admin, "lst-exp-"+uuid.New().String(), time.Now().Add(-time.Hour), nil)

	// Track existing rows so the test isn't sensitive to other inserts.
	rows, err := q.ListInviteTokens(ctx)
	if err != nil {
		t.Fatalf("ListInviteTokens: %v", err)
	}
	var sawLive, sawExpired bool
	for _, r := range rows {
		// We can't filter by hash from the ListInviteTokens row directly
		// (no hash returned), but the admin context is enough — both
		// rows we just created share the same admin.
		if r.CreatedBy == admin {
			if r.ExpiresAt.Time.After(time.Now()) {
				sawLive = true
			} else {
				sawExpired = true
			}
		}
	}
	if !sawLive {
		t.Error("List should include the live invite")
	}
	if !sawExpired {
		t.Error("List should ALSO include the expired invite — admins need to see history")
	}
}

// TestInvites_Integration_DeleteRemovesRow — admin "revoke" hard-deletes.
func TestInvites_Integration_DeleteRemovesRow(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	admin := seedUser(ctx, t, q, "inv-del-"+uuid.New().String()[:8])
	hash := "del-" + uuid.New().String()
	id := createInvite(t, q, admin, hash, time.Now().Add(time.Hour), nil)

	if err := q.DeleteInviteToken(ctx, id); err != nil {
		t.Fatalf("DeleteInviteToken: %v", err)
	}
	if _, err := q.GetInviteToken(ctx, hash); err != pgx.ErrNoRows {
		t.Errorf("deleted invite still returned by Get: %v", err)
	}
}
