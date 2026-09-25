package main

import (
	"context"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
)

var pinDigitsOnly = regexp.MustCompile(`^\d{4}$`)

type userQuerier interface {
	GetUser(ctx context.Context, id uuid.UUID) (gen.User, error)
	SetUserPIN(ctx context.Context, arg gen.SetUserPINParams) error
	ClearUserPIN(ctx context.Context, id uuid.UUID) error
	ListSwitchableUsers(ctx context.Context, parentUserID pgtype.UUID) ([]gen.ListSwitchableUsersRow, error)
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

	// Cost 12 to match every other stored credential (DefaultCost=10 was the
	// lone outlier here). The 4-digit keyspace makes bcrypt cost only marginally
	// helpful, but there's no reason to hash the PIN weaker than the password.
	hash, err := bcrypt.GenerateFromPassword([]byte(rawPIN), 12)
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

// ListSwitchable returns the caller's own managed profiles with their has_pin
// status (never the hash). Scoped to parent_user_id == callerID: the profile
// picker must not enumerate other households' accounts, and these are the only
// accounts the caller is allowed to PIN-switch into.
func (s *userService) ListSwitchable(ctx context.Context, callerID uuid.UUID) ([]v1.SwitchableUser, error) {
	rows, err := s.db.ListSwitchableUsers(ctx, pgtype.UUID{Bytes: callerID, Valid: true})
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

// VerifyPIN authorizes a PIN-switch from callerID into targetID. It enforces the
// household relationship and the non-admin / no-second-factor invariants BEFORE
// comparing the PIN, then verifies the PIN against the stored bcrypt hash.
// Returns the target's details on success so the caller can issue tokens.
//
// Every failure returns the same ErrInvalidCredentials so the endpoint cannot be
// used as an oracle for "does this account exist / is it an admin / is it in my
// household" — the answer is uniform whether the target is unrelated, an admin,
// second-factor protected, PIN-less, or the PIN is simply wrong.
func (s *userService) VerifyPIN(ctx context.Context, callerID, targetID uuid.UUID, rawPIN string) (*v1.PINSwitchResult, error) {
	if !pinDigitsOnly.MatchString(rawPIN) {
		return nil, v1.ErrBadPIN
	}

	user, err := s.db.GetUser(ctx, targetID)
	if err != nil {
		return nil, v1.ErrInvalidCredentials
	}

	// Household gate: the target must be a managed profile OWNED by the caller.
	// This is the fix for cross-account PIN-switching — without it, any user
	// could switch into any non-admin account by guessing its 4-digit PIN.
	if !user.ParentUserID.Valid || uuid.UUID(user.ParentUserID.Bytes) != callerID {
		return nil, v1.ErrInvalidCredentials
	}

	// A 4-digit PIN must never grant admin or step over a second factor.
	// Managed profiles carry neither today; this refuses defensively in case a
	// profile row was ever promoted to admin or enrolled in TOTP.
	if user.IsAdmin || user.TotpEnabled {
		return nil, v1.ErrInvalidCredentials
	}

	if user.Pin == nil {
		return nil, v1.ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(*user.Pin), []byte(rawPIN)); err != nil {
		return nil, v1.ErrInvalidCredentials
	}

	result := &v1.PINSwitchResult{
		UserID:       user.ID,
		Username:     user.Username,
		IsAdmin:      user.IsAdmin,
		SessionEpoch: user.SessionEpoch,
	}
	if user.MaxContentRating != nil {
		result.MaxContentRating = *user.MaxContentRating
	}
	return result, nil
}
