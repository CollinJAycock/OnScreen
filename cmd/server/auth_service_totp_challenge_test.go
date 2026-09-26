package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"aidanwoods.dev/go-paseto"
	"github.com/google/uuid"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/valkey"
)

// challengeRecoveryCode is the one recovery code challengeFakeDB accepts. The
// fake never marks it used, so tests can present a valid second factor as
// often as they like and isolate the challenge's own single-use rule.
const challengeRecoveryCode = "ABCDE-FGHJK"

// challengeFakeDB adds the login-side queries (password step, session
// creation) to stepUpFakeDB.
type challengeFakeDB struct {
	*stepUpFakeDB
	sessions atomic.Int64
}

func (d *challengeFakeDB) GetUserByUsername(ctx context.Context, username string) (gen.User, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, u := range d.users {
		if u.Username == username {
			return u, nil
		}
	}
	return gen.User{}, errors.New("no such user")
}

func (d *challengeFakeDB) CreateSession(context.Context, gen.CreateSessionParams) (gen.Session, error) {
	d.sessions.Add(1)
	return gen.Session{ID: uuid.New()}, nil
}

func (d *challengeFakeDB) ConsumeTOTPRecoveryCode(_ context.Context, arg gen.ConsumeTOTPRecoveryCodeParams) (int64, error) {
	if arg.CodeHash == auth.HashToken(auth.NormalizeRecoveryCode(challengeRecoveryCode)) {
		return 1, nil
	}
	return 0, nil
}

func (d *challengeFakeDB) bumpEpoch(id uuid.UUID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	u := d.users[id]
	u.SessionEpoch++
	d.users[id] = u
}

func newChallengeFixture(t *testing.T) (*stepUpFixture, *challengeFakeDB) {
	t.Helper()
	f := newStepUpFixture(t)
	db := &challengeFakeDB{stepUpFakeDB: f.db}
	f.svc.db = db
	vc, err := valkey.New(context.Background(), "redis://"+f.mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vc.Close() })
	f.svc.totpChallenges = valkey.NewTOTPChallengeGuard(vc)
	return f, db
}

