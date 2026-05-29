package main

import (
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/auth"
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

// An encv1:-prefixed secret — server_settings shape — round-trips and keeps its
// prefix.
func TestReEncrypt_PrefixedRoundTrips(t *testing.T) {
	old := mustEnc(t, strings.Repeat("a", 32))
	nw := mustEnc(t, strings.Repeat("b", 32))

	ct, err := old.Encrypt(`{"client_secret":"x"}`)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	out, ok := reEncrypt(old, nw, encPrefix+ct)
	if !ok {
		t.Fatal("expected ok")
	}
	if !strings.HasPrefix(out, encPrefix) {
		t.Error("prefixed value must keep its prefix")
	}
	got, err := nw.Decrypt(strings.TrimPrefix(out, encPrefix))
	if err != nil || got != `{"client_secret":"x"}` {
		t.Errorf("decrypt: got %q err %v", got, err)
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
