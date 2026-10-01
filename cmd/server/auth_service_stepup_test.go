package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
	sessionErr  error // GetSessionByAnyTokenHash failure (pgx.ErrNoRows = unknown / expired token)
	userErr     error // GetUser failure, when set
	sessionByTH gen.Session
	familyBurns []uuid.UUID
	epochBumps  []uuid.UUID
	// deleteErr / bumpErr fail DeleteSessionsForUser / BumpSessionEpoch (the
	// attempt is still recorded).
	deleteErr error
	bumpErr   error
	// rotateRows is what RotateSessionConditional reports: 1 = rotated,
	// 0 = someone rotated the token first (the CAS-miss reuse branch).
	// rotateErr fails it instead.
	rotateRows int64
	rotateErr  error
	// rotatedHash is the new hash RotateSessionConditional was asked to
	// write; rotateLanded says whether that write committed despite
	// rotateErr, which is what a lookup by rotatedHash then reports.
	rotatedHash  string
	rotateLanded bool
}

func (f *stepUpFakeDB) GetUser(_ context.Context, id uuid.UUID) (gen.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.userErr != nil {
		return gen.User{}, f.userErr
	}
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
func (f *stepUpFakeDB) GetSessionByAnyTokenHash(_ context.Context, hash string) (gen.GetSessionByAnyTokenHashRow, error) {
	if f.rotatedHash != "" && hash == f.rotatedHash {
		if !f.rotateLanded {
			return gen.GetSessionByAnyTokenHashRow{}, pgx.ErrNoRows
		}
		row := f.sessionRow
		row.IsCurrent = true
		return row, nil
	}
	if f.sessionErr != nil {
		return gen.GetSessionByAnyTokenHashRow{}, f.sessionErr
	}
	return f.sessionRow, nil
}
func (f *stepUpFakeDB) RotateSessionConditional(_ context.Context, arg gen.RotateSessionConditionalParams) (int64, error) {
	f.rotatedHash = arg.TokenHash
	return f.rotateRows, f.rotateErr
}
func (f *stepUpFakeDB) GetSessionByTokenHash(context.Context, string) (gen.Session, error) {
	return f.sessionByTH, nil
}
func (f *stepUpFakeDB) DeleteSession(context.Context, uuid.UUID) error { return nil }
func (f *stepUpFakeDB) DeleteSessionsForUser(_ context.Context, id uuid.UUID) error {
	f.familyBurns = append(f.familyBurns, id)
	return f.deleteErr
}
func (f *stepUpFakeDB) BumpSessionEpoch(_ context.Context, id uuid.UUID) error {
	f.epochBumps = append(f.epochBumps, id)
	return f.bumpErr
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

// Both reuse branches must hand the handler a *v1.RefreshReuseError naming the
// user, the session and which check fired — that is all AuthHandler.Refresh
// has to write the auth.sessions_revoked audit entry from. Before, both
// returned a bare fmt.Errorf and the wipe never reached the audit log.
func TestRefreshReuse_ReturnsTypedError(t *testing.T) {
	cases := []struct {
		name        string
		isCurrent   bool
		wantTrigger string
	}{
		// The victim's client (or the thief) presents a token that was
		// already rotated away.
		{"superseded token", false, v1.RefreshReuseSuperseded},
		// The token was current at lookup, but another request rotated it
		// before this one's compare-and-swap landed (rotateRows stays 0).
		{"rotation race", true, v1.RefreshReuseRotationRace},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newStepUpFixture(t)
			sid := uuid.New()
			client, platform := "OnScreen TV", "android_tv"
			f.db.sessionRow = gen.GetSessionByAnyTokenHashRow{
				ID: sid, UserID: f.userID, IsCurrent: c.isCurrent,
				TokenHash: "current-hash", ClientName: &client, Platform: &platform,
				LastSeen: pgtype.Timestamptz{Time: f.clock.Add(-95 * time.Second), Valid: true},
			}

			_, err := f.svc.Refresh(context.Background(), "spent-token")
			var reuse *v1.RefreshReuseError
			if !errors.As(err, &reuse) {
				t.Fatalf("err = %v (%T), want *v1.RefreshReuseError", err, err)
			}
			if reuse.UserID != f.userID || reuse.SessionID != sid || reuse.Trigger != c.wantTrigger {
				t.Errorf("reuse error = %+v, want user %s session %s trigger %q",
					*reuse, f.userID, sid, c.wantTrigger)
			}
			if !reuse.SessionsDeleted || !reuse.EpochBumped {
				t.Errorf("SessionsDeleted=%v EpochBumped=%v, want both true — both writes succeeded",
					reuse.SessionsDeleted, reuse.EpochBumped)
			}
			// Forensics from the row, idle time measured on the service clock.
			if reuse.ClientName != client || reuse.Platform != platform {
				t.Errorf("client/platform = %q/%q, want %q/%q", reuse.ClientName, reuse.Platform, client, platform)
			}
			if reuse.SecondsSinceLastSeen == nil || *reuse.SecondsSinceLastSeen != 95 {
				t.Errorf("SecondsSinceLastSeen = %v, want 95", reuse.SecondsSinceLastSeen)
			}
			if strings.Contains(fmt.Sprintf("%+v", *reuse), "current-hash") {
				t.Error("the session's token hash was copied into the reuse error")
			}
			// The typed error must not change what the branch does: the
			// family is still burned.
			if len(f.db.familyBurns) != 1 || len(f.db.epochBumps) != 1 {
				t.Errorf("family burns = %v, epoch bumps = %v, want one each",
					f.db.familyBurns, f.db.epochBumps)
			}
		})
	}
}

