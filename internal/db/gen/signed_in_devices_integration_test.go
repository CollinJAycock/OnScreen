//go:build integration

// Integration coverage for the signed-in devices queries (per-sign-in
// revocation) and the per-user internet streaming cap (migration 00046).
//
// Run with: go test -tags=integration ./internal/db/gen/...
package gen_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestSignedInDevices_Integration(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	alice := seedUser(ctx, t, q, "dev-a-"+uuid.New().String()[:8])
	bob := seedUser(ctx, t, q, "dev-b-"+uuid.New().String()[:8])
	ip := netip.MustParseAddr("203.0.113.5")
	name, plat := "Chrome on Windows", "web"
	here, err := q.CreateSession(ctx, gen.CreateSessionParams{
		UserID: alice, TokenHash: "h-" + uuid.NewString(), ClientName: &name, Platform: &plat, IpAddr: &ip,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	tv := mustCreateSession(ctx, t, q, alice, "h-"+uuid.NewString(), time.Now().Add(time.Hour))
	phone := mustCreateSession(ctx, t, q, alice, "h-"+uuid.NewString(), time.Now().Add(time.Hour))
	_ = mustCreateSession(ctx, t, q, alice, "h-"+uuid.NewString(), time.Now().Add(-time.Minute)) // expired
	bobs := mustCreateSession(ctx, t, q, bob, "h-"+uuid.NewString(), time.Now().Add(time.Hour))

	rows, err := q.ListActiveSessionsForUser(ctx, alice)
	if err != nil {
		t.Fatalf("ListActiveSessionsForUser: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("listed %d sessions, want 3 (expired excluded)", len(rows))
	}

	// The session check the auth middleware runs.
	st, err := q.GetSessionState(ctx, gen.GetSessionStateParams{UserID: alice, SessionID: tv.ID})
	if err != nil || !st.SessionLive {
		t.Fatalf("live session: %+v, %v", st, err)
	}
	// Another user's session ID never reads as live for this user.
	if st, _ := q.GetSessionState(ctx, gen.GetSessionStateParams{UserID: alice, SessionID: bobs.ID}); st.SessionLive {
		t.Error("bob's session reads as live for alice")
	}
	if _, err := q.GetSessionState(ctx, gen.GetSessionStateParams{UserID: uuid.New(), SessionID: tv.ID}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("unknown user: err = %v, want ErrNoRows", err)
	}

	// Single revoke is scoped to the owner.
	if _, err := q.DeleteUserSession(ctx, gen.DeleteUserSessionParams{ID: bobs.ID, UserID: alice}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("cross-user delete: err = %v, want ErrNoRows", err)
	}
	if st, _ := q.GetSessionState(ctx, gen.GetSessionStateParams{UserID: bob, SessionID: bobs.ID}); !st.SessionLive {
		t.Error("cross-user delete removed bob's session")
	}
	del, err := q.DeleteUserSession(ctx, gen.DeleteUserSessionParams{ID: tv.ID, UserID: alice})
	if err != nil || del.ID != tv.ID {
		t.Fatalf("DeleteUserSession: %+v, %v", del, err)
	}
	if st, _ := q.GetSessionState(ctx, gen.GetSessionStateParams{UserID: alice, SessionID: tv.ID}); st.SessionLive {
		t.Error("revoked session still live")
	}

	// Refresh re-labels; NULLs keep the stored values.
	newIP := netip.MustParseAddr("198.51.100.9")
	if err := q.UpdateSessionClient(ctx, gen.UpdateSessionClientParams{ID: here.ID, IpAddr: &newIP}); err != nil {
		t.Fatalf("UpdateSessionClient: %v", err)
	}
	rows, _ = q.ListActiveSessionsForUser(ctx, alice)
	for _, r := range rows {
		if r.ID != here.ID {
			continue
		}
		if r.IpAddr == nil || *r.IpAddr != newIP {
			t.Errorf("ip = %v, want %v", r.IpAddr, newIP)
		}
		if r.ClientName == nil || *r.ClientName != name || r.Platform == nil || *r.Platform != plat {
			t.Errorf("label lost on refresh: %v / %v", r.ClientName, r.Platform)
		}
	}

	// Sign out everywhere else keeps only the current session.
	n, err := q.DeleteOtherSessionsForUser(ctx, gen.DeleteOtherSessionsForUserParams{UserID: alice, ID: here.ID})
	if err != nil {
		t.Fatalf("DeleteOtherSessionsForUser: %v", err)
	}
	if n < 1 {
		t.Errorf("deleted %d, want at least the phone", n)
	}
	if st, _ := q.GetSessionState(ctx, gen.GetSessionStateParams{UserID: alice, SessionID: phone.ID}); st.SessionLive {
		t.Error("phone survived sign-out-everywhere-else")
	}
	if st, _ := q.GetSessionState(ctx, gen.GetSessionStateParams{UserID: alice, SessionID: here.ID}); !st.SessionLive {
		t.Error("current session was signed out")
	}
	if st, _ := q.GetSessionState(ctx, gen.GetSessionStateParams{UserID: bob, SessionID: bobs.ID}); !st.SessionLive {
		t.Error("another user's session was signed out")
	}
}

func TestRemoteStreamCap_Integration(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	uid := seedUser(ctx, t, q, "cap-"+uuid.New().String()[:8])
	i32 := func(v int32) *int32 { return &v }

	if err := q.UpdateUserStreamCaps(ctx, gen.UpdateUserStreamCapsParams{
		ID: uid, MaxStreamBitrateKbps: i32(20000), SetRemote: true, MaxRemoteStreamBitrateKbps: i32(6000),
	}); err != nil {
		t.Fatalf("UpdateUserStreamCaps: %v", err)
	}
	// A write that doesn't set the remote cap leaves it alone.
	if err := q.UpdateUserStreamCaps(ctx, gen.UpdateUserStreamCapsParams{
		ID: uid, MaxStreamBitrateKbps: i32(15000),
	}); err != nil {
		t.Fatalf("UpdateUserStreamCaps: %v", err)
	}
	row, err := q.GetUserStreamCaps(ctx, uid)
	if err != nil {
		t.Fatalf("GetUserStreamCaps: %v", err)
	}
	if row.MaxStreamBitrateKbps == nil || *row.MaxStreamBitrateKbps != 15000 {
		t.Errorf("bitrate cap = %v", row.MaxStreamBitrateKbps)
	}
	if row.MaxRemoteStreamBitrateKbps == nil || *row.MaxRemoteStreamBitrateKbps != 6000 {
		t.Errorf("internet cap = %v, want 6000 kept", row.MaxRemoteStreamBitrateKbps)
	}
	// Explicitly clearing it.
	if err := q.UpdateUserStreamCaps(ctx, gen.UpdateUserStreamCapsParams{ID: uid, SetRemote: true}); err != nil {
		t.Fatalf("UpdateUserStreamCaps: %v", err)
	}
	if row, _ = q.GetUserStreamCaps(ctx, uid); row.MaxRemoteStreamBitrateKbps != nil {
		t.Errorf("internet cap = %v, want cleared", *row.MaxRemoteStreamBitrateKbps)
	}
}
