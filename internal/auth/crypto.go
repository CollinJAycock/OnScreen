package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// Encryptor uses AES-256-GCM to encrypt/decrypt sensitive values at rest
// (webhook signing secrets) using the server's SECRET_KEY.
type Encryptor struct {
	gcm cipher.AEAD
}

// NewEncryptor creates an Encryptor from the 32-byte key derived from SECRET_KEY.
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryptor: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &Encryptor{gcm: gcm}, nil
}

// Encrypt encrypts plaintext with NO associated data and returns a
// base64-encoded ciphertext string. The nonce is prepended before encoding.
//
// Prefer [Encryptor.EncryptContext]. Without associated data a ciphertext is
// interchangeable between storage slots: it decrypts successfully wherever it
// is pasted, so anyone able to write the store can move a secret into a field
// that is echoed back and read it out. Retained for reading and re-writing
// legacy `encv1:` values.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	return e.seal(plaintext, nil)
}

// Decrypt decrypts a base64-encoded ciphertext produced by [Encryptor.Encrypt]
// (no associated data).
func (e *Encryptor) Decrypt(encoded string) (string, error) {
	return e.open(encoded, nil)
}

// EncryptContext encrypts plaintext bound to context, which is authenticated
// but not encrypted. Decryption succeeds only when the SAME context is
// supplied, so the ciphertext is cryptographically pinned to the slot it was
// written for — moving it to another row, key, or user makes it undecryptable
// rather than a working copy of the secret.
//
// context must be a stable identifier available at both encrypt and decrypt
// time: the settings key name, a user id, a row id. It is not secret.
func (e *Encryptor) EncryptContext(plaintext, context string) (string, error) {
	return e.seal(plaintext, []byte(context))
}

// DecryptContext decrypts a value produced by [Encryptor.EncryptContext].
// A context mismatch fails as an authentication error, indistinguishable from
// a corrupted ciphertext — which is the point.
func (e *Encryptor) DecryptContext(encoded, context string) (string, error) {
	return e.open(encoded, []byte(context))
}

func (e *Encryptor) seal(plaintext string, aad []byte) (string, error) {
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := e.gcm.Seal(nonce, nonce, []byte(plaintext), aad)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func (e *Encryptor) open(encoded string, aad []byte) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	nonceSize := e.gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := e.gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

// HashUsernameForLog returns an opaque, deterministic 16-hex-char digest of
// `username` keyed on `pepper`. Use it anywhere a raw username would otherwise
// land in a place we'd rather not store it verbatim — Valkey rate-limit
// keys, slog/audit entries, retained log archives.
//
// Why hash: the prior code used raw lowercased usernames in the Valkey
// keyspace and slog. Anyone who could SCAN Valkey or read operator logs
// could enumerate every username an attacker (or a confused legitimate user)
// had recently typed at the login form, which is leaking exactly the
// credential half a brute-force attacker already needs.
//
// HMAC-SHA256 keyed on `pepper` gives us:
//   - same input → same digest (so failure counters keep accumulating
//     across attempts within a window),
//   - different deployments → different digests (pepper is per-server),
//   - no offline dictionary attack against an exfiltrated digest unless
//     the pepper is also leaked.
//
// Truncating to 16 hex chars (64 bits) is plenty: collisions just merge
// two users' failure counters, the worst-case effect is one user getting
// a slightly tighter throttle than the other. Not a security concern.
//
// Username is lowercased and trimmed first so "Alice", "alice ", and "ALICE"
// all hash to the same value — matches the matching used elsewhere in auth.
func HashUsernameForLog(pepper []byte, username string) string {
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(strings.ToLower(strings.TrimSpace(username))))
	return hex.EncodeToString(mac.Sum(nil))[:16]
}

// DeriveKey32 returns a 32-byte AES key from a string SECRET_KEY value.
// It mirrors the decoding logic in config.validateSecretKey:
//   - hex-encoded (64 chars) -> decode to 32 bytes
//   - base64-encoded (>=43 chars) -> decode to 32 bytes
//   - raw bytes >= 32 -> first 32 bytes
//   - shorter values -> SHA-256 hash (fallback, should not happen with config validation)
func DeriveKey32(secret string) []byte {
	// Try hex decode (64 hex chars = 32 bytes).
	if len(secret) == 64 {
		b, err := hex.DecodeString(secret)
		if err == nil && len(b) == 32 {
			return b
		}
	}
	// Try base64 decode (44 chars with padding, or 43 without = 32 bytes).
	if len(secret) >= 43 {
		if b, err := base64.StdEncoding.DecodeString(secret); err == nil && len(b) == 32 {
			return b
		}
		if b, err := base64.RawStdEncoding.DecodeString(secret); err == nil && len(b) == 32 {
			return b
		}
	}
	// Raw bytes.
	b := []byte(secret)
	if len(b) >= 32 {
		return b[:32]
	}
	// Fallback: should never reach here because config validates len >= 32.
	sum := sha256.Sum256(b)
	return sum[:]
}
