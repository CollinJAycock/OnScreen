//go:build integration

// Round-trips against a real Postgres testcontainer for the security-
// critical users-table queries: session_epoch (PASETO revocation),
// CreateFirstAdmin (atomic setup race), and the FK cascades that
// take a deleted user's sessions + library_access rows with them.
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// seedUser inserts a non-admin user and returns its ID.
func seedUser(ctx context.Context, t *testing.T, q *gen.Queries, username string) uuid.UUID {
	t.Helper()
	hash := "placeholder-bcrypt-hash"
	user, err := q.CreateUser(ctx, gen.CreateUserParams{
		Username:     username,
		PasswordHash: &hash,
		IsAdmin:      false,
	})
	if err != nil {
		t.Fatalf("CreateUser %q: %v", username, err)
	}
	return user.ID
}

// ── Session epoch ────────────────────────────────────────────────────────────

// TestSessionEpoch_Integration_BumpAndRead proves the session_epoch
// field starts at 0, bumps monotonically, and survives concurrent
// writers without losing updates — the SQL uses session_epoch + 1,
// which Postgres serializes per row.
func TestSessionEpoch_Integration_BumpAndRead(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	id := seedUser(ctx, t, q, "epoch-bump-"+uuid.New().String()[:8])

	epoch0, err := q.GetSessionEpoch(ctx, id)
	if err != nil {
		t.Fatalf("GetSessionEpoch initial: %v", err)
	}
	if epoch0 != 0 {
		t.Fatalf("initial epoch = %d, want 0", epoch0)
	}

	const bumps = 20
	var wg sync.WaitGroup
	for i := 0; i < bumps; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := q.BumpSessionEpoch(ctx, id); err != nil {
				t.Errorf("BumpSessionEpoch: %v", err)
			}
		}()
	}
	wg.Wait()

	final, err := q.GetSessionEpoch(ctx, id)
	if err != nil {
		t.Fatalf("GetSessionEpoch final: %v", err)
	}
	if final != bumps {
		t.Errorf("after %d concurrent bumps: epoch = %d, want %d (lost updates)", bumps, final, bumps)
	}
}

// TestSessionEpoch_Integration_DeletedUserReturnsErrNoRows pins the
// contract that adapter_session_epoch.go relies on: a deleted user's
// GetSessionEpoch lookup must surface pgx.ErrNoRows, which the adapter
// translates to middleware.ErrUserNotFound so the auth layer fails
// closed instead of fail-opening a deleted user's access token.
func TestSessionEpoch_Integration_DeletedUserReturnsErrNoRows(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	id := seedUser(ctx, t, q, "epoch-delete-"+uuid.New().String()[:8])
	if err := q.DeleteUser(ctx, id); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	_, err := q.GetSessionEpoch(ctx, id)
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("after delete: got err=%v, want pgx.ErrNoRows — fail-closed path will stop working", err)
	}
}

// ── First-admin race ─────────────────────────────────────────────────────────

// TestCreateFirstAdmin_Integration_AtomicUnderConcurrency drives 16
// concurrent CreateFirstAdmin calls and asserts exactly one succeeds.
// Without the `WHERE NOT EXISTS (SELECT 1 FROM users)` guard, multiple
// goroutines would each see count=0 and race to insert — the winners
// are bounded only by the unique-username constraint, which can yield
// multiple admins if they pick different usernames.
func TestCreateFirstAdmin_Integration_AtomicUnderConcurrency(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	const goroutines = 16
	var success int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	hash := "placeholder-bcrypt-hash"

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			username := "admin-" + uuid.New().String()[:12]
			_, err := q.CreateFirstAdmin(ctx, gen.CreateFirstAdminParams{
				Username:     username,
				PasswordHash: &hash,
			})
			if err == nil {
				atomic.AddInt64(&success, 1)
			} else if !errors.Is(err, pgx.ErrNoRows) {
				t.Errorf("goroutine %d: unexpected error %v", idx, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt64(&success); got != 1 {
		t.Errorf("expected exactly 1 successful first-admin insert, got %d", got)
	}

	count, err := q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 1 {
		t.Errorf("users table row count = %d, want 1", count)
	}

	admins, err := q.CountAdmins(ctx)
	if err != nil {
		t.Fatalf("CountAdmins: %v", err)
	}
	if admins != 1 {
		t.Errorf("admins count = %d, want 1", admins)
	}
}

// TestCreateFirstAdmin_Integration_NoOpWhenUsersExist proves the
// race-loser path — once any user exists, CreateFirstAdmin returns
// pgx.ErrNoRows and doesn't insert. The v1 handler maps this to a
// 409 SETUP_COMPLETE response.
func TestCreateFirstAdmin_Integration_NoOpWhenUsersExist(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	// Seed any existing user.
	_ = seedUser(ctx, t, q, "existing-"+uuid.New().String()[:8])

	hash := "placeholder-bcrypt-hash"
	_, err := q.CreateFirstAdmin(ctx, gen.CreateFirstAdminParams{
		Username:     "should-not-land",
		PasswordHash: &hash,
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected pgx.ErrNoRows, got %v", err)
	}

	count, err := q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 1 {
		t.Errorf("row count = %d, want 1 (the pre-seeded user)", count)
	}
}

// ── FK cascades on user delete ───────────────────────────────────────────────

// TestDeleteUser_Integration_CascadesSessionsAndLibraryAccess locks in
// the FK cascades from migration 00007 + 00028 that make user delete
// actually log the user out and revoke their ACLs. If either FK is
// ever downgraded to NO ACTION, this test fails before production
// inherits a ghost-session bug.
func TestDeleteUser_Integration_CascadesSessionsAndLibraryAccess(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	userID := seedUser(ctx, t, q, "cascade-"+uuid.New().String()[:8])

	// Seed a session for the user.
	_, err := q.CreateSession(ctx, gen.CreateSessionParams{
		UserID:    userID,
		TokenHash: "hash-" + uuid.New().String(),
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Seed a library and grant access to the user.
	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name:                    "cascade-lib-" + uuid.New().String()[:8],
		Type:                    "movie",
		ScanPaths:               []string{"/tmp"},
		Agent:                   "tmdb",
		Language:                "en",
		ScanInterval:            time.Hour,
		MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	if err := q.GrantLibraryAccess(ctx, gen.GrantLibraryAccessParams{
		UserID:    userID,
		LibraryID: lib.ID,
	}); err != nil {
		t.Fatalf("GrantLibraryAccess: %v", err)
	}

	// Confirm seeds landed.
	var sessionsBefore, accessBefore int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = $1`, userID).Scan(&sessionsBefore); err != nil {
		t.Fatalf("count sessions before: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM library_access WHERE user_id = $1`, userID).Scan(&accessBefore); err != nil {
		t.Fatalf("count library_access before: %v", err)
	}
	if sessionsBefore != 1 || accessBefore != 1 {
		t.Fatalf("seed failure: sessions=%d access=%d", sessionsBefore, accessBefore)
	}

	if err := q.DeleteUser(ctx, userID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	var sessionsAfter, accessAfter int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = $1`, userID).Scan(&sessionsAfter); err != nil {
		t.Fatalf("count sessions after: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM library_access WHERE user_id = $1`, userID).Scan(&accessAfter); err != nil {
		t.Fatalf("count library_access after: %v", err)
	}
	if sessionsAfter != 0 {
		t.Errorf("sessions cascade failed: %d rows remain", sessionsAfter)
	}
	if accessAfter != 0 {
		t.Errorf("library_access cascade failed: %d rows remain", accessAfter)
	}
}