// A wipe that failed must say so: the handler records SessionsDeleted /
// EpochBumped in the audit entry, which otherwise claimed a revocation that
// never happened. Both branches, each write failing on its own.
func TestRefreshReuse_ReportsFailedWipe(t *testing.T) {
	cases := []struct {
		name                  string
		isCurrent             bool
		deleteErr, bumpErr    error
		wantDeleted, wantBump bool
	}{
		{"superseded, delete fails", false, errors.New("db down"), nil, false, true},
		{"superseded, bump fails", false, nil, errors.New("db down"), true, false},
		// One write failing on its own in the CAS-miss branch too, so the two
		// flags can't be swapped there without a failure.
		{"rotation race, delete fails", true, errors.New("db down"), nil, false, true},
		{"rotation race, bump fails", true, nil, errors.New("db down"), true, false},
		{"rotation race, both fail", true, errors.New("db down"), errors.New("db down"), false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newStepUpFixture(t)
			f.db.deleteErr, f.db.bumpErr = c.deleteErr, c.bumpErr
			// No last_seen on the row: the idle time is unknown, not zero.
			f.db.sessionRow = gen.GetSessionByAnyTokenHashRow{ID: uuid.New(), UserID: f.userID, IsCurrent: c.isCurrent}

			_, err := f.svc.Refresh(context.Background(), "spent-token")
			var reuse *v1.RefreshReuseError
			if !errors.As(err, &reuse) {
				t.Fatalf("err = %v (%T), want *v1.RefreshReuseError", err, err)
			}
			if reuse.SessionsDeleted != c.wantDeleted || reuse.EpochBumped != c.wantBump {
				t.Errorf("SessionsDeleted=%v EpochBumped=%v, want %v/%v",
					reuse.SessionsDeleted, reuse.EpochBumped, c.wantDeleted, c.wantBump)
			}
			if reuse.SecondsSinceLastSeen != nil {
				t.Errorf("SecondsSinceLastSeen = %d with no last_seen on the row, want nil", *reuse.SecondsSinceLastSeen)
			}
			// A failing first write must not skip the second.
			if len(f.db.familyBurns) != 1 || len(f.db.epochBumps) != 1 {
				t.Errorf("family burns = %v, epoch bumps = %v, want one attempt each",
					f.db.familyBurns, f.db.epochBumps)
			}
		})
	}
}

