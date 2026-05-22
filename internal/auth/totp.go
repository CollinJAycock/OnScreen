package auth

import (
	"crypto/rand"
	"fmt"
	"strings"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// TOTPIssuer is the label shown in authenticator apps (Google
// Authenticator, Authy, 1Password, …) — "OnScreen (alice)".
const TOTPIssuer = "OnScreen"

// RecoveryCodeCount is how many single-use recovery codes we mint when a
// user activates TOTP. 10 matches what GitHub / Google hand out.
const RecoveryCodeCount = 10

// recoveryAlphabet excludes visually ambiguous characters (0/O, 1/I/L)
// so a code read off a printout doesn't get mistyped.
const recoveryAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// GenerateTOTPSecret creates a fresh TOTP key for accountName. Returns
// the base32 secret (to encrypt + store, and to show for manual entry)
// and the otpauth:// URI the client renders as a QR code. SHA1 / 6
// digits / 30 s — the universal defaults every authenticator app
// supports without extra configuration.
func GenerateTOTPSecret(accountName string) (secret, otpauthURL string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      TOTPIssuer,
		AccountName: accountName,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", fmt.Errorf("generate totp secret: %w", err)
	}
	return key.Secret(), key.URL(), nil
}

// ValidateTOTPCode reports whether code is valid for secret right now.
// totp.Validate applies a ±1 step (±30 s) skew window, covering ordinary
// clock drift between the server and the user's phone.
func ValidateTOTPCode(code, secret string) bool {
	return totp.Validate(strings.TrimSpace(code), secret)
}

// GenerateRecoveryCodes returns n random single-use codes: `display`
// holds the human-readable XXXXX-XXXXX form shown to the user exactly
// once, and `hashes` holds their SHA-256 digests for storage. The two
// slices are positionally aligned. Verify by NormalizeRecoveryCode +
// HashToken on user input and matching against a stored hash.
func GenerateRecoveryCodes(n int) (display, hashes []string, err error) {
	display = make([]string, 0, n)
	hashes = make([]string, 0, n)
	for range n {
		raw, err := randomRecoveryCode()
		if err != nil {
			return nil, nil, err
		}
		display = append(display, raw[:5]+"-"+raw[5:])
		hashes = append(hashes, HashToken(raw))
	}
	return display, hashes, nil
}

// NormalizeRecoveryCode canonicalises user-entered input to the form
// GenerateRecoveryCodes hashed: uppercase, with dashes/whitespace
// stripped. So "abcde-fghjk", "ABCDE FGHJK", and "ABCDEFGHJK" all hash
// to the same value.
func NormalizeRecoveryCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// randomRecoveryCode returns a 10-char code drawn uniformly from
// recoveryAlphabet (~49 bits of entropy). Rejection sampling keeps the
// distribution flat (no modulo bias).
func randomRecoveryCode() (string, error) {
	const n = 10
	out := make([]byte, n)
	buf := make([]byte, 1)
	for i := 0; i < n; {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("read random: %w", err)
		}
		// 256 % 31 != 0, so discard the top values that would bias the mod.
		max := byte(len(recoveryAlphabet) * (256 / len(recoveryAlphabet)))
		if buf[0] >= max {
			continue
		}
		out[i] = recoveryAlphabet[int(buf[0])%len(recoveryAlphabet)]
		i++
	}
	return string(out), nil
}
