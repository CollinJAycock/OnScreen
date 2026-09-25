package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/valkey"
)

// stepUpFakeDB implements the slice of authQuerier the step-up / TOTP /
// refresh paths touch. The embedded nil interface panics on anything else,
// which flags a test exercising more than it meant to.
type stepUpFakeDB struct {
	authQuerier
	mu          sync.Mutex
	users       map[uuid.UUID]gen.User
	totpOff     bool
	sessionRow  gen.GetSessionByAnyTokenHashRow
	sessionByTH gen.Session
	familyBurns []uuid.UUID
	epochBumps  []uuid.UUID
}

func (f *stepUpFakeDB) GetUser(_ context.Context, id uuid.UUID) (gen.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return gen.User{}, errors.New("no such user")
	}
	return u, nil
}
func (f *stepUpFakeDB) ConsumeTOTPRecoveryCode(context.Context, gen.ConsumeTOTPRecoveryCodeParams) (int64, error) {
	return 0, nil
}
func (f *stepUpFakeDB) DisableUserTOTP(context.Context, uuid.UUID) error {
	f.mu.Lock()
	f.totpOff = true
	f.mu.Unlock()
	return nil
}
func (f *stepUpFakeDB) DeleteTOTPRecoveryCodes(context.Context, uuid.UUID) error { return nil }
func (f *stepUpFakeDB) GetSessionByAnyTokenHash(context.Context, string) (gen.GetSessionByAnyTokenHashRow, error) {
	return f.sessionRow, nil
}
func (f *stepUpFakeDB) GetSessionByTokenHash(context.Context, string) (gen.Session, error) {
	return f.sessionByTH, nil
}
func (f *stepUpFakeDB) DeleteSession(context.Context, uuid.UUID) error { return nil }
func (f *stepUpFakeDB) DeleteSessionsForUser(_ context.Context, id uuid.UUID) error {
	f.familyBurns = append(f.familyBurns, id)
	return nil
}
func (f *stepUpFakeDB) BumpSessionEpoch(_ context.Context, id uuid.UUID) error {
	f.epochBumps = append(f.epochBumps, id)
	return nil
}

type fakeSegRevoker struct{ revoked []uuid.UUID }

func (f *fakeSegRevoker) RevokeAllForUser(_ context.Context, id uuid.UUID) error {
	f.revoked = append(f.revoked, id)
	return nil
}

type stepUpFixture struct {
	svc    *authService
	db     *stepUpFakeDB
	userID uuid.UUID
	secret string
	clock  *time.Time
	mr     *miniredis.Miniredis
}

const stepUpPassword = "correct horse"

func newStepUpFixture(t *testing.T) *stepUpFixture {
	t.Helper()
	key := auth.DeriveKey32("test-secret-key-that-is-32-bytes!")
	tm, err := auth.NewTokenMaker(key)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := auth.GenerateTOTPSecret("alice")
	if err != nil {
		t.Fatal(err)
	}
	ct, err := enc.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(stepUpPassword), bcrypt.MinCost)
	hs := string(hash)
	uid := uuid.New()
	db := &stepUpFakeDB{users: map[uuid.UUID]gen.User{
		uid: {ID: uid, Username: "alice", PasswordHash: &hs, TotpEnabled: true, TotpSecret: &ct},
	}}

	mr := miniredis.RunT(t)
	vc, err := valkey.New(context.Background(), "redis://"+mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vc.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	clock := time.Unix(1_700_000_010, 0)
	f := &stepUpFixture{db: db, userID: uid, secret: secret, clock: &clock, mr: mr}
	f.svc = &authService{
		db: db, tokens: tm, enc: enc, logger: logger,
		rateLimiter: valkey.NewRateLimiter(vc, logger, nil),
		totpReplay:  valkey.NewTOTPReplayGuard(vc),
		now:         func() time.Time { return *f.clock },
	}
	return f
}

// code returns the TOTP code for the fixture clock's current step.
func (f *stepUpFixture) code(t *testing.T) string {
	t.Helper()
	c, err := totp.GenerateCode(f.secret, *f.clock)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *stepUpFixture) nextStep() { *f.clock = f.clock.Add(30 * time.Second) }

