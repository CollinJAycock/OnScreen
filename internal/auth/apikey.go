package auth

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

// Personal API keys (migration 00050) are long-lived credentials for scripts
// and integrations. The wire format is "osk_" followed by 43 base62 characters
// encoding 32 random bytes (256 bits) — recognisable in logs and by secret
// scanners, and far beyond online guessing. Only HashAPIKey's SHA-256 of the
// whole string is stored; APIKeyDisplayPrefix's first few characters are kept
// in clear so a user can tell their keys apart.
const (
	APIKeyPrefix = "osk_"
	// apiKeyBodyLen is the base62 length of 32 bytes: ceil(256 / log2(62)).
	apiKeyBodyLen = 43
	// apiKeyDisplayLen is how much of the body APIKeyDisplayPrefix keeps.
	apiKeyDisplayLen = 8

	// APIKeyScopeFull keys may do anything their user can, except the
	// credential and account-security endpoints no key may reach.
	APIKeyScopeFull = "full"
	// APIKeyScopeReadOnly keys are refused on every method but GET, HEAD and
	// OPTIONS.
	APIKeyScopeReadOnly = "read_only"
)

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// GenerateAPIKey returns a new key, the hash to store and the display prefix.
func GenerateAPIKey() (raw, hash, display string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", "", fmt.Errorf("generate api key: %w", err)
	}
	raw = APIKeyPrefix + encodeBase62(b[:], apiKeyBodyLen)
	return raw, HashAPIKey(raw), APIKeyDisplayPrefix(raw), nil
}

// HashAPIKey is the SHA-256 hex digest stored for a key and looked up per
// request. A plain (unsalted, fast) hash is right here: the input is 256 bits
// of randomness, so there is nothing for a slow hash to protect, and the
// lookup must be an index probe.
func HashAPIKey(raw string) string { return HashToken(raw) }

// APIKeyDisplayPrefix is the part of a key shown in key lists ("osk_AbC12xYz").
func APIKeyDisplayPrefix(raw string) string {
	if len(raw) <= len(APIKeyPrefix)+apiKeyDisplayLen {
		return raw
	}
	return raw[:len(APIKeyPrefix)+apiKeyDisplayLen]
}

// LooksLikeAPIKey reports whether s has the shape of a personal API key. The
// auth middleware uses it to tell `Authorization: Bearer osk_…` apart from a
// PASETO bearer token; it says nothing about whether the key exists.
func LooksLikeAPIKey(s string) bool {
	if !strings.HasPrefix(s, APIKeyPrefix) {
		return false
	}
	body := s[len(APIKeyPrefix):]
	if len(body) != apiKeyBodyLen {
		return false
	}
	for i := 0; i < len(body); i++ {
		if strings.IndexByte(base62Alphabet, body[i]) < 0 {
			return false
		}
	}
	return true
}

// ValidAPIKeyScope reports whether scope is one a key may carry.
func ValidAPIKeyScope(scope string) bool {
	return scope == APIKeyScopeFull || scope == APIKeyScopeReadOnly
}

// encodeBase62 renders b as a base62 string left-padded with '0' to width.
func encodeBase62(b []byte, width int) string {
	n := new(big.Int).SetBytes(b)
	base := big.NewInt(62)
	mod := new(big.Int)
	out := make([]byte, 0, width)
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		out = append(out, base62Alphabet[mod.Int64()])
	}
	for len(out) < width {
		out = append(out, '0')
	}
	// Digits were produced least-significant first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}
