//go:build integration

// New-account media-request defaults against a real Postgres: the invite
// adapter and local registration read Settings ▸ Requests through the real
// settings service and write the toggles into the new users row; a later
// change to the defaults leaves existing accounts alone.
//
// Run with: go test -tags=integration ./cmd/server/ -run RequestDefaults
package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestRequestDefaults_Integration_InviteAndRegister(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := settings.New(pool, logger)

	invite := &inviteAdapter{q: q, reqDefaults: st}
	register := &authService{db: q, logger: logger, reqDefaults: st}

	check := func(label string, id uuid.UUID, movies, tv bool) {
		t.Helper()
		u, err := q.GetUser(ctx, id)
		if err != nil {
			t.Fatalf("%s: GetUser: %v", label, err)
		}
		if u.AutoApproveMovies != movies || u.AutoApproveTv != tv {
			t.Errorf("%s: movies=%v tv=%v, want %v/%v", label, u.AutoApproveMovies, u.AutoApproveTv, movies, tv)
		}
	}

	// Nothing stored yet: both off.
	id0, err := invite.CreateUser(ctx, "invitee-0", nil, "hash")
	if err != nil {
		t.Fatalf("invite CreateUser: %v", err)
	}
	check("invite, defaults unset", id0, false, false)

	if err := st.SetRequests(ctx, settings.RequestsConfig{DefaultAutoApproveMovies: true}); err != nil {
		t.Fatalf("SetRequests: %v", err)
	}
	id1, err := invite.CreateUser(ctx, "invitee-1", nil, "hash")
	if err != nil {
		t.Fatalf("invite CreateUser: %v", err)
	}
	check("invite, movies default", id1, true, false)
	reg, err := register.CreateUser(ctx, "registrant-1", "", "correct horse battery", false)
	if err != nil {
		t.Fatalf("register CreateUser: %v", err)
	}
	check("register, movies default", reg.ID, true, false)

	// Flip the defaults: new accounts follow, existing ones don't move.
	if err := st.SetRequests(ctx, settings.RequestsConfig{DefaultAutoApproveTV: true}); err != nil {
		t.Fatalf("SetRequests: %v", err)
	}
	reg2, err := register.CreateUser(ctx, "registrant-2", "", "correct horse battery", false)
	if err != nil {
		t.Fatalf("register CreateUser: %v", err)
	}
	check("register, tv default", reg2.ID, false, true)
	check("invite created earlier", id1, true, false)
	check("register created earlier", reg.ID, true, false)
}
