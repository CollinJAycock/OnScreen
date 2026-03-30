package main

import (
	"context"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
)

var pinDigitsOnly = regexp.MustCompile(`^\d{4}$`)

type userQuerier interface {
	GetUser(ctx context.Context, id uuid.UUID) (gen.User, error)
	SetUserPIN(ctx context.Context, arg gen.SetUserPINParams) error
	ClearUserPIN(ctx context.Context, id uuid.UUID) error
	ListSwitchableUsers(ctx context.Context) ([]gen.ListSwitchableUsersRow, error)
}

type userService struct {
	db userQuerier
}

func newUserService(db userQuerier) *userService {
	return &userService{db: db}
}

// SetPIN validates the user's current password, then stores a bcrypt hash of rawPIN.
func (s *userService) SetPIN(ctx context.Context, userID uuid.UUID, rawPIN, password string) error {
	if !pinDigitsOnly.MatchString(rawPIN) {
		return v1.ErrBadPIN
	}

	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	// Require current password for non-admin PIN changes.
	if user.PasswordHash != nil {
		if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(password)); err != nil {
			return v1.ErrInvalidCredentials
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(rawPIN), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash PIN: %w", err)
	}
	pinHash := string(hash)
	return s.db.SetUserPIN(ctx, gen.SetUserPINParams{ID: userID, Pin: &pinHash})
}

// ClearPIN validates the user's current password, then removes the PIN.
func (s *userService) ClearPIN(ctx context.Context, userID uuid.UUID, password string) error {
	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	if user.PasswordHash != nil {
		if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(password)); err != nil {
			return v1.ErrInvalidCredentials
		}
	}

	return s.db.ClearUserPIN(ctx, userID)
}

// ListSwitchable returns all users with their has_pin status (never exposes the hash).
func (s *userService) ListSwitchable(ctx context.Context) ([]v1.SwitchableUser, error) {
	rows, err := s.db.ListSwitchableUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list switchable users: %w", err)
	}
	out := make([]v1.SwitchableUser, len(rows))
	for i, r := range rows {
		out[i] = v1.SwitchableUser{
			ID:       r.ID,
			Username: r.Username,
			IsAdmin:  r.IsAdmin,
			HasPin:   r.HasPin.(bool),
		}
	}
	return out, nil
}

// VerifyPIN looks up a user by ID and verifies the submitted PIN against the stored bcrypt hash.
// Returns the gen.User on success so the caller can issue tokens.
func (s *userService) VerifyPIN(ctx context.Context, userID uuid.UUID, rawPIN string) (*v1.PINSwitchResult, error) {
	if !pinDigitsOnly.MatchString(rawPIN) {
		return nil, v1.ErrBadPIN
	}

	user, err := s.db.GetUser(ctx, userID)
	if err != nil {
		return nil, v1.ErrInvalidCredentials
	}

	if user.Pin == nil {
		return nil, v1.ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(*user.Pin), []byte(rawPIN)); err != nil {
		return nil, v1.ErrInvalidCredentials
	}

	return &v1.PINSwitchResult{
		UserID:   user.ID,
		Username: user.Username,
		IsAdmin:  user.IsAdmin,
	}, nil
}
