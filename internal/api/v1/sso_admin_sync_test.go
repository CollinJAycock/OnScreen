package v1

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/db/gen"
)

type syncDB struct {
	users    map[uuid.UUID]gen.User
	setCalls []gen.SetUserAdminParams
	bumps    []uuid.UUID
	deletes  []uuid.UUID
	setErr   error
	getErr   error
	getCalls int
}

func (d *syncDB) SetUserAdmin(_ context.Context, arg gen.SetUserAdminParams) error {
	if d.setErr != nil {
		return d.setErr
	}
	d.setCalls = append(d.setCalls, arg)
	u := d.users[arg.ID]
	u.IsAdmin = arg.IsAdmin
	d.users[arg.ID] = u
	return nil
}
func (d *syncDB) BumpSessionEpoch(_ context.Context, id uuid.UUID) error {
	d.bumps = append(d.bumps, id)
	u := d.users[id]
	u.SessionEpoch++
	d.users[id] = u
	return nil
}
func (d *syncDB) DeleteSessionsForUser(_ context.Context, id uuid.UUID) error {
	d.deletes = append(d.deletes, id)
	return nil
}
func (d *syncDB) GetUser(_ context.Context, id uuid.UUID) (gen.User, error) {
	d.getCalls++
	if d.getErr != nil {
		return gen.User{}, d.getErr
	}
	if u, ok := d.users[id]; ok {
		return u, nil
	}
	return gen.User{}, pgx.ErrNoRows
}

func newSyncDB(u gen.User) *syncDB {
	return &syncDB{users: map[uuid.UUID]gen.User{u.ID: u}}
}

// The defect all three providers shared: SetUserAdmin wrote is_admin=false to
// the DB, then the caller minted the token from the SAME struct it had read
// BEFORE the write. issueTokenPair takes IsAdmin straight off that struct, and
// AdminRequired trusts the claim with no DB read — so the login that was meant
// to demote the user handed them a fresh admin credential for the full TTL.
func TestSyncAdminFromIdP_ReturnsDemotedUser(t *testing.T) {
	id := uuid.New()
	db := newSyncDB(gen.User{ID: id, IsAdmin: true, SessionEpoch: 4})

	got := syncAdminFromIdP(context.Background(), db, slog.Default(),
		gen.User{ID: id, IsAdmin: true, SessionEpoch: 4}, false, true, "test")

	if got.IsAdmin {
		t.Fatal("returned user still says IsAdmin — the token minted from it " +
			"grants admin the IdP just revoked")
	}
	if len(db.setCalls) != 1 || db.setCalls[0].IsAdmin {
		t.Errorf("SetUserAdmin calls = %+v, want one call with IsAdmin=false", db.setCalls)
	}
}

// A demotion must also revoke credentials already outstanding, which is the
// invariant the admin UI enforces at PUT /users/{id}/admin.
func TestSyncAdminFromIdP_RevokesOutstandingCredentials(t *testing.T) {
	id := uuid.New()
	db := newSyncDB(gen.User{ID: id, IsAdmin: true, SessionEpoch: 4})

	got := syncAdminFromIdP(context.Background(), db, slog.Default(),
		gen.User{ID: id, IsAdmin: true, SessionEpoch: 4}, false, true, "test")

	if len(db.bumps) != 1 {
		t.Errorf("session epoch bumps = %d, want 1 — tokens minted under the old "+
			"role stay valid for their full TTL", len(db.bumps))
	}
	if len(db.deletes) != 1 {
		t.Errorf("session deletions = %d, want 1 — refresh sessions survive the demotion",
			len(db.deletes))
	}
	// The returned user must carry the NEW epoch, or the token we are about to
	// mint is invalidated by the bump we just performed.
	if got.SessionEpoch != 5 {
		t.Errorf("returned SessionEpoch = %d, want 5 — the fresh token would be "+
			"rejected by its own revocation", got.SessionEpoch)
	}
}

// Promotion is the same machinery in the other direction.
func TestSyncAdminFromIdP_Promotes(t *testing.T) {
	id := uuid.New()
	db := newSyncDB(gen.User{ID: id, IsAdmin: false, SessionEpoch: 1})

	got := syncAdminFromIdP(context.Background(), db, slog.Default(),
		gen.User{ID: id, IsAdmin: false, SessionEpoch: 1}, true, true, "test")

	if !got.IsAdmin {
		t.Error("promotion not reflected in the returned user")
	}
}

// No group mapping configured, or no change: touch nothing. Admins promoted
// through the UI must not be demoted by an IdP that does not drive the flag.
func TestSyncAdminFromIdP_NoOpCases(t *testing.T) {
	cases := []struct {
		name          string
		isAdmin, want bool
		groupSync     bool
	}{
		{"group sync disabled", true, false, false},
		{"already correct", true, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := uuid.New()
			db := newSyncDB(gen.User{ID: id, IsAdmin: c.isAdmin, SessionEpoch: 1})
			got := syncAdminFromIdP(context.Background(), db, slog.Default(),
				gen.User{ID: id, IsAdmin: c.isAdmin, SessionEpoch: 1}, c.want, c.groupSync, "test")

			if len(db.setCalls) != 0 || len(db.bumps) != 0 || len(db.deletes) != 0 {
				t.Errorf("no-op case wrote: set=%d bump=%d del=%d",
					len(db.setCalls), len(db.bumps), len(db.deletes))
			}
			if got.IsAdmin != c.isAdmin {
				t.Errorf("admin flag changed on a no-op: got %v", got.IsAdmin)
			}
		})
	}
}

// If the re-read fails we still must not hand back a struct asserting an admin
// role the database just revoked.
func TestSyncAdminFromIdP_ReReadFailureStillDemotes(t *testing.T) {
	id := uuid.New()
	db := newSyncDB(gen.User{ID: id, IsAdmin: true, SessionEpoch: 4})
	db.getErr = errors.New("db blip")

	got := syncAdminFromIdP(context.Background(), db, slog.Default(),
		gen.User{ID: id, IsAdmin: true, SessionEpoch: 4}, false, true, "test")

	if got.IsAdmin {
		t.Error("re-read failed and the fallback still asserts admin")
	}
}

// A failed write must not be reported as a successful demotion by silently
// flipping the in-memory flag — the DB is still authoritative and still says
// admin, so the caller should see that.
func TestSyncAdminFromIdP_WriteFailureLeavesUserUnchanged(t *testing.T) {
	id := uuid.New()
	db := newSyncDB(gen.User{ID: id, IsAdmin: true, SessionEpoch: 4})
	db.setErr = errors.New("write failed")

	got := syncAdminFromIdP(context.Background(), db, slog.Default(),
		gen.User{ID: id, IsAdmin: true, SessionEpoch: 4}, false, true, "test")

	if !got.IsAdmin {
		t.Error("write failed but the returned user was demoted anyway — the token " +
			"would disagree with the database")
	}
	if len(db.bumps) != 0 {
		t.Error("credentials revoked despite the admin write failing")
	}
}
