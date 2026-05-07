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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

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
		IsPrivate:               true,
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

// TestLibraryAccess_Integration_ProfileInheritsParent proves the v2.1
// Track G item 3 inheritance default: a managed profile with
// inherit_library_access=true sees its parent's grants on private
// libraries even though the profile has zero rows of its own. This is
// the safe-by-default behaviour that prevents the "barren home page
// for kid profiles" footgun.
func TestLibraryAccess_Integration_ProfileInheritsParent(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	parent := seedUser(ctx, t, q, "acl-inh-p-"+uuid.New().String()[:8])

	// Manually create a managed profile (no helper, since the existing
	// CreateManagedProfile wraps a different Params type).
	var profile uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, parent_user_id, is_admin) VALUES ($1, $2, false) RETURNING id`,
		"acl-inh-c-"+uuid.New().String()[:8], parent,
	).Scan(&profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	priv := seedLibrary(ctx, t, q, "acl-inh-priv-"+uuid.New().String()[:8])
	if _, err := pool.Exec(ctx, `UPDATE libraries SET is_private = true WHERE id = $1`, priv); err != nil {
		t.Fatalf("mark private: %v", err)
	}

	// Grant ONLY the parent.
	if err := q.GrantLibraryAccess(ctx, gen.GrantLibraryAccessParams{
		UserID: parent, LibraryID: priv,
	}); err != nil {
		t.Fatalf("grant parent: %v", err)
	}

	// Profile inherits by default → must see the parent's grant.
	ok, err := q.HasLibraryAccess(ctx, gen.HasLibraryAccessParams{UserID: profile, LibraryID: priv})
	if err != nil {
		t.Fatalf("HasLibraryAccess profile: %v", err)
	}
	if !ok {
		t.Error("profile with inherit=true must see parent's private library grant")
	}

	// And ListAllowed for the profile must include the inherited library.
	ids, err := q.ListAllowedLibraryIDsForUser(ctx, profile)
	if err != nil {
		t.Fatalf("ListAllowed profile: %v", err)
	}
	found := false
	for _, id := range ids {
		if id == priv {
			found = true
		}
	}
	if !found {
		t.Errorf("ListAllowed missing inherited library; got %v", ids)
	}
}

// TestLibraryAccess_Integration_ProfileNarrowsWhenInheritOff proves
// the override side: when an admin flips inherit_library_access=false
// and grants a narrower set, the profile loses access to libraries
// the parent has but the profile doesn't — even on private libraries.
// This is the parental-control use case (kid sees Family Movies only
// even though parent has 4K Movies too).
func TestLibraryAccess_Integration_ProfileNarrowsWhenInheritOff(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	parent := seedUser(ctx, t, q, "acl-narr-p-"+uuid.New().String()[:8])
	var profile uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, parent_user_id, is_admin) VALUES ($1, $2, false) RETURNING id`,
		"acl-narr-c-"+uuid.New().String()[:8], parent,
	).Scan(&profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	family := seedLibrary(ctx, t, q, "acl-narr-fam-"+uuid.New().String()[:8])
	uhd := seedLibrary(ctx, t, q, "acl-narr-uhd-"+uuid.New().String()[:8])
	for _, lid := range []uuid.UUID{family, uhd} {
		if _, err := pool.Exec(ctx, `UPDATE libraries SET is_private = true WHERE id = $1`, lid); err != nil {
			t.Fatalf("mark private: %v", err)
		}
	}

	// Parent gets access to both.
	for _, lid := range []uuid.UUID{family, uhd} {
		if err := q.GrantLibraryAccess(ctx, gen.GrantLibraryAccessParams{
			UserID: parent, LibraryID: lid,
		}); err != nil {
			t.Fatalf("grant parent: %v", err)
		}
	}

	// Flip inheritance OFF for the profile and grant only Family.
	rows, err := q.SetProfileInheritLibraryAccess(ctx, gen.SetProfileInheritLibraryAccessParams{
		Inherit: false, ID: profile,
	})
	if err != nil || rows != 1 {
		t.Fatalf("SetProfileInheritLibraryAccess: rows=%d err=%v", rows, err)
	}
	if err := q.GrantLibraryAccess(ctx, gen.GrantLibraryAccessParams{
		UserID: profile, LibraryID: family,
	}); err != nil {
		t.Fatalf("grant profile family: %v", err)
	}

	// Profile must see Family (own grant) but NOT UHD (would only be
	// visible via inheritance, which is now off).
	if ok, err := q.HasLibraryAccess(ctx, gen.HasLibraryAccessParams{UserID: profile, LibraryID: family}); err != nil {
		t.Fatalf("HasLibraryAccess family: %v", err)
	} else if !ok {
		t.Error("profile with explicit grant on Family must see it")
	}
	if ok, err := q.HasLibraryAccess(ctx, gen.HasLibraryAccessParams{UserID: profile, LibraryID: uhd}); err != nil {
		t.Fatalf("HasLibraryAccess uhd: %v", err)
	} else if ok {
		t.Error("profile with inherit=false must NOT see parent's UHD grant — narrowing failed")
	}
}

// TestLibraryAccess_Integration_SetInheritOwnerGate proves the SQL's
// owner_id gate: passing a non-parent user_id as owner_id returns 0
// rows (which the handler maps to 404), preventing IDOR-style writes
// where one user could flip another user's profile inheritance.
func TestLibraryAccess_Integration_SetInheritOwnerGate(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	owner := seedUser(ctx, t, q, "acl-gate-o-"+uuid.New().String()[:8])
	stranger := seedUser(ctx, t, q, "acl-gate-s-"+uuid.New().String()[:8])
	var profile uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (username, parent_user_id, is_admin) VALUES ($1, $2, false) RETURNING id`,
		"acl-gate-c-"+uuid.New().String()[:8], owner,
	).Scan(&profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	// Stranger tries to flip the inherit flag — must affect 0 rows.
	strangerArg := pgUUID(stranger)
	rows, err := q.SetProfileInheritLibraryAccess(ctx, gen.SetProfileInheritLibraryAccessParams{
		Inherit: false, ID: profile, OwnerID: strangerArg,
	})
	if err != nil {
		t.Fatalf("SetProfileInheritLibraryAccess stranger: %v", err)
	}
	if rows != 0 {
		t.Errorf("stranger gate: rows=%d, want 0 — IDOR risk", rows)
	}

	// Real owner succeeds.
	ownerArg := pgUUID(owner)
	rows, err = q.SetProfileInheritLibraryAccess(ctx, gen.SetProfileInheritLibraryAccessParams{
		Inherit: false, ID: profile, OwnerID: ownerArg,
	})
	if err != nil {
		t.Fatalf("SetProfileInheritLibraryAccess owner: %v", err)
	}
	if rows != 1 {
		t.Errorf("owner gate: rows=%d, want 1", rows)
	}
}
