//go:build integration

// Round-trips the queries behind the 2026-11 accounts / auth hardening: the
// last-admin guards (SetUserAdminGuarded, DeleteUserGuarded) including the
// race they close, the forgot-password write (ResetUserLocalPassword), API
// key revocation, the managed-profile household rules (request permissions,
// quota usage, stream-limit membership) and the DVR's disabled-channel gate.
//
// Run with: go test -tags 'dev integration' -p 1 -run Hardening ./internal/db/gen/
package gen_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func seedAdmin(ctx context.Context, t *testing.T, q *gen.Queries, name string) uuid.UUID {
	t.Helper()
	hash := "placeholder-bcrypt-hash"
	u, err := q.CreateUser(ctx, gen.CreateUserParams{Username: name, PasswordHash: &hash, IsAdmin: true})
	if err != nil {
		t.Fatalf("CreateUser %q: %v", name, err)
	}
	return u.ID
}

func countAdmins(ctx context.Context, t *testing.T, q *gen.Queries) int64 {
	t.Helper()
	n, err := q.CountAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHardening_Integration_AdminGuards(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	a := seedAdmin(ctx, t, q, "guard_a")
	b := seedAdmin(ctx, t, q, "guard_b")
	c := seedUser(ctx, t, q, "guard_c")
	if _, err := q.CreateSession(ctx, gen.CreateSessionParams{
		UserID: b, TokenHash: "tok-b",
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}

	// Demoting a non-admin is always fine.
	if res, err := q.SetUserAdminGuarded(ctx, gen.SetUserAdminGuardedParams{ID: c, IsAdmin: false}); err != nil || !res.UserExists || !res.Updated {
		t.Fatalf("demote non-admin: %+v, %v", res, err)
	}

	// Demoting B (A remains) succeeds and, in the same statement, bumps B's
	// epoch and deletes B's sign-ins.
	epoch0, _ := q.GetSessionEpoch(ctx, b)
	res, err := q.SetUserAdminGuarded(ctx, gen.SetUserAdminGuardedParams{ID: b, IsAdmin: false})
	if err != nil || !res.Updated {
		t.Fatalf("demote b: %+v, %v", res, err)
	}
	if epoch1, _ := q.GetSessionEpoch(ctx, b); epoch1 != epoch0+1 {
		t.Errorf("b's epoch %d → %d, want one bump", epoch0, epoch1)
	}
	if rows, _ := q.ListActiveSessionsForUser(ctx, b); len(rows) != 0 {
		t.Errorf("b still has %d sign-ins after the role change", len(rows))
	}

	// A is now the last admin: refused, and nothing about A changes.
	epochA, _ := q.GetSessionEpoch(ctx, a)
	if res, err := q.SetUserAdminGuarded(ctx, gen.SetUserAdminGuardedParams{ID: a, IsAdmin: false}); err != nil || !res.UserExists || res.Updated {
		t.Fatalf("demote last admin: %+v, %v (want refused)", res, err)
	}
	if e, _ := q.GetSessionEpoch(ctx, a); e != epochA {
		t.Error("a refused demotion still bumped the epoch")
	}
	if res, err := q.DeleteUserGuarded(ctx, a); err != nil || !res.UserExists || res.Deleted {
		t.Fatalf("delete last admin: %+v, %v (want refused)", res, err)
	}
	if countAdmins(ctx, t, q) != 1 {
		t.Fatal("the last admin was removed")
	}

	// Promotion is never refused; unknown ids report user_exists=false.
	if res, err := q.SetUserAdminGuarded(ctx, gen.SetUserAdminGuardedParams{ID: b, IsAdmin: true}); err != nil || !res.Updated {
		t.Fatalf("promote b: %+v, %v", res, err)
	}
	if res, err := q.SetUserAdminGuarded(ctx, gen.SetUserAdminGuardedParams{ID: uuid.New(), IsAdmin: true}); err != nil || res.UserExists || res.Updated {
		t.Fatalf("unknown user role change: %+v, %v", res, err)
	}
	if res, err := q.DeleteUserGuarded(ctx, uuid.New()); err != nil || res.UserExists || res.Deleted {
		t.Fatalf("unknown user delete: %+v, %v", res, err)
	}
	if res, err := q.DeleteUserGuarded(ctx, c); err != nil || !res.Deleted {
		t.Fatalf("delete non-admin: %+v, %v", res, err)
	}

	// The race the guard closes: two admins demoting (or deleting) each other
	// at the same moment. Under a count-then-write both passed the count and
	// the server was left with no admin.
	race := func(round int, op func(id uuid.UUID) error, x, y uuid.UUID) {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, id := range []uuid.UUID{x, y} {
			wg.Add(1)
			go func(id uuid.UUID) {
				defer wg.Done()
				<-start
				if err := op(id); err != nil {
					t.Errorf("round %d: %v", round, err)
				}
			}(id)
		}
		close(start)
		wg.Wait()
		if n := countAdmins(ctx, t, q); n < 1 {
			t.Fatalf("round %d: %d admins left", round, n)
		}
	}
	demote := func(id uuid.UUID) error {
		_, err := q.SetUserAdminGuarded(ctx, gen.SetUserAdminGuardedParams{ID: id, IsAdmin: false})
		return err
	}
	del := func(id uuid.UUID) error {
		_, err := q.DeleteUserGuarded(ctx, id)
		return err
	}
	for i := 0; i < 15; i++ {
		// Back to exactly two admins, a and b.
		for _, id := range []uuid.UUID{a, b} {
			if _, err := q.SetUserAdminGuarded(ctx, gen.SetUserAdminGuardedParams{ID: id, IsAdmin: true}); err != nil {
				t.Fatal(err)
			}
		}
		race(i, demote, a, b)
	}
	for i := 0; i < 10; i++ {
		// Make a fresh pair the only admins, then have them delete each other.
		x := seedAdmin(ctx, t, q, "race_x_"+uuid.NewString()[:8])
		y := seedAdmin(ctx, t, q, "race_y_"+uuid.NewString()[:8])
		for _, id := range []uuid.UUID{a, b} {
			if err := q.SetUserAdmin(ctx, gen.SetUserAdminParams{ID: id, IsAdmin: false}); err != nil {
				t.Fatal(err)
			}
		}
		race(i, del, x, y)
		// Back to a alone, so the next pair is again the only two admins.
		if err := q.SetUserAdmin(ctx, gen.SetUserAdminParams{ID: a, IsAdmin: true}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []uuid.UUID{x, y} {
			if err := q.SetUserAdmin(ctx, gen.SetUserAdminParams{ID: id, IsAdmin: false}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestHardening_Integration_AccountsAndHouseholds(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	// ── ResetUserLocalPassword: only accounts with a local password ──────────
	local := seedUser(ctx, t, q, "local_user")
	iss, sub := "https://idp.example", "sub-1"
	sso, err := q.CreateOIDCUser(ctx, gen.CreateOIDCUserParams{Username: "sso_user", OidcIssuer: &iss, OidcSubject: &sub})
	if err != nil {
		t.Fatal(err)
	}
	newHash := "new-hash"
	for _, tc := range []struct {
		name string
		id   uuid.UUID
		want int64
	}{{"local account", local, 1}, {"single sign-on account", sso.ID, 0}} {
		n, err := q.ResetUserLocalPassword(ctx, gen.ResetUserLocalPasswordParams{ID: tc.id, PasswordHash: &newHash})
		if err != nil || n != tc.want {
			t.Errorf("%s: rows %d, %v; want %d", tc.name, n, err, tc.want)
		}
	}
	if u, _ := q.GetUser(ctx, sso.ID); u.PasswordHash != nil {
		t.Error("the SSO account was given a local password")
	}

	// ── DeleteAPIKeysForUser ────────────────────────────────────────────────
	for i, h := range []string{"hash-1", "hash-2"} {
		if _, err := q.CreateAPIKey(ctx, gen.CreateAPIKeyParams{
			UserID: local, Name: "k", KeyHash: h, KeyPrefix: "osk_abcd", Scope: "full",
		}); err != nil {
			t.Fatalf("key %d: %v", i, err)
		}
	}
	other := seedUser(ctx, t, q, "other_user")
	if _, err := q.CreateAPIKey(ctx, gen.CreateAPIKeyParams{UserID: other, Name: "k", KeyHash: "hash-3", KeyPrefix: "osk_efgh", Scope: "full"}); err != nil {
		t.Fatal(err)
	}
	if n, err := q.DeleteAPIKeysForUser(ctx, local); err != nil || n != 2 {
		t.Errorf("DeleteAPIKeysForUser: %d, %v; want 2", n, err)
	}
	if _, err := q.GetAPIKeyAuth(ctx, "hash-1"); err == nil {
		t.Error("a revoked key still authenticates")
	}
	if _, err := q.GetAPIKeyAuth(ctx, "hash-3"); err != nil {
		t.Errorf("another user's key was revoked: %v", err)
	}

	// ── Households: owner + managed profiles ────────────────────────────────
	owner := seedUser(ctx, t, q, "house_owner")
	ownerPG := pgtype.UUID{Bytes: owner, Valid: true}
	kid, err := q.CreateManagedProfile(ctx, gen.CreateManagedProfileParams{Username: "house_kid", ParentUserID: ownerPG})
	if err != nil {
		t.Fatal(err)
	}
	teen, err := q.CreateManagedProfile(ctx, gen.CreateManagedProfileParams{Username: "house_teen", ParentUserID: ownerPG})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := q.ResetUserLocalPassword(ctx, gen.ResetUserLocalPasswordParams{ID: kid.ID, PasswordHash: &newHash}); err != nil || n != 0 {
		t.Errorf("managed profile password reset: rows %d, %v; want 0", n, err)
	}

	members, err := q.ListHouseholdMemberIDs(ctx, kid.ID)
	if err != nil || !sameSet(members, owner, kid.ID, teen.ID) {
		t.Errorf("household of the kid: %v, %v", members, err)
	}
	if members, _ := q.ListHouseholdMemberIDs(ctx, owner); !sameSet(members, owner, kid.ID, teen.ID) {
		t.Errorf("household of the owner: %v", members)
	}
	if members, _ := q.ListHouseholdMemberIDs(ctx, other); !sameSet(members, other) {
		t.Errorf("household of an account without profiles: %v", members)
	}
	if members, _ := q.ListHouseholdMemberIDs(ctx, uuid.New()); len(members) != 0 {
		t.Errorf("household of an unknown id: %v", members)
	}

	// can_request: the profile's own flag AND its owner's.
	no, yes := false, true
	setPerm := func(id uuid.UUID, p gen.SetUserRequestPermissionsParams) {
		t.Helper()
		p.ID = id
		if _, err := q.SetUserRequestPermissions(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	setPerm(owner, gen.SetUserRequestPermissionsParams{CanRequest: &no})
	if p, _ := q.GetUserRequestPermissions(ctx, kid.ID); p.CanRequest {
		t.Error("a profile of an owner who may not request can request")
	}
	setPerm(owner, gen.SetUserRequestPermissionsParams{CanRequest: &yes})
	if p, _ := q.GetUserRequestPermissions(ctx, kid.ID); !p.CanRequest {
		t.Error("a profile of an allowed owner was blocked")
	}
	setPerm(kid.ID, gen.SetUserRequestPermissionsParams{CanRequest: &no})
	if p, _ := q.GetUserRequestPermissions(ctx, kid.ID); p.CanRequest {
		t.Error("the profile's own block was lost")
	}
	setPerm(kid.ID, gen.SetUserRequestPermissionsParams{CanRequest: &yes})

	// Quotas: a profile without its own falls back to its owner's.
	two, five := int32(2), int32(5)
	setPerm(owner, gen.SetUserRequestPermissionsParams{SetQuotaMovies: true, RequestQuotaMovies: &two})
	if p, _ := q.GetUserRequestPermissions(ctx, kid.ID); p.RequestQuotaMovies == nil || *p.RequestQuotaMovies != 2 {
		t.Errorf("profile quota = %v, want the owner's 2", p.RequestQuotaMovies)
	}
	setPerm(teen.ID, gen.SetUserRequestPermissionsParams{SetQuotaMovies: true, RequestQuotaMovies: &five})
	if p, _ := q.GetUserRequestPermissions(ctx, teen.ID); p.RequestQuotaMovies == nil || *p.RequestQuotaMovies != 5 {
		t.Errorf("profile's own quota = %v, want 5", p.RequestQuotaMovies)
	}
	if p, _ := q.GetUserRequestPermissions(ctx, other); p.RequestQuotaMovies != nil {
		t.Errorf("an unrelated account picked up a quota: %v", *p.RequestQuotaMovies)
	}

	// Usage and the pending count are the household's.
	request := func(user uuid.UUID, tmdb int32, status string) {
		t.Helper()
		if _, err := q.CreateMediaRequest(ctx, gen.CreateMediaRequestParams{
			UserID: user, Type: "movie", TmdbID: tmdb, Title: "t", Status: status,
		}); err != nil {
			t.Fatal(err)
		}
	}
	request(owner, 1, "pending")
	request(kid.ID, 2, "pending")
	request(teen.ID, 3, "approved")
	request(kid.ID, 4, "declined") // declined never counts
	request(other, 5, "pending")   // another household
	since := pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	for _, id := range []uuid.UUID{owner, kid.ID, teen.ID} {
		n, err := q.CountRecentMediaRequestsForUser(ctx, gen.CountRecentMediaRequestsForUserParams{UserID: id, Type: "movie", Since: since})
		if err != nil || n != 3 {
			t.Errorf("household usage seen from %s: %d, %v; want 3", id, n, err)
		}
		if p, _ := q.GetUserRequestPermissions(ctx, id); p.HouseholdPending != 2 {
			t.Errorf("household pending seen from %s: %d, want 2", id, p.HouseholdPending)
		}
	}
	if n, _ := q.CountRecentMediaRequestsForUser(ctx, gen.CountRecentMediaRequestsForUserParams{UserID: other, Type: "movie", Since: since}); n != 1 {
		t.Errorf("an account without profiles counts only itself: %d, want 1", n)
	}
}

func sameSet(got []uuid.UUID, want ...uuid.UUID) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[uuid.UUID]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	for _, id := range want {
		if !seen[id] {
			return false
		}
	}
	return true
}

func TestHardening_Integration_DVRDisabledChannel(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	viewer := seedUser(ctx, t, q, "dvr_viewer")
	admin := seedAdmin(ctx, t, q, "dvr_admin")
	chID := seedTunerChannel(t, q, "dvr-gate-tuner")
	start := time.Now().Add(-time.Minute) // due now
	end := time.Now().Add(time.Hour)
	prog := seedEPGProgram(t, pool, chID, "EP-gate", start, end)
	futureProg := seedEPGProgram(t, pool, chID, "EP-later", time.Now().Add(2*time.Hour), time.Now().Add(3*time.Hour))

	upsert := func(user, program uuid.UUID, s, e time.Time) gen.Recording {
		t.Helper()
		r, err := q.UpsertRecording(ctx, gen.UpsertRecordingParams{
			UserID: user, ChannelID: chID, ProgramID: pgtype.UUID{Bytes: program, Valid: true}, Title: "x",
			StartsAt: pgtype.Timestamptz{Time: s, Valid: true}, EndsAt: pgtype.Timestamptz{Time: e, Valid: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	viewerRec := upsert(viewer, prog, start, end)
	adminRec := upsert(admin, prog, start, end)
	viewerLater := upsert(viewer, futureProg, time.Now().Add(2*time.Hour), time.Now().Add(3*time.Hour))

	due := func() map[uuid.UUID]bool {
		t.Helper()
		rows, err := q.ListDueRecordings(ctx, pgtype.Timestamptz{Time: time.Now(), Valid: true})
		if err != nil {
			t.Fatal(err)
		}
		out := map[uuid.UUID]bool{}
		for _, r := range rows {
			out[r.ID] = true
		}
		return out
	}
	if d := due(); !d[viewerRec.ID] || !d[adminRec.ID] {
		t.Fatalf("enabled channel: due = %v", d)
	}
	enabled, err := q.ScheduleChannelsEnabled(ctx, gen.ScheduleChannelsEnabledParams{
		ChannelID: pgtype.UUID{Bytes: chID, Valid: true}, ProgramID: pgtype.UUID{Bytes: prog, Valid: true},
	})
	if err != nil || !enabled {
		t.Fatalf("ScheduleChannelsEnabled on an enabled channel: %v, %v", enabled, err)
	}

	if err := q.SetChannelEnabled(ctx, gen.SetChannelEnabledParams{ID: chID, Enabled: false}); err != nil {
		t.Fatal(err)
	}

	// The capture gate: the viewer's recording is held back, the admin's isn't.
	if d := due(); d[viewerRec.ID] || !d[adminRec.ID] {
		t.Errorf("disabled channel: viewer due=%v admin due=%v; want false/true", d[viewerRec.ID], d[adminRec.ID])
	}
	// Schedule creation sees the channel as unavailable, by id or by programme;
	// an unknown programme is not enabled either, and NULLs aren't checked.
	for name, p := range map[string]gen.ScheduleChannelsEnabledParams{
		"channel":         {ChannelID: pgtype.UUID{Bytes: chID, Valid: true}},
		"programme":       {ProgramID: pgtype.UUID{Bytes: prog, Valid: true}},
		"unknown program": {ProgramID: pgtype.UUID{Bytes: uuid.New(), Valid: true}},
	} {
		if ok, err := q.ScheduleChannelsEnabled(ctx, p); err != nil || ok {
			t.Errorf("%s on a disabled channel: %v, %v; want false", name, ok, err)
		}
	}
	if ok, _ := q.ScheduleChannelsEnabled(ctx, gen.ScheduleChannelsEnabledParams{}); !ok {
		t.Error("no channel and no programme should not be refused")
	}

	// The matcher's sweep fails the viewer's due recording with a reason and
	// leaves the admin's and the viewer's future one alone.
	n, err := q.FailDueRecordingsOnDisabledChannels(ctx)
	if err != nil || n != 1 {
		t.Fatalf("sweep: %d, %v; want 1", n, err)
	}
	if r, _ := q.GetRecording(ctx, viewerRec.ID); r.Status != "failed" || r.Error == nil || *r.Error != "channel disabled" {
		t.Errorf("viewer's due recording: status %q error %v", r.Status, r.Error)
	}
	for _, id := range []uuid.UUID{adminRec.ID, viewerLater.ID} {
		if r, _ := q.GetRecording(ctx, id); r.Status != "scheduled" {
			t.Errorf("recording %s: status %q, want scheduled", id, r.Status)
		}
	}
}
