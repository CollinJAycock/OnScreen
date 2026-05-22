package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestGenerateTOTPSecret_ValidatesOwnCode(t *testing.T) {
	secret, url, err := GenerateTOTPSecret("alice")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	if secret == "" {
		t.Fatal("empty secret")
	}
	// otpauth URI must name the issuer + account so authenticators label
	// the entry "OnScreen (alice)".
	if !strings.HasPrefix(url, "otpauth://totp/") {
		t.Errorf("url = %q, want otpauth://totp/ prefix", url)
	}
	if !strings.Contains(url, "issuer=OnScreen") {
		t.Errorf("url = %q, want issuer=OnScreen", url)
	}

	// A code generated from the secret right now must validate.
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	if !ValidateTOTPCode(code, secret) {
		t.Errorf("ValidateTOTPCode rejected a freshly generated code %q", code)
	}
	// Whitespace around the code is tolerated (users paste with spaces).
	if !ValidateTOTPCode("  "+code+"  ", secret) {
		t.Errorf("ValidateTOTPCode should trim surrounding whitespace")
	}
}

func TestValidateTOTPCode_RejectsBadInput(t *testing.T) {
	secret, _, err := GenerateTOTPSecret("bob")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	for _, bad := range []string{"", "notacode", "000000", "12345"} {
		// "000000" has a ~1e-6 chance of being valid at this instant;
		// accept that astronomically rare flake rather than mocking time.
		if bad == "000000" {
			if code, _ := totp.GenerateCode(secret, time.Now()); code == "000000" {
				continue
			}
		}
		if ValidateTOTPCode(bad, secret) {
			t.Errorf("ValidateTOTPCode(%q) = true, want false", bad)
		}
	}
}

func TestGenerateRecoveryCodes(t *testing.T) {
	display, hashes, err := GenerateRecoveryCodes(RecoveryCodeCount)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(display) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatalf("got %d display / %d hashes, want %d each", len(display), len(hashes), RecoveryCodeCount)
	}

	seen := make(map[string]bool)
	for i, d := range display {
		// Display form is XXXXX-XXXXX.
		if len(d) != 11 || d[5] != '-' {
			t.Errorf("code %d = %q, want XXXXX-XXXXX", i, d)
		}
		if seen[d] {
			t.Errorf("duplicate recovery code %q", d)
		}
		seen[d] = true
		// The stored hash must match hashing the normalised user input.
		if HashToken(NormalizeRecoveryCode(d)) != hashes[i] {
			t.Errorf("hash mismatch for code %q", d)
		}
		// A different code must not collide with this hash.
		if HashToken(NormalizeRecoveryCode("ZZZZZ-ZZZZZ")) == hashes[i] {
			t.Errorf("unexpected hash collision")
		}
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"abcde-fghjk", "ABCDEFGHJK"},
		{"ABCDE FGHJK", "ABCDEFGHJK"},
		{"ABCDEFGHJK", "ABCDEFGHJK"},
		{"  a1b2c3  ", "A1B2C3"},
		{"!!!", ""},
	} {
		if got := NormalizeRecoveryCode(tc.in); got != tc.want {
			t.Errorf("NormalizeRecoveryCode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