// VerifyReauth guards the SECRET_KEY / DB URL reveal and backup restore; it
// must be capped per user, not only by the general per-session API limit.
func TestVerifyReauth_PerUserFailureCap(t *testing.T) {
	f := newStepUpFixture(t)
	ctx := context.Background()
	for i := 0; i < MaxTOTPFailuresPerUser; i++ {
		if err := f.svc.VerifyReauth(ctx, f.userID, "wrong", f.code(t)); err == nil || errors.Is(err, errStepUpLocked) {
			t.Fatalf("attempt %d: got %v, want a plain wrong-password failure", i, err)
		}
	}
	// Even the right password + a fresh code is refused while locked out.
	if err := f.svc.VerifyReauth(ctx, f.userID, stepUpPassword, f.code(t)); !errors.Is(err, errStepUpLocked) {
		t.Fatalf("after %d failures: got %v, want errStepUpLocked", MaxTOTPFailuresPerUser, err)
	}
}

// Wrong TOTP codes on reauth count as well as wrong passwords.
func TestVerifyReauth_WrongCodeCounts(t *testing.T) {
	f := newStepUpFixture(t)
	ctx := context.Background()
	for i := 0; i < MaxTOTPFailuresPerUser; i++ {
		_ = f.svc.VerifyReauth(ctx, f.userID, stepUpPassword, "000000")
	}
	if err := f.svc.VerifyReauth(ctx, f.userID, stepUpPassword, f.code(t)); !errors.Is(err, errStepUpLocked) {
		t.Fatalf("got %v, want errStepUpLocked after repeated bad codes", err)
	}
}

