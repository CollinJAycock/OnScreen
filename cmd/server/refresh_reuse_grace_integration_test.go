//go:build integration

// Refresh-token reuse grace window against a real Postgres: the window is
// measured from the last_seen that RotateSessionConditional stamps, so this
// checks the real queries feed it. Inside the window a spent token is refused
// without touching the user's other sign-ins; two refreshes of one token at
// once end with one winner and one refusal, not a wipe; outside the window
// the wipe still happens.
//
// The service compares its own clock with the database's NOW(), so the
// container's clock must be within the window of the host's (Docker keeps
// them in step).
//
// Run with: go test -tags=integration ./cmd/server/...
package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestRefreshReuseGrace_Integration(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	tm, err := auth.NewTokenMaker(auth.DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatalf("NewTokenMaker: %v", err)
	}
	var skew time.Duration // added to the service clock
	svc := &authService{db: q, tokens: tm, logger: slog.Default(),
		now: func() time.Time { return time.Now().Add(skew) }}

	hash, _ := bcrypt.GenerateFromPassword([]byte("pw-123456"), bcrypt.MinCost)
	hs := string(hash)
	user, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "grace-user", PasswordHash: &hs})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	login := func() *v1.TokenPair {
		t.Helper()
		p, err := svc.LoginLocal(ctx, "grace-user", "pw-123456")
		if err != nil {
			t.Fatalf("LoginLocal: %v", err)
		}
		return p
	}
	sessions := func() int {
		t.Helper()
		rows, err := q.ListActiveSessionsForUser(ctx, user.ID)
		if err != nil {
			t.Fatalf("ListActiveSessionsForUser: %v", err)
		}
		return len(rows)
	}
	epoch := func() int64 {
		t.Helper()
		u, err := q.GetUser(ctx, user.ID)
		if err != nil {
			t.Fatalf("GetUser: %v", err)
		}
		return int64(u.SessionEpoch)
	}

	phone := login()
	tv := login()
	startEpoch := epoch()

	// 1. A spent token presented right after its rotation: refused, nothing else touched.
	rotated, err := svc.Refresh(ctx, tv.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	_, err = svc.Refresh(ctx, tv.RefreshToken)
	var tol *v1.RefreshReuseToleratedError
	if !errors.As(err, &tol) || tol.Trigger != v1.RefreshReuseSuperseded {
		t.Fatalf("spent token inside the window: err = %v, want tolerated superseded_token", err)
	}
	if !errors.Is(err, v1.ErrRefreshInvalid) {
		t.Errorf("tolerated reuse is not ErrRefreshInvalid (the handler's 401): %v", err)
	}
	if n, e := sessions(), epoch(); n != 2 || e != startEpoch {
		t.Fatalf("after tolerated reuse: %d sessions, epoch %d -> %d; want 2 and unchanged", n, startEpoch, e)
	}
	// The holder of the new token carries on.
	if rotated, err = svc.Refresh(ctx, rotated.RefreshToken); err != nil {
		t.Fatalf("refresh with the rotated token after a tolerated reuse: %v", err)
	}
	if _, err := svc.Refresh(ctx, phone.RefreshToken); err != nil {
		t.Fatalf("the other device's refresh after a tolerated reuse: %v", err)
	}

	// 2. Two refreshes of one token at once: one wins, the other is refused —
	// by either reuse check, depending on timing — and nobody is signed out.
	results := make([]error, 2)
	pairs := make([]*v1.TokenPair, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			pairs[i], results[i] = svc.Refresh(ctx, rotated.RefreshToken)
		}(i)
	}
	close(start)
	wg.Wait()
	var winner *v1.TokenPair
	refused := 0
	for i, err := range results {
		switch {
		case err == nil:
			winner = pairs[i]
		case errors.As(err, &tol):
			refused++
		default:
			t.Fatalf("concurrent refresh %d: %v, want success or tolerated reuse", i, err)
		}
	}
	if winner == nil || refused != 1 {
		t.Fatalf("concurrent refreshes: winner=%v refused=%d, want one of each", winner != nil, refused)
	}
	if n, e := sessions(), epoch(); n != 2 || e != startEpoch {
		t.Fatalf("after a refresh race: %d sessions, epoch %d -> %d; want 2 and unchanged", n, startEpoch, e)
	}

	// 3. The token the race spent, presented after the window: the full reuse
	// response — every sign-in gone, epoch bumped.
	skew = refreshReuseGrace + time.Minute
	_, err = svc.Refresh(ctx, rotated.RefreshToken)
	var reuse *v1.RefreshReuseError
	if !errors.As(err, &reuse) || !reuse.SessionsDeleted || !reuse.EpochBumped {
		t.Fatalf("spent token after the window: err = %v, want a completed reuse wipe", err)
	}
	if n, e := sessions(), epoch(); n != 0 || e == startEpoch {
		t.Fatalf("after reuse outside the window: %d sessions, epoch %d -> %d; want 0 and bumped", n, startEpoch, e)
	}
	if _, err := svc.Refresh(ctx, winner.RefreshToken); !errors.Is(err, v1.ErrRefreshInvalid) {
		t.Errorf("the race winner's token after the wipe: %v, want ErrRefreshInvalid", err)
	}
}
