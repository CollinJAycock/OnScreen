package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// createUserFakeDB implements the slice of authQuerier that CreateUser
// touches; the embedded nil interface panics on anything else.
type createUserFakeDB struct {
	authQuerier
	created []gen.CreateUserParams
}

func (f *createUserFakeDB) CreateUser(_ context.Context, p gen.CreateUserParams) (gen.User, error) {
	f.created = append(f.created, p)
	return gen.User{ID: uuid.New(), Username: p.Username, IsAdmin: p.IsAdmin}, nil
}
func (f *createUserFakeDB) GrantAutoLibrariesToUser(context.Context, uuid.UUID) error { return nil }

type staticRequestDefaults struct{ cfg settings.RequestsConfig }

func (s staticRequestDefaults) Requests(context.Context) settings.RequestsConfig { return s.cfg }

// TestAuthService_CreateUser_AppliesRequestDefaults: local registration (and an
// admin creating an account) seeds the new row from Settings ▸ Requests; with
// no settings source wired both toggles stay off.
func TestAuthService_CreateUser_AppliesRequestDefaults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		src        v1.RequestDefaultsReader
		isAdmin    bool
		wantMovies bool
		wantTV     bool
	}{
		{name: "movies default", src: staticRequestDefaults{settings.RequestsConfig{DefaultAutoApproveMovies: true}}, wantMovies: true},
		{name: "both defaults, admin-created admin", src: staticRequestDefaults{settings.RequestsConfig{DefaultAutoApproveMovies: true, DefaultAutoApproveTV: true}}, isAdmin: true, wantMovies: true, wantTV: true},
		{name: "defaults off", src: staticRequestDefaults{}},
		{name: "not wired", src: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &createUserFakeDB{}
			svc := &authService{db: db, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), reqDefaults: tc.src}
			if _, err := svc.CreateUser(context.Background(), "gina", "", "correct horse battery", tc.isAdmin); err != nil {
				t.Fatalf("CreateUser: %v", err)
			}
			if len(db.created) != 1 {
				t.Fatalf("CreateUser calls = %d, want 1", len(db.created))
			}
			got := db.created[0]
			if got.AutoApproveMovies != tc.wantMovies || got.AutoApproveTv != tc.wantTV {
				t.Errorf("created with movies=%v tv=%v, want %v/%v",
					got.AutoApproveMovies, got.AutoApproveTv, tc.wantMovies, tc.wantTV)
			}
		})
	}
}