// login runs the password step and returns the TOTP challenge it mints.
func (f *stepUpFixture) login(t *testing.T) string {
	t.Helper()
	pair, err := f.svc.LoginLocal(context.Background(), "alice", stepUpPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !pair.TOTPRequired || pair.LoginChallengeToken == "" || pair.AccessToken != "" {
		t.Fatalf("password step: %+v, want a bare TOTP challenge", pair)
	}
	return pair.LoginChallengeToken
}

// A redeemed challenge must never mint a second session, even with a fresh,
// unspent code (live test: re-verify of a used challenge -> 200 new session).
func TestVerifyTOTPLogin_ChallengeIsSingleUse(t *testing.T) {
	f, db := newChallengeFixture(t)
	ctx := context.Background()
	challenge := f.login(t)

	if _, err := f.svc.VerifyTOTPLogin(ctx, challenge, f.code(t)); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	f.nextStep()
	if _, err := f.svc.VerifyTOTPLogin(ctx, challenge, f.code(t)); !errors.Is(err, v1.ErrInvalidTOTPChallenge) {
		t.Fatalf("re-verify with a fresh code: got %v, want ErrInvalidTOTPChallenge", err)
	}
	if _, err := f.svc.VerifyTOTPLogin(ctx, challenge, challengeRecoveryCode); !errors.Is(err, v1.ErrInvalidTOTPChallenge) {
		t.Fatalf("re-verify with a recovery code: got %v, want ErrInvalidTOTPChallenge", err)
	}
	if n := db.sessions.Load(); n != 1 {
		t.Fatalf("one challenge minted %d sessions, want 1", n)
	}
	// A new password step gets a new challenge that works.
	if _, err := f.svc.VerifyTOTPLogin(ctx, f.login(t), challengeRecoveryCode); err != nil {
		t.Fatalf("fresh challenge: %v", err)
	}
}

// A wrong code must not burn the challenge — the per-user failure cap is what
// bounds guessing, and a typo shouldn't force the user back to the password.
func TestVerifyTOTPLogin_WrongCodeDoesNotBurnChallenge(t *testing.T) {
	f, db := newChallengeFixture(t)
	ctx := context.Background()
	challenge := f.login(t)

	if _, err := f.svc.VerifyTOTPLogin(ctx, challenge, "000000"); !errors.Is(err, v1.ErrBadTOTPCode) {
		t.Fatalf("wrong code: got %v, want ErrBadTOTPCode", err)
	}
	if _, err := f.svc.VerifyTOTPLogin(ctx, challenge, f.code(t)); err != nil {
		t.Fatalf("right code after a typo: %v", err)
	}
	if n := db.sessions.Load(); n != 1 {
		t.Fatalf("sessions = %d, want 1", n)
	}
}

// A session-epoch bump between the two steps (admin password reset,
// force-logout, demote) revokes the half-finished login too (live test: a
// pre-reset challenge + a current code still minted a full session).
func TestVerifyTOTPLogin_EpochBumpRevokesChallenge(t *testing.T) {
	f, db := newChallengeFixture(t)
	ctx := context.Background()
	challenge := f.login(t)
	code := f.code(t)

	db.bumpEpoch(f.userID)
	if _, err := f.svc.VerifyTOTPLogin(ctx, challenge, code); !errors.Is(err, v1.ErrInvalidTOTPChallenge) {
		t.Fatalf("pre-bump challenge: got %v, want ErrInvalidTOTPChallenge", err)
	}
	if n := db.sessions.Load(); n != 0 {
		t.Fatalf("revoked challenge minted %d sessions", n)
	}
	// The refusal happens before the code is checked, so the code isn't
	// spent: a challenge from a fresh password step accepts it.
	if _, err := f.svc.VerifyTOTPLogin(ctx, f.login(t), code); err != nil {
		t.Fatalf("post-bump challenge: %v", err)
	}
}

// Concurrent verifies of one challenge, each with a valid second factor: the
// burn is atomic, so exactly one mints a session.
func TestVerifyTOTPLogin_ConcurrentRedeemOneWinner(t *testing.T) {
	f, db := newChallengeFixture(t)
	f.svc.rateLimiter = nil // every attempt here is a success; keep the cap out of it
	challenge := f.login(t)

	var wins atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := f.svc.VerifyTOTPLogin(context.Background(), challenge, challengeRecoveryCode); err == nil {
				wins.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 || db.sessions.Load() != 1 {
		t.Fatalf("wins = %d, sessions = %d; want 1 and 1", wins.Load(), db.sessions.Load())
	}
}

type erroringChallengeGuard struct{}

func (erroringChallengeGuard) RedeemTOTPChallenge(context.Context, string, time.Duration) (bool, error) {
	return false, errors.New("valkey down")
}

// With the shared guard unavailable, the in-process record still makes the
// challenge single-use on this instance.
func TestVerifyTOTPLogin_ChallengeBurnFallsBackToInProcessGuard(t *testing.T) {
	f, db := newChallengeFixture(t)
	f.svc.totpChallenges = erroringChallengeGuard{}
	ctx := context.Background()
	challenge := f.login(t)

	if _, err := f.svc.VerifyTOTPLogin(ctx, challenge, challengeRecoveryCode); err != nil {
		t.Fatalf("first verify with guard down: %v", err)
	}
	if _, err := f.svc.VerifyTOTPLogin(ctx, challenge, challengeRecoveryCode); !errors.Is(err, v1.ErrInvalidTOTPChallenge) {
		t.Fatalf("second verify with guard down: got %v, want ErrInvalidTOTPChallenge", err)
	}
	if n := db.sessions.Load(); n != 1 {
		t.Fatalf("sessions = %d, want 1", n)
	}
}

// A challenge minted before this change (no jti) can't be burned, so it is
// refused outright rather than left multi-use for the rest of its TTL.
func TestVerifyTOTPLogin_RejectsChallengeWithoutJTI(t *testing.T) {
	f, db := newChallengeFixture(t)
	key, err := paseto.V4SymmetricKeyFromBytes(auth.DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	tok := paseto.NewToken()
	tok.SetIssuedAt(time.Now())
	tok.SetNotBefore(time.Now())
	tok.SetExpiration(time.Now().Add(auth.TOTPChallengeTTL))
	tok.SetString("user_id", f.userID.String())
	tok.SetString("purpose", "totp_challenge")
	legacy := tok.V4Encrypt(key, nil)

	if _, err := f.svc.VerifyTOTPLogin(context.Background(), legacy, challengeRecoveryCode); !errors.Is(err, v1.ErrInvalidTOTPChallenge) {
		t.Fatalf("legacy challenge: got %v, want ErrInvalidTOTPChallenge", err)
	}
	if n := db.sessions.Load(); n != 0 {
		t.Fatalf("legacy challenge minted %d sessions", n)
	}
}
