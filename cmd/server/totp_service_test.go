//go:build integration

// End-to-end TOTP flow against a real Postgres: setup stages an
// encrypted secret, activate enables + mints recovery codes, login gates
// on the second factor, verify completes it, recovery codes are
// single-use, and disable wipes everything. Exercises the real sqlc
// queries + AES-256-GCM secret round-trip the unit tests stub out.
//
// Run with: go test -tags=integration ./cmd/server/...
package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestTOTP_Integration_FullFlow(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	key := auth.DeriveKey32("test-secret-key-that-is-32-bytes!")
	tm, err := auth.NewTokenMaker(key)
	if err != nil {
		t.Fatalf("NewTokenMaker: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	svc := &authService{db: q, tokens: tm, enc: enc, logger: slog.Default()}

	// Seed a local password account.
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	hs := string(hash)
	user, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "totp-user", PasswordHash: &hs})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Login before any 2FA → ordinary token pair, no gate.
	pre, err := svc.LoginLocal(ctx, "totp-user", "correct horse")
	if err != nil {
		t.Fatalf("LoginLocal (pre-2fa): %v", err)
	}
	if pre.TOTPRequired || pre.AccessToken == "" {
		t.Fatalf("pre-2FA login should issue a normal pair, got TOTPRequired=%v access=%q", pre.TOTPRequired, pre.AccessToken)
	}

	// 1. Setup stages a secret but does NOT enable.
	url, secret, err := svc.SetupTOTP(ctx, user.ID, user.Username)
	if err != nil {
		t.Fatalf("SetupTOTP: %v", err)
	}
	if secret == "" || url == "" {
		t.Fatal("SetupTOTP returned empty secret/url")
	}
	if u, _ := q.GetUser(ctx, user.ID); u.TotpEnabled {
		t.Fatal("totp_enabled must stay false until activation")
	}

	// Setup again while pending is fine; setup after enabled is rejected
	// (tested below).

	// 2. Activate with a live code → enabled + recovery codes.
	code, _ := totp.GenerateCode(secret, time.Now())
	codes, err := svc.ActivateTOTP(ctx, user.ID, code)
	if err != nil {
		t.Fatalf("ActivateTOTP: %v", err)
	}
	if len(codes) != auth.RecoveryCodeCount {
		t.Fatalf("got %d recovery codes, want %d", len(codes), auth.RecoveryCodeCount)
	}
	if u, _ := q.GetUser(ctx, user.ID); !u.TotpEnabled {
		t.Fatal("totp_enabled must be true after activation")
	}

	// Setup now must refuse (already enabled).
	if _, _, err := svc.SetupTOTP(ctx, user.ID, user.Username); err != v1.ErrTOTPAlreadyEnabled {
		t.Fatalf("SetupTOTP after enable = %v, want ErrTOTPAlreadyEnabled", err)
	}

	// 3. Login now demands the second factor.
	gated, err := svc.LoginLocal(ctx, "totp-user", "correct horse")
	if err != nil {
		t.Fatalf("LoginLocal (2fa): %v", err)
	}
	if !gated.TOTPRequired || gated.LoginChallengeToken == "" || gated.AccessToken != "" {
		t.Fatalf("2FA login should gate: TOTPRequired=%v challenge=%q access=%q",
			gated.TOTPRequired, gated.LoginChallengeToken, gated.AccessToken)
	}

	// 4. Verify with a wrong code → ErrBadTOTPCode.
	if _, err := svc.VerifyTOTPLogin(ctx, gated.LoginChallengeToken, "999999"); err == nil {
		t.Error("VerifyTOTPLogin accepted a wrong code")
	}

	// 5. Verify with a live code → real token pair.
	code2, _ := totp.GenerateCode(secret, time.Now())
	ok, err := svc.VerifyTOTPLogin(ctx, gated.LoginChallengeToken, code2)
	if err != nil {
		t.Fatalf("VerifyTOTPLogin (good code): %v", err)
	}
	if ok.AccessToken == "" || ok.RefreshToken == "" {
		t.Fatal("verify should issue a full token pair")
	}

	// 6. A bad challenge token is rejected.
	if _, err := svc.VerifyTOTPLogin(ctx, "not-a-token", code2); err != v1.ErrInvalidTOTPChallenge {
		t.Errorf("bad challenge = %v, want ErrInvalidTOTPChallenge", err)
	}

	// 7. Recovery code is single-use.
	g2, _ := svc.LoginLocal(ctx, "totp-user", "correct horse")
	if _, err := svc.VerifyTOTPLogin(ctx, g2.LoginChallengeToken, codes[0]); err != nil {
		t.Fatalf("VerifyTOTPLogin with recovery code: %v", err)
	}
	g3, _ := svc.LoginLocal(ctx, "totp-user", "correct horse")
	if _, err := svc.VerifyTOTPLogin(ctx, g3.LoginChallengeToken, codes[0]); err == nil {
		t.Error("a recovery code must not be reusable")
	}
	if _, remaining, _ := svc.TOTPStatus(ctx, user.ID); remaining != auth.RecoveryCodeCount-1 {
		t.Errorf("recovery remaining = %d, want %d", remaining, auth.RecoveryCodeCount-1)
	}

	// 8. Disable wipes the secret + recovery codes.
	code3, _ := totp.GenerateCode(secret, time.Now())
	if err := svc.DisableTOTP(ctx, user.ID, code3); err != nil {
		t.Fatalf("DisableTOTP: %v", err)
	}
	u, _ := q.GetUser(ctx, user.ID)
	if u.TotpEnabled || u.TotpSecret != nil {
		t.Errorf("after disable: enabled=%v secret=%v, want false/nil", u.TotpEnabled, u.TotpSecret)
	}
	if n, _ := q.CountUnusedTOTPRecoveryCodes(ctx, user.ID); n != 0 {
		t.Errorf("recovery codes remaining after disable = %d, want 0", n)
	}

	// Login is back to a normal single-step pair.
	post, err := svc.LoginLocal(ctx, "totp-user", "correct horse")
	if err != nil {
		t.Fatalf("LoginLocal (post-disable): %v", err)
	}
	if post.TOTPRequired || post.AccessToken == "" {
		t.Error("after disable, login should issue a normal pair")
	}
}