// A concurrent burst is capped too: at most the cap's worth of attempts ever
// reach the password check.
func TestVerifyReauth_ConcurrentBurstCapped(t *testing.T) {
	f := newStepUpFixture(t)
	ctx := context.Background()
	var reachedCheck atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := f.svc.VerifyReauth(ctx, f.userID, "wrong", ""); err != nil && !errors.Is(err, errStepUpLocked) {
				reachedCheck.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if n := reachedCheck.Load(); n > MaxTOTPFailuresPerUser {
		t.Fatalf("%d concurrent guesses reached the password check, cap is %d", n, MaxTOTPFailuresPerUser)
	}
}

// DisableTOTP shares the per-user budget: failures spent on reauth lock it too.
func TestDisableTOTP_SharesStepUpCap(t *testing.T) {
	f := newStepUpFixture(t)
	ctx := context.Background()
	for i := 0; i < MaxTOTPFailuresPerUser; i++ {
		_ = f.svc.DisableTOTP(ctx, f.userID, "000000")
	}
	if err := f.svc.DisableTOTP(ctx, f.userID, f.code(t)); !errors.Is(err, v1.ErrBadTOTPCode) {
		t.Fatalf("got %v, want ErrBadTOTPCode while locked out", err)
	}
	if f.db.totpOff {
		t.Fatal("2FA was disabled despite the lockout")
	}
}

// In-session step-up must not spend the login second-factor budget: step-up is
// reachable from any live session without the password, so a shared counter
// let a stolen session lock the real user out of signing in on a new device —
// and so out of revoking that session.
func TestStepUpFailures_DoNotLockLoginVerify(t *testing.T) {
	f := newStepUpFixture(t)
	ctx := context.Background()
	for i := 0; i < MaxTOTPFailuresPerUser+2; i++ {
		_ = f.svc.DisableTOTP(ctx, f.userID, "000000")
		_ = f.svc.VerifyReauth(ctx, f.userID, "wrong", "000000")
	}
	allowed, err := f.svc.rateLimiter.CheckFailures(ctx, loginTOTPFailureKey(f.userID), MaxTOTPFailuresPerUser)
	if err != nil || !allowed {
		t.Fatalf("login verify budget spent by step-up failures: allowed=%v err=%v", allowed, err)
	}
	if loginTOTPFailureKey(f.userID) == stepUpFailureKey(f.userID) {
		t.Fatal("login and step-up counters share a key")
	}
}

func TestVerifyReauth_SuccessResetsCap(t *testing.T) {
	f := newStepUpFixture(t)
	ctx := context.Background()
	for i := 0; i < MaxTOTPFailuresPerUser-1; i++ {
		_ = f.svc.VerifyReauth(ctx, f.userID, "wrong", "")
	}
	if err := f.svc.VerifyReauth(ctx, f.userID, stepUpPassword, f.code(t)); err != nil {
		t.Fatalf("correct credentials under the cap: %v", err)
	}
	f.nextStep()
	for i := 0; i < MaxTOTPFailuresPerUser-1; i++ {
		if err := f.svc.VerifyReauth(ctx, f.userID, "wrong", ""); errors.Is(err, errStepUpLocked) {
			t.Fatalf("failure %d after a success hit the lockout — success didn't reset", i)
		}
	}
}

// The same TOTP code can't be used twice within its validity window.
func TestVerifyReauth_TOTPCodeIsSingleUse(t *testing.T) {
	f := newStepUpFixture(t)
	ctx := context.Background()
	code := f.code(t)
	if err := f.svc.VerifyReauth(ctx, f.userID, stepUpPassword, code); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := f.svc.VerifyReauth(ctx, f.userID, stepUpPassword, code); err == nil {
		t.Fatal("replayed TOTP code was accepted")
	}
	// Nor can the replay be laundered through a different endpoint.
	if err := f.svc.DisableTOTP(ctx, f.userID, code); !errors.Is(err, v1.ErrBadTOTPCode) {
		t.Fatalf("replay via DisableTOTP: got %v, want ErrBadTOTPCode", err)
	}
	// An older code still inside the skew window is refused too.
	prev, _ := totp.GenerateCode(f.secret, f.clock.Add(-30*time.Second))
	if err := f.svc.VerifyReauth(ctx, f.userID, stepUpPassword, prev); err == nil {
		t.Fatal("a code from an earlier step than the last used one was accepted")
	}
	f.nextStep()
	if err := f.svc.VerifyReauth(ctx, f.userID, stepUpPassword, f.code(t)); err != nil {
		t.Fatalf("next step's code: %v", err)
	}
}

type erroringReplayGuard struct{}

func (erroringReplayGuard) ConsumeTOTPStep(context.Context, uuid.UUID, int64) (bool, error) {
	return false, errors.New("valkey down")
}

// With the shared guard unavailable, the in-process guard still refuses replays.
func TestTOTPReplay_FallsBackToInProcessGuard(t *testing.T) {
	f := newStepUpFixture(t)
	f.svc.totpReplay = erroringReplayGuard{}
	f.svc.rateLimiter = nil
	ctx := context.Background()
	code := f.code(t)
	if err := f.svc.VerifyReauth(ctx, f.userID, stepUpPassword, code); err != nil {
		t.Fatalf("first use with guard down: %v", err)
	}
	if err := f.svc.VerifyReauth(ctx, f.userID, stepUpPassword, code); err == nil {
		t.Fatal("replay accepted while the shared guard was down")
	}
}

// Refresh-token reuse burns the whole session family; HLS segment tokens must
// go with it.
func TestRefreshReuse_RevokesSegmentTokens(t *testing.T) {
	f := newStepUpFixture(t)
	rev := &fakeSegRevoker{}
	f.svc.segTokens = rev
	f.db.sessionRow = gen.GetSessionByAnyTokenHashRow{ID: uuid.New(), UserID: f.userID, IsCurrent: false}

	if _, err := f.svc.Refresh(context.Background(), "superseded-token"); err == nil {
		t.Fatal("a superseded refresh token must be rejected")
	}
	if len(rev.revoked) != 1 || rev.revoked[0] != f.userID {
		t.Fatalf("segment tokens revoked for %v, want [%s]", rev.revoked, f.userID)
	}
}

// A single-device logout must not cut HLS playback on the user's other
// devices: segment tokens aren't tied to the refresh session.
func TestLogout_DoesNotRevokeOtherDevicesSegmentTokens(t *testing.T) {
	f := newStepUpFixture(t)
	rev := &fakeSegRevoker{}
	f.svc.segTokens = rev
	f.db.sessionByTH = gen.Session{ID: uuid.New(), UserID: f.userID}
	if err := f.svc.Logout(context.Background(), "rt"); err != nil {
		t.Fatal(err)
	}
	if len(rev.revoked) != 0 {
		t.Fatalf("single-device logout revoked all segment tokens: %v", rev.revoked)
	}
}
