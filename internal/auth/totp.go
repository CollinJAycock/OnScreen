package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"image/png"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/hotp"
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

// RenderTOTPQRPNG turns an otpauth:// URI into a PNG QR code (256×256).
// Rendering server-side (via the same pquerna/otp lib that minted the
// secret) means native clients don't each need to ship a QR encoder —
// they show the bytes directly. Web renders its own QR from the URL, so
// it ignores this.
func RenderTOTPQRPNG(otpauthURL string) ([]byte, error) {
	key, err := otp.NewKeyFromURL(otpauthURL)
	if err != nil {
		return nil, fmt.Errorf("parse otpauth url: %w", err)
	}
	img, err := key.Image(256, 256)
	if err != nil {
		return nil, fmt.Errorf("render qr: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

// ValidateTOTPCode reports whether code is valid for secret right now.
// totp.Validate applies a ±1 step (±30 s) skew window, covering ordinary
// clock drift between the server and the user's phone.
//
// NOT single-use on its own: it is pure and records nothing, so the same six
// digits validate repeatedly for up to ~90 s. Every account-facing path (login
// second factor, step-up reauth, disable, enrolment confirm) must instead go
// through MatchTOTPStep + a TOTPReplayGuard, which spends the matched time step
// so an observed code cannot be replayed.
func ValidateTOTPCode(code, secret string) bool {
	return totp.Validate(strings.TrimSpace(code), secret)
}

// totpPeriod / totpSkew mirror totp.Validate's defaults (and what
// GenerateTOTPSecret enrols): 30 s steps, ±1 step of clock-drift tolerance.
const (
	totpPeriod = 30
	totpSkew   = 1
)

// MatchTOTPStep reports which RFC 6238 time step (counter) code is valid for,
// searching the same ±1-step window totp.Validate accepts. ok=false when the
// code matches no step in the window. The step is what a TOTPReplayGuard
// spends to make the code single-use.
func MatchTOTPStep(code, secret string, now time.Time) (step int64, ok bool) {
	code = strings.TrimSpace(code)
	if len(code) != otp.DigitsSix.Length() {
		return 0, false
	}
	current := now.Unix() / totpPeriod
	opts := hotp.ValidateOpts{Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
	for off := int64(-totpSkew); off <= totpSkew; off++ {
		c := current + off
		if c < 0 {
			continue
		}
		// hotp.ValidateCustom compares in constant time.
		if valid, err := hotp.ValidateCustom(code, uint64(c), secret, opts); err == nil && valid {
			return c, true
		}
	}
	return 0, false
}

// TOTPReplayGuard records the TOTP time steps a user has already spent.
// ConsumeTOTPStep must atomically refuse any step <= the user's last consumed
// step and otherwise record it — no check-then-set window in which two
// concurrent submissions of the same code both pass.
//
// Implemented by valkey.TOTPReplayGuard (shared across instances) and
// MemoryTOTPReplayGuard (per process; fallback when Valkey is unreachable).
type TOTPReplayGuard interface {
	ConsumeTOTPStep(ctx context.Context, userID uuid.UUID, step int64) (fresh bool, err error)
}

// memoryStepTTL matches the Valkey guard: after four periods every step that
// can still validate is newer than the remembered one.
const memoryStepTTL = 4 * totpPeriod * time.Second

// MemoryTOTPReplayGuard is an in-process TOTPReplayGuard: a mutex-protected map
// of user → last consumed step with expiry. The zero value is ready to use.
type MemoryTOTPReplayGuard struct {
	mu        sync.Mutex
	last      map[uuid.UUID]memoryStep
	lastSweep time.Time
	now       func() time.Time // nil = time.Now (tests override)
}

type memoryStep struct {
	step      int64
	expiresAt time.Time
}

// ConsumeTOTPStep implements TOTPReplayGuard. Never returns an error.
func (g *MemoryTOTPReplayGuard) ConsumeTOTPStep(_ context.Context, userID uuid.UUID, step int64) (bool, error) {
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
		g.last = make(map[uuid.UUID]memoryStep)
	}
	// Bound memory: sweep expired entries at most once per TTL.
	if now.Sub(g.lastSweep) >= memoryStepTTL {
		for k, e := range g.last {
			if !now.Before(e.expiresAt) {
				delete(g.last, k)
			}
		}
		g.lastSweep = now
	}
	if e, ok := g.last[userID]; ok && now.Before(e.expiresAt) && step <= e.step {
		return false, nil
	}
	g.last[userID] = memoryStep{step: step, expiresAt: now.Add(memoryStepTTL)}
	return true, nil
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