// Only a verdict on the token may come back as v1.ErrRefreshInvalid: the
// handler answers it with 401, which native clients take as "signed out" and
// wipe their stored sign-in. A database that could not answer must come back as
// any other error (503) — mapping it to "not found" signed out every client
// that refreshed through a DB blip or a restart. Neither is reuse (which would
// audit a wipe that never happened), and neither burns anything.
func TestRefresh_InvalidVersusUnavailable(t *testing.T) {
	dbDown := errors.New("read tcp 10.0.0.5:5432: connection reset by peer")
	cases := []struct {
		name        string
		setup       func(f *stepUpFixture)
		wantInvalid bool
	}{
		// The query filters expired rows, so no row = unknown or expired.
		{"unknown or expired token", func(f *stepUpFixture) { f.db.sessionErr = pgx.ErrNoRows }, true},
		// Sessions cascade with their user: a delete racing the refresh.
		{"user deleted mid-refresh", func(f *stepUpFixture) { f.db.userErr = pgx.ErrNoRows }, true},
		{"session lookup fails", func(f *stepUpFixture) { f.db.sessionErr = dbDown }, false},
		{"session lookup cancelled", func(f *stepUpFixture) { f.db.sessionErr = context.Canceled }, false},
		{"user lookup fails", func(f *stepUpFixture) { f.db.userErr = dbDown }, false},
		{"rotation fails", func(f *stepUpFixture) { f.db.rotateErr = dbDown }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newStepUpFixture(t)
			f.db.sessionRow = gen.GetSessionByAnyTokenHashRow{ID: uuid.New(), UserID: f.userID, IsCurrent: true}
			f.db.rotateRows = 1
			c.setup(f)

			pair, err := f.svc.Refresh(context.Background(), "tok")
			if err == nil {
				t.Fatalf("refresh succeeded: %+v", pair)
			}
			if got := errors.Is(err, v1.ErrRefreshInvalid); got != c.wantInvalid {
				t.Errorf("errors.Is(err, ErrRefreshInvalid) = %v, want %v (err: %v)", got, c.wantInvalid, err)
			}
			var reuse *v1.RefreshReuseError
			if errors.As(err, &reuse) {
				t.Fatalf("reported as reuse: %+v", *reuse)
			}
			if len(f.db.familyBurns) != 0 || len(f.db.epochBumps) != 0 {
				t.Errorf("burned sessions: burns=%v bumps=%v", f.db.familyBurns, f.db.epochBumps)
			}
		})
	}
}

// A rotation that reports an error may still have committed (a connection
// lost after the UPDATE). The service then checks the new hash: if the row
// carries it, the pair minted before the swap is handed back, because a 503
// there would make the client retry a retired token and trip reuse detection.
// If the write did not land it stays a 503 (not a verdict on the token).
func TestRefresh_RotationErrorButCommitted(t *testing.T) {
	connLost := errors.New("unexpected EOF")
	for _, landed := range []bool{true, false} {
		t.Run(fmt.Sprintf("landed=%v", landed), func(t *testing.T) {
			f := newStepUpFixture(t)
			sessionID := uuid.New()
			f.db.sessionRow = gen.GetSessionByAnyTokenHashRow{ID: sessionID, UserID: f.userID, IsCurrent: true}
			f.db.rotateErr = connLost
			f.db.rotateLanded = landed

			pair, err := f.svc.Refresh(context.Background(), "tok")
			if landed {
				if err != nil {
					t.Fatalf("refresh failed although the rotation committed: %v", err)
				}
				if pair.RefreshToken == "" || pair.AccessToken == "" || auth.HashToken(pair.RefreshToken) != f.db.rotatedHash {
					t.Errorf("pair does not carry the committed refresh token: %+v", pair)
				}
			} else {
				if err == nil {
					t.Fatalf("refresh succeeded although the rotation did not land: %+v", pair)
				}
				if errors.Is(err, v1.ErrRefreshInvalid) {
					t.Errorf("a failed rotation was reported as an invalid token: %v", err)
				}
			}
			if len(f.db.familyBurns) != 0 || len(f.db.epochBumps) != 0 {
				t.Errorf("burned sessions: burns=%v bumps=%v", f.db.familyBurns, f.db.epochBumps)
			}
		})
	}
}

// The happy path still rotates and mints a full pair now that the tokens are
// minted before the compare-and-swap rather than after it.
func TestRefresh_RotatesAndMints(t *testing.T) {
	f := newStepUpFixture(t)
	f.db.sessionRow = gen.GetSessionByAnyTokenHashRow{ID: uuid.New(), UserID: f.userID, IsCurrent: true}
	f.db.rotateRows = 1

	pair, err := f.svc.Refresh(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if pair.AccessToken == "" || pair.AssetToken == "" || pair.RefreshToken == "" || pair.RefreshToken == "tok" {
		t.Errorf("incomplete pair: access=%t asset=%t refresh=%q",
			pair.AccessToken != "", pair.AssetToken != "", pair.RefreshToken)
	}
	if pair.UserID != f.userID || pair.Username != "alice" {
		t.Errorf("pair user = %s/%q, want %s/alice", pair.UserID, pair.Username, f.userID)
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
