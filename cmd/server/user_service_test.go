package main

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// pgUUID wraps a uuid.UUID as a valid pgtype.UUID for building gen.User rows in
// tests (parent_user_id is nullable, so it's pgtype.UUID in the generated model).
func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// stubUserQ implements userQuerier with hand-crafted state. Keeps
// behaviour observable: each method records the most recent params
// and the returned err comes from a field test code can populate.
type stubUserQ struct {
	user gen.User

	getErr   error
	setErr   error
	clearErr error
	listErr  error

	setCalled   bool
	clearCalled bool
	setPin      *string
}

func (s *stubUserQ) GetUser(_ context.Context, _ uuid.UUID) (gen.User, error) {
	return s.user, s.getErr
}
func (s *stubUserQ) SetUserPIN(_ context.Context, arg gen.SetUserPINParams) error {
	s.setCalled = true
	s.setPin = arg.Pin
	return s.setErr
}
func (s *stubUserQ) ClearUserPIN(_ context.Context, _ uuid.UUID) error {
	s.clearCalled = true
	return s.clearErr
}
func (s *stubUserQ) ListSwitchableUsers(_ context.Context, _ pgtype.UUID) ([]gen.ListSwitchableUsersRow, error) {
	return nil, s.listErr
}

// hashPassword bcrypt-hashes a string at the default cost.
func hashPassword(t *testing.T, pw string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(h)
}

// ── SetPIN ───────────────────────────────────────────────────────────────────

func TestSetPIN_RejectsNonDigitPIN(t *testing.T) {
	cases := []string{"abcd", "12a4", "12345", "12", ""}
	svc := newUserService(&stubUserQ{})
	for _, p := range cases {
		err := svc.SetPIN(context.Background(), uuid.New(), p, "any-password")
		if !errors.Is(err, v1.ErrBadPIN) {
			t.Errorf("PIN %q: got %v, want ErrBadPIN", p, err)
		}
	}
}

func TestSetPIN_AcceptsCorrectPassword(t *testing.T) {
	pw := "correct-password"
	hash := hashPassword(t, pw)
	q := &stubUserQ{
		user: gen.User{ID: uuid.New(), PasswordHash: &hash},
	}
	svc := newUserService(q)

	err := svc.SetPIN(context.Background(), q.user.ID, "1234", pw)
	if err != nil {
		t.Fatalf("SetPIN: %v", err)
	}
	if !q.setCalled {
		t.Fatal("SetUserPIN was not called")
	}
	// Stored value should be a bcrypt hash of "1234", not the plain PIN.
	if q.setPin == nil || *q.setPin == "1234" {
		t.Errorf("SetPIN stored raw PIN instead of bcrypt hash: %v", q.setPin)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(*q.setPin), []byte("1234")); err != nil {
		t.Errorf("stored hash doesn't match raw PIN: %v", err)
	}
}

func TestSetPIN_RejectsWrongPassword(t *testing.T) {
	pw := "real-password"
	hash := hashPassword(t, pw)
	q := &stubUserQ{
		user: gen.User{ID: uuid.New(), PasswordHash: &hash},
	}
	svc := newUserService(q)

	err := svc.SetPIN(context.Background(), q.user.ID, "1234", "WRONG")
	if !errors.Is(err, v1.ErrInvalidCredentials) {
		t.Errorf("got %v, want ErrInvalidCredentials", err)
	}
	if q.setCalled {
		t.Error("SetUserPIN should NOT be called when password verification fails")
	}
}

func TestSetPIN_PasswordlessUserSkipsCheck(t *testing.T) {
	// Managed-profile users (created without a password) — the password
	// check is skipped. Set goes straight through to storage.
	q := &stubUserQ{
		user: gen.User{ID: uuid.New(), PasswordHash: nil},
	}
	svc := newUserService(q)

	if err := svc.SetPIN(context.Background(), q.user.ID, "9876", ""); err != nil {
		t.Fatalf("SetPIN: %v", err)
	}
	if !q.setCalled {
		t.Error("SetUserPIN should still be called for passwordless users")
	}
}

// ── ClearPIN ─────────────────────────────────────────────────────────────────

