package main

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/notifyagents"
	"github.com/onscreen/onscreen/internal/scrobble"
)

func mustEnc(t *testing.T, key string) *auth.Encryptor {
	t.Helper()
	e, err := auth.NewEncryptor(auth.DeriveKey32(key))
	if err != nil {
		t.Fatalf("encryptor: %v", err)
	}
	return e
}

// A raw (no-prefix) secret — webhook / TOTP shape — round-trips to the new key
// and must NOT gain an encv1: prefix.
func TestReEncrypt_RawRoundTrips(t *testing.T) {
	old := mustEnc(t, strings.Repeat("a", 32))
	nw := mustEnc(t, strings.Repeat("b", 32))

	stored, err := old.Encrypt("hunter2")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	out, ok := reEncrypt(old, nw, stored)
	if !ok {
		t.Fatal("expected ok")
	}
	if strings.HasPrefix(out, encPrefix) {
		t.Error("raw value must not gain a prefix")
	}
	got, err := nw.Decrypt(out)
	if err != nil || got != "hunter2" {
		t.Errorf("new-key decrypt: got %q err %v", got, err)
	}
}

// A legacy encv1: settings value re-keys AND upgrades to the v2 envelope.
// Rotation is the upgrade path: a rotated row must come out in the current
// format, bound to its key name, not be rewritten in the weaker one.
func TestReEncryptSetting_UpgradesV1ToV2(t *testing.T) {
	old := mustEnc(t, strings.Repeat("a", 32))
	nw := mustEnc(t, strings.Repeat("b", 32))

	ct, err := old.Encrypt(`{"client_secret":"x"}`)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	out, ok := reEncryptSetting(old, nw, "oidc_config", encPrefix+ct)
	if !ok {
		t.Fatal("expected ok")
	}
	if !strings.HasPrefix(out, encPrefixV2) {
		t.Fatalf("rotated settings row must be upgraded to %s, got %q", encPrefixV2, out[:12])
	}
	got, err := nw.DecryptContext(strings.TrimPrefix(out, encPrefixV2), "oidc_config")
	if err != nil || got != `{"client_secret":"x"}` {
		t.Errorf("decrypt: got %q err %v", got, err)
	}
	// The upgraded value must be pinned to its key.
	if _, err := nw.DecryptContext(strings.TrimPrefix(out, encPrefixV2), "tmdb_api_key"); err == nil {
		t.Error("upgraded value still decrypts under a foreign settings key")
	}
}

// An already-v2 value re-keys and stays v2, decrypting only under its own key.
func TestReEncryptSetting_V2RoundTrips(t *testing.T) {
	old := mustEnc(t, strings.Repeat("a", 32))
	nw := mustEnc(t, strings.Repeat("b", 32))

	ct, err := old.EncryptContext("tok", "tmdb_api_key")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	out, ok := reEncryptSetting(old, nw, "tmdb_api_key", encPrefixV2+ct)
	if !ok {
		t.Fatal("expected ok")
	}
	got, err := nw.DecryptContext(strings.TrimPrefix(out, encPrefixV2), "tmdb_api_key")
	if err != nil || got != "tok" {
		t.Errorf("decrypt: got %q err %v", got, err)
	}
}

// A v2 value whose stored key does not match must be skipped, never clobbered
// — the same safety property TestReEncrypt_WrongOldKeySkips guards for keys.
func TestReEncryptSetting_WrongKeyContextSkips(t *testing.T) {
	old := mustEnc(t, strings.Repeat("a", 32))
	nw := mustEnc(t, strings.Repeat("b", 32))

	ct, _ := old.EncryptContext("tok", "tmdb_api_key")
	if _, ok := reEncryptSetting(old, nw, "tvdb_api_key", encPrefixV2+ct); ok {
		t.Fatal("a context mismatch must skip, not rotate")
	}
}

// A value the supplied old key can't decrypt must be skipped, never clobbered —
// this is what makes a wrong OLD_SECRET_KEY safe (reports rotated=0).
func TestReEncrypt_WrongOldKeySkips(t *testing.T) {
	actual := mustEnc(t, strings.Repeat("c", 32)) // value encrypted with this key
	old := mustEnc(t, strings.Repeat("a", 32))    // but we try a different "old" key
	nw := mustEnc(t, strings.Repeat("b", 32))

	stored, err := actual.Encrypt("secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, ok := reEncrypt(old, nw, stored); ok {
		t.Error("must NOT rotate a value the old key can't decrypt")
	}
}

// Notification agent credentials are bound to their row id: they re-seal to
// the new key under the same id, and a ciphertext moved to another row (or
// opened with the wrong old key) is skipped, never rewritten.
func TestReEncryptAgentSecret(t *testing.T) {
	old := mustEnc(t, strings.Repeat("a", 32))
	nw := mustEnc(t, strings.Repeat("b", 32))
	id, other := uuid.New(), uuid.New()

	stored, err := old.EncryptContext("tk_secret", notifyagents.SecretContext(id))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	out, ok := reEncryptAgentSecret(old, nw, id, stored)
	if !ok {
		t.Fatal("expected the agent secret to rotate")
	}
	if got, err := nw.DecryptContext(out, notifyagents.SecretContext(id)); err != nil || got != "tk_secret" {
		t.Fatalf("new key should open it for the same row: %q, %v", got, err)
	}
	if _, ok := reEncryptAgentSecret(old, nw, other, stored); ok {
		t.Error("a secret sealed for another row must be skipped")
	}
	if _, ok := reEncryptAgentSecret(nw, old, id, stored); ok {
		t.Error("a wrong old key must be skipped")
	}
}

// Last.fm / Trakt credentials are bound to their column and user: each bound
// column rotates under the same (column, user), and a ciphertext moved to
// another user or another column is skipped, never rewritten.
func TestReSealScrobbleCredential(t *testing.T) {
	old := mustEnc(t, strings.Repeat("a", 32))
	nw := mustEnc(t, strings.Repeat("b", 32))
	user, other := uuid.New(), uuid.New()

	for _, col := range scrobble.BoundColumns {
		stored, err := old.EncryptContext("sk-"+col, scrobble.CredentialContext(col, user))
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		out, ok := reSealScrobbleCredential(old, nw, col, user, stored)
		if !ok {
			t.Fatalf("%s: expected the credential to rotate", col)
		}
		if got, err := nw.DecryptContext(out, scrobble.CredentialContext(col, user)); err != nil || got != "sk-"+col {
			t.Fatalf("%s: new key should open it for the same slot: %q, %v", col, got, err)
		}
		if _, ok := reSealScrobbleCredential(old, nw, col, other, stored); ok {
			t.Errorf("%s: a credential sealed for another user must be skipped", col)
		}
		if _, ok := reSealScrobbleCredential(nw, old, col, user, stored); ok {
			t.Errorf("%s: a wrong old key must be skipped", col)
		}
	}
	// The access token opened as a refresh token is another slot.
	stored, _ := old.EncryptContext("tok", scrobble.CredentialContext(scrobble.ColumnTraktAccessToken, user))
	if _, ok := reSealScrobbleCredential(old, nw, scrobble.ColumnTraktRefreshToken, user, stored); ok {
		t.Error("a credential moved to another column must be skipped")
	}
}
