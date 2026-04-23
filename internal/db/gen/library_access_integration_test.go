//go:build integration

// Round-trips the library_access queries that gate every per-user
// item lookup. These queries are the authoritative source of truth
// for "can this user see this library" — a regression here is an
// IDOR on the entire catalog.
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func seedLibrary(ctx context.Context, t *testing.T, q *gen.Queries, name string) uuid.UUID {
	t.Helper()
	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name:                    name,
		Type:                    "movie",
		ScanPaths:               []string{"/tmp/" + name},
		Agent:                   "tmdb",
		Language:                "en",
		ScanInterval:            time.Hour,
		MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("CreateLibrary %q: %v", name, err)
	}
	return lib.ID
}

// TestLibraryAccess_Integration_GrantAndCheck covers the happy path
// and the negative path: a granted user returns true, a non-granted
// user returns false. HasLibraryAccess is called on every item read,
// so a broken version would leak cross-user data.
func TestLibraryAccess_Integration_GrantAndCheck(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	userA := seedUser(ctx, t, q, "acl-a-"+uuid.New().String()[:8])
	userB := seedUser(ctx, t, q, "acl-b-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "acl-lib-"+uuid.New().String()[:8])

	if err := q.GrantLibraryAccess(ctx, gen.GrantLibraryAccessParams{
		UserID: userA, LibraryID: lib,
	}); err != nil {
		t.Fatalf("GrantLibraryAccess A: %v", err)
	}

	ok, err := q.HasLibraryAccess(ctx, gen.HasLibraryAccessParams{UserID: userA, LibraryID: lib})
	if err != nil {
		t.Fatalf("HasLibraryAccess A: %v", err)
	}
	if !ok {
		t.Error("granted user: HasLibraryAccess = false, want true")
	}

	ok, err = q.HasLibraryAccess(ctx, gen.HasLibraryAccessParams{UserID: userB, LibraryID: lib})
	if err != nil {
		t.Fatalf("HasLibraryAccess B: %v", err)
	}
	if ok {
		t.Error("ungranted user: HasLibraryAccess = true, want false — IDOR")
	}
}

// TestLibraryAccess_Integration_GrantIsIdempotent proves the ON
// CONFLICT DO NOTHING clause works — callers can re-run Grant
// without hitting a unique-violation.
func TestLibraryAccess_Integration_GrantIsIdempotent(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "acl-idem-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "acl-idem-lib-"+uuid.New().String()[:8])

	for i := 0; i < 3; i++ {
		if err := q.GrantLibraryAccess(ctx, gen.GrantLibraryAccessParams{
			UserID: user, LibraryID: lib,
		}); err != nil {
			t.Fatalf("GrantLibraryAccess iteration %d: %v", i, err)
		}
	}

	libs, err := q.ListLibraryAccessByUser(ctx, user)
	if err != nil {
		t.Fatalf("ListLibraryAccessByUser: %v", err)
	}
	if len(libs) != 1 {
		t.Errorf("expected 1 grant row after 3 idempotent Grants, got %d", len(libs))
	}
}

// TestLibraryAccess_Integration_RevokeRemovesRow verifies Revoke
// takes the ACL back without touching other grants.
func TestLibraryAccess_Integration_RevokeRemovesRow(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "acl-rev-"+uuid.New().String()[:8])
	lib1 := seedLibrary(ctx, t, q, "acl-rev-l1-"+uuid.New().String()[:8])
	lib2 := seedLibrary(ctx, t, q, "acl-rev-l2-"+uuid.New().String()[:8])

	for _, lid := range []uuid.UUID{lib1, lib2} {
		if err := q.GrantLibraryAccess(ctx, gen.GrantLibraryAccessParams{
			UserID: user, LibraryID: lid,
		}); err != nil {
			t.Fatalf("GrantLibraryAccess: %v", err)
		}
	}

	if err := q.RevokeLibraryAccess(ctx, gen.RevokeLibraryAccessParams{
		UserID: user, LibraryID: lib1,
	}); err != nil {
		t.Fatalf("RevokeLibraryAccess: %v", err)
	}

	if ok, err := q.HasLibraryAccess(ctx, gen.HasLibraryAccessParams{UserID: user, LibraryID: lib1}); err != nil {
		t.Fatalf("HasLibraryAccess lib1: %v", err)
	} else if ok {
		t.Error("revoked grant still returns true")
	}
	if ok, err := q.HasLibraryAccess(ctx, gen.HasLibraryAccessParams{UserID: user, LibraryID: lib2}); err != nil {
		t.Fatalf("HasLibraryAccess lib2: %v", err)
	} else if !ok {
		t.Error("neighbouring grant was wrongly revoked")
	}
}