func TestClearPIN_AcceptsCorrectPassword(t *testing.T) {
	pw := "right-pw"
	hash := hashPassword(t, pw)
	q := &stubUserQ{
		user: gen.User{ID: uuid.New(), PasswordHash: &hash},
	}
	svc := newUserService(q)

	if err := svc.ClearPIN(context.Background(), q.user.ID, pw); err != nil {
		t.Fatalf("ClearPIN: %v", err)
	}
	if !q.clearCalled {
		t.Error("ClearUserPIN was not called")
	}
}

func TestClearPIN_RejectsWrongPassword(t *testing.T) {
	pw := "right-pw"
	hash := hashPassword(t, pw)
	q := &stubUserQ{
		user: gen.User{ID: uuid.New(), PasswordHash: &hash},
	}
	svc := newUserService(q)

	err := svc.ClearPIN(context.Background(), q.user.ID, "WRONG")
	if !errors.Is(err, v1.ErrInvalidCredentials) {
		t.Errorf("got %v, want ErrInvalidCredentials", err)
	}
	if q.clearCalled {
		t.Error("ClearUserPIN should NOT be called on wrong password")
	}
}

// ── VerifyPIN ────────────────────────────────────────────────────────────────

// managedProfile builds a managed-profile row owned by callerID with the given
// PIN hash — the shape VerifyPIN must accept.
func managedProfile(callerID uuid.UUID, hash *string) gen.User {
	return gen.User{
		ID: uuid.New(), Username: "alice", IsAdmin: false,
		Pin: hash, SessionEpoch: 7, ParentUserID: pgUUID(callerID),
	}
}

func TestVerifyPIN_RejectsBadFormat(t *testing.T) {
	for _, p := range []string{"abc", "12345", ""} {
		_, err := newUserService(&stubUserQ{}).VerifyPIN(context.Background(), uuid.New(), uuid.New(), p)
		if !errors.Is(err, v1.ErrBadPIN) {
			t.Errorf("PIN %q: got %v, want ErrBadPIN", p, err)
		}
	}
}

func TestVerifyPIN_AcceptsCorrectPIN(t *testing.T) {
	rawPIN := "4321"
	hash := hashPassword(t, rawPIN)
	caller := uuid.New()
	q := &stubUserQ{user: managedProfile(caller, &hash)}
	svc := newUserService(q)

	res, err := svc.VerifyPIN(context.Background(), caller, q.user.ID, rawPIN)
	if err != nil {
		t.Fatalf("VerifyPIN: %v", err)
	}
	if res.Username != "alice" {
		t.Errorf("username = %q, want alice", res.Username)
	}
	if res.SessionEpoch != 7 {
		t.Errorf("session_epoch = %d, want 7 (must round-trip for token issuance)", res.SessionEpoch)
	}
}

func TestVerifyPIN_RejectsWrongPIN(t *testing.T) {
	hash := hashPassword(t, "1234")
	caller := uuid.New()
	q := &stubUserQ{user: managedProfile(caller, &hash)}
	svc := newUserService(q)

	_, err := svc.VerifyPIN(context.Background(), caller, q.user.ID, "9999")
	if !errors.Is(err, v1.ErrInvalidCredentials) {
		t.Errorf("got %v, want ErrInvalidCredentials", err)
	}
}

// TestVerifyPIN_RejectsForeignHousehold is the core cross-account-switch fix:
// a correct PIN on a profile owned by SOMEONE ELSE must be refused.
func TestVerifyPIN_RejectsForeignHousehold(t *testing.T) {
	rawPIN := "4321"
	hash := hashPassword(t, rawPIN)
	owner := uuid.New()
	attacker := uuid.New()
	q := &stubUserQ{user: managedProfile(owner, &hash)} // owned by `owner`
	svc := newUserService(q)

	_, err := svc.VerifyPIN(context.Background(), attacker, q.user.ID, rawPIN)
	if !errors.Is(err, v1.ErrInvalidCredentials) {
		t.Errorf("got %v, want ErrInvalidCredentials — a correct PIN on another household's profile must be refused", err)
	}
}

