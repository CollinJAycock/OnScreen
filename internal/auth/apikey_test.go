package auth

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestGenerateAPIKey_Format(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		raw, hash, display, err := GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey: %v", err)
		}
		if !strings.HasPrefix(raw, "osk_") || len(raw) != len("osk_")+43 {
			t.Fatalf("unexpected key shape %q", raw)
		}
		if !LooksLikeAPIKey(raw) {
			t.Fatalf("generated key not recognised: %q", raw)
		}
		if hash != HashAPIKey(raw) || hash == raw || len(hash) != 64 {
			t.Fatalf("hash %q is not the SHA-256 hex of the key", hash)
		}
		if display != raw[:12] || !strings.HasPrefix(raw, display) {
			t.Fatalf("display prefix %q of %q", display, raw)
		}
		if seen[raw] {
			t.Fatal("duplicate key generated")
		}
		seen[raw] = true
	}
}

func TestLooksLikeAPIKey(t *testing.T) {
	good := "osk_" + strings.Repeat("aZ9", 14) + "x" // 43 base62 chars
	cases := map[string]bool{
		good:                                   true,
		"":                                     false,
		"osk_":                                 false,
		"osk_short":                            false,
		"OSK_" + good[4:]:                      false,
		good + "y":                             false,
		"osk_" + strings.Repeat("a", 42) + "-": false, // not base62
		"v4.local.abcdef":                      false, // a PASETO token
	}
	for in, want := range cases {
		if got := LooksLikeAPIKey(in); got != want {
			t.Errorf("LooksLikeAPIKey(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestValidAPIKeyScope(t *testing.T) {
	if !ValidAPIKeyScope("full") || !ValidAPIKeyScope("read_only") || ValidAPIKeyScope("admin") || ValidAPIKeyScope("") {
		t.Fatal("scope validation wrong")
	}
}

// A stream token minted by a key-authenticated request carries the key id,
// so the middleware can kill it when the key is deleted; a normal one doesn't.
func TestStreamToken_CarriesAPIKeyID(t *testing.T) {
	tm, err := NewTokenMaker(DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	keyID := uuid.New()
	tok, err := tm.IssueStreamToken(Claims{UserID: uuid.New(), APIKeyID: keyID, APIKeyScope: APIKeyScopeFull}, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	c, err := tm.ValidateAccessToken(tok)
	if err != nil {
		t.Fatal(err)
	}
	if c.APIKeyID != keyID || !c.IsAPIKey() {
		t.Fatalf("akid not round-tripped: %v", c.APIKeyID)
	}
	if c.APIKeyScope != "" {
		t.Fatal("scope must not be written into a token")
	}

	plain, _ := tm.IssueAccessToken(Claims{UserID: uuid.New()})
	pc, _ := tm.ValidateAccessToken(plain)
	if pc.IsAPIKey() {
		t.Fatal("session token reported as an API key")
	}
	var nilClaims *Claims
	if nilClaims.IsAPIKey() {
		t.Fatal("nil claims reported as an API key")
	}
}
