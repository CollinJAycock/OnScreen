//go:build integration

// End-to-end per-device sign-in against a real Postgres: login labels the
// session row with the device and trusted IP and stamps its ID into the
// tokens (sid), refresh keeps the same sid and re-labels the row, and
// deleting that one row makes the auth middleware's session check reject the
// device's tokens while the user's other sign-in keeps working.
//
// Run with: go test -tags=integration ./cmd/server/...
package main

import (
	"context"
	"log/slog"
	"net/netip"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestSignedInDevices_Integration_LoginRefreshRevoke(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	tm, err := auth.NewTokenMaker(auth.DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatalf("NewTokenMaker: %v", err)
	}
	svc := &authService{db: q, tokens: tm, logger: slog.Default()}
	epochs := &sessionEpochAdapter{q: q}

	hash, _ := bcrypt.GenerateFromPassword([]byte("pw-123456"), bcrypt.MinCost)
	hs := string(hash)
	user, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "devices-user", PasswordHash: &hs})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	tvIP := netip.MustParseAddr("203.0.113.40")
	tvCtx := v1.WithSessionClient(ctx, v1.SessionClient{ClientName: "Roku", Platform: "roku", IP: &tvIP})
	tv, err := svc.LoginLocal(tvCtx, "devices-user", "pw-123456")
	if err != nil {
		t.Fatalf("LoginLocal (tv): %v", err)
	}
	phone, err := svc.LoginLocal(ctx, "devices-user", "pw-123456")
	if err != nil {
		t.Fatalf("LoginLocal (phone): %v", err)
	}

	tvClaims, err := tm.ValidateAccessToken(tv.AccessToken)
	if err != nil {
		t.Fatalf("validate tv token: %v", err)
	}
	assetClaims, _ := tm.ValidateAccessToken(tv.AssetToken)
	if tvClaims.SessionID == uuid.Nil || assetClaims.SessionID != tvClaims.SessionID {
		t.Fatalf("tokens lack a shared sid: access %v asset %v", tvClaims.SessionID, assetClaims.SessionID)
	}

	rows, err := q.ListActiveSessionsForUser(ctx, user.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListActiveSessionsForUser: %d rows, %v", len(rows), err)
	}
	for _, r := range rows {
		if r.ID != tvClaims.SessionID {
			continue
		}
		if r.ClientName == nil || *r.ClientName != "Roku" || r.IpAddr == nil || *r.IpAddr != tvIP {
			t.Errorf("tv session not labelled: name=%v ip=%v", r.ClientName, r.IpAddr)
		}
	}

	// Refresh from a new address keeps the sid and moves the recorded IP.
	newIP := netip.MustParseAddr("198.51.100.41")
	refreshed, err := svc.Refresh(v1.WithSessionClient(ctx, v1.SessionClient{IP: &newIP}), tv.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	rc, _ := tm.ValidateAccessToken(refreshed.AccessToken)
	if rc.SessionID != tvClaims.SessionID {
		t.Fatalf("refresh changed the sid: %v -> %v", tvClaims.SessionID, rc.SessionID)
	}
	rows, _ = q.ListActiveSessionsForUser(ctx, user.ID)
	for _, r := range rows {
		if r.ID == tvClaims.SessionID {
			if r.IpAddr == nil || *r.IpAddr != newIP {
				t.Errorf("refresh didn't record the new IP: %v", r.IpAddr)
			}
			if r.ClientName == nil || *r.ClientName != "Roku" {
				t.Errorf("refresh with no label erased it: %v", r.ClientName)
			}
		}
	}

	// Revoke the TV alone: its tokens fail the session check, the phone's pass.
	if _, err := q.DeleteUserSession(ctx, gen.DeleteUserSessionParams{ID: tvClaims.SessionID, UserID: user.ID}); err != nil {
		t.Fatalf("DeleteUserSession: %v", err)
	}
	if _, live, err := epochs.GetSessionState(ctx, user.ID, rc.SessionID); err != nil || live {
		t.Errorf("revoked tv session: live=%v err=%v", live, err)
	}
	pc, _ := tm.ValidateAccessToken(phone.AccessToken)
	if epoch, live, err := epochs.GetSessionState(ctx, user.ID, pc.SessionID); err != nil || !live || epoch != pc.SessionEpoch {
		t.Errorf("phone session after tv revoke: live=%v epoch=%d err=%v", live, epoch, err)
	}
	if _, err := svc.Refresh(ctx, refreshed.RefreshToken); err == nil {
		t.Error("revoked tv session could still refresh")
	}
}