// TestLibraryAccess_Integration_ListAllowedIgnoresSoftDeleted proves
// ListAllowedLibraryIDsForUser filters out libraries whose deleted_at
// is set — otherwise a user could still see items from a library the
// admin "deleted" from the Libraries page.
func TestLibraryAccess_Integration_ListAllowedIgnoresSoftDeleted(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	user := seedUser(ctx, t, q, "acl-soft-"+uuid.New().String()[:8])
	live := seedLibrary(ctx, t, q, "acl-soft-live-"+uuid.New().String()[:8])
	dead := seedLibrary(ctx, t, q, "acl-soft-dead-"+uuid.New().String()[:8])

	for _, lid := range []uuid.UUID{live, dead} {
		if err := q.GrantLibraryAccess(ctx, gen.GrantLibraryAccessParams{
			UserID: user, LibraryID: lid,
		}); err != nil {
			t.Fatalf("GrantLibraryAccess: %v", err)
		}
	}

	if _, err := pool.Exec(ctx, `UPDATE libraries SET deleted_at = NOW() WHERE id = $1`, dead); err != nil {
		t.Fatalf("soft-delete library: %v", err)
	}

	ids, err := q.ListAllowedLibraryIDsForUser(ctx, user)
	if err != nil {
		t.Fatalf("ListAllowedLibraryIDsForUser: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("got %d allowed libraries, want 1", len(ids))
	}
	if ids[0] != live {
		t.Errorf("allowed id = %s, want live library %s", ids[0], live)
	}
}

// TestLibraryAccess_Integration_RevokeAllForUser proves bulk revoke
// wipes every grant row for the user, leaving others untouched.
func TestLibraryAccess_Integration_RevokeAllForUser(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	victim := seedUser(ctx, t, q, "acl-wipe-v-"+uuid.New().String()[:8])
	bystander := seedUser(ctx, t, q, "acl-wipe-b-"+uuid.New().String()[:8])
	lib := seedLibrary(ctx, t, q, "acl-wipe-lib-"+uuid.New().String()[:8])

	for _, uid := range []uuid.UUID{victim, bystander} {
		if err := q.GrantLibraryAccess(ctx, gen.GrantLibraryAccessParams{
			UserID: uid, LibraryID: lib,
		}); err != nil {
			t.Fatalf("GrantLibraryAccess: %v", err)
		}
	}

	if err := q.RevokeAllLibraryAccessForUser(ctx, victim); err != nil {
		t.Fatalf("RevokeAllLibraryAccessForUser: %v", err)
	}

	if libs, err := q.ListLibraryAccessByUser(ctx, victim); err != nil {
		t.Fatalf("ListLibraryAccessByUser victim: %v", err)
	} else if len(libs) != 0 {
		t.Errorf("victim still has %d grants", len(libs))
	}
	if libs, err := q.ListLibraryAccessByUser(ctx, bystander); err != nil {
		t.Fatalf("ListLibraryAccessByUser bystander: %v", err)
	} else if len(libs) != 1 {
		t.Errorf("bystander grants were wrongly wiped: got %d", len(libs))
	}
}
