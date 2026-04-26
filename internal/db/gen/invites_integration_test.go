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
	if err := q.MarkInviteTokenUsed(ctx, gen.MarkInviteTokenUsedParams{
		ID:     id,
		UsedBy: pgtype.UUID{Bytes: consumer, Valid: true},
	}); err != nil {
		t.Fatalf("MarkInviteTokenUsed: %v", err)
	}

	if _, err := q.GetInviteToken(ctx, hash); err != pgx.ErrNoRows {
		t.Errorf("used invite still gettable: %v — replay vector", err)
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