// TestVerifyPIN_RejectsTopLevelAccount refuses switching into a full account
// (parent_user_id NULL) even when it is in the caller's chain and has a PIN.
func TestVerifyPIN_RejectsTopLevelAccount(t *testing.T) {
	rawPIN := "4321"
	hash := hashPassword(t, rawPIN)
	caller := uuid.New()
	u := managedProfile(caller, &hash)
	u.ParentUserID = pgtype.UUID{} // NULL — a top-level account
	q := &stubUserQ{user: u}
	svc := newUserService(q)

	_, err := svc.VerifyPIN(context.Background(), caller, q.user.ID, rawPIN)
	if !errors.Is(err, v1.ErrInvalidCredentials) {
		t.Errorf("got %v, want ErrInvalidCredentials — a full account is not a PIN-switch target", err)
	}
}

// TestVerifyPIN_RejectsAdminTarget refuses a profile flagged admin even with a
// correct PIN and correct household — a 4-digit PIN must never grant admin.
func TestVerifyPIN_RejectsAdminTarget(t *testing.T) {
	rawPIN := "4321"
	hash := hashPassword(t, rawPIN)
	caller := uuid.New()
	u := managedProfile(caller, &hash)
	u.IsAdmin = true
	q := &stubUserQ{user: u}
	svc := newUserService(q)

	_, err := svc.VerifyPIN(context.Background(), caller, q.user.ID, rawPIN)
	if !errors.Is(err, v1.ErrInvalidCredentials) {
		t.Errorf("got %v, want ErrInvalidCredentials — PIN must not enter an admin account", err)
	}
}

// TestVerifyPIN_RejectsTOTPTarget refuses a profile with a second factor even on
// a correct PIN — the PIN path must not step over TOTP.
func TestVerifyPIN_RejectsTOTPTarget(t *testing.T) {
	rawPIN := "4321"
	hash := hashPassword(t, rawPIN)
	caller := uuid.New()
	u := managedProfile(caller, &hash)
	u.TotpEnabled = true
	q := &stubUserQ{user: u}
	svc := newUserService(q)

	_, err := svc.VerifyPIN(context.Background(), caller, q.user.ID, rawPIN)
	if !errors.Is(err, v1.ErrInvalidCredentials) {
		t.Errorf("got %v, want ErrInvalidCredentials — PIN must not bypass a second factor", err)
	}
}

func TestVerifyPIN_NoPINSetReturnsInvalidCredentials(t *testing.T) {
	// User exists in the household but never set a PIN — must NOT crash, must
	// NOT distinguish from wrong-PIN (no PIN-set enumeration).
	caller := uuid.New()
	q := &stubUserQ{user: managedProfile(caller, nil)}
	svc := newUserService(q)

	_, err := svc.VerifyPIN(context.Background(), caller, q.user.ID, "1234")
	if !errors.Is(err, v1.ErrInvalidCredentials) {
		t.Errorf("got %v, want ErrInvalidCredentials (must not leak PIN-existence)", err)
	}
}

func TestVerifyPIN_MissingUserReturnsInvalidCredentials(t *testing.T) {
	// No timing-attack divergence — missing user must surface as
	// invalid-credentials, same as wrong PIN. (The DB error is mapped
	// in the service layer; the handler doesn't get to see it.)
	q := &stubUserQ{getErr: errors.New("not found")}
	svc := newUserService(q)

	_, err := svc.VerifyPIN(context.Background(), uuid.New(), uuid.New(), "1234")
	if !errors.Is(err, v1.ErrInvalidCredentials) {
		t.Errorf("got %v, want ErrInvalidCredentials (no user-existence leak)", err)
	}
}

// ── ListSwitchable ───────────────────────────────────────────────────────────

func TestListSwitchable_PropagatesDBError(t *testing.T) {
	q := &stubUserQ{listErr: errors.New("db down")}
	_, err := newUserService(q).ListSwitchable(context.Background(), uuid.New())
	if err == nil {
		t.Error("expected DB error to propagate")
	}
}

func TestPinDigitsOnly_RegexMatches(t *testing.T) {
	good := []string{"0000", "1234", "9999"}
	bad := []string{"", "1", "12", "123", "12345", "abcd", "12a4", "1.34", " 1234"}
	for _, g := range good {
		if !pinDigitsOnly.MatchString(g) {
			t.Errorf("%q should match", g)
		}
	}
	for _, b := range bad {
		if pinDigitsOnly.MatchString(b) {
			t.Errorf("%q should NOT match", b)
		}
	}
}
