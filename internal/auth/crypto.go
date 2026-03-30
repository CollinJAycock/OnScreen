package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
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

// Encrypt encrypts plaintext and returns a base64-encoded ciphertext string.
// The nonce is prepended to the ciphertext before encoding.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts a base64-encoded ciphertext produced by Encrypt.
func (e *Encryptor) Decrypt(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	nonceSize := e.gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := e.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
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
