package auth

import "testing"

func newTestEncryptor(t *testing.T) *Encryptor {
	t.Helper()
	enc, err := NewEncryptor(DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return enc
}

// ── Context-bound (AES-GCM associated data) ──────────────────────────────────
//
// Without associated data a ciphertext is interchangeable between storage
// slots — it decrypts wherever it is pasted. In server_settings that made the
// table a decryption oracle: the read path decrypts anything carrying the
// sentinel "regardless of allowlist", so moving a genuinely secret row's
// ciphertext into a row that is echoed back through an admin API read the
// secret out. Binding a per-slot context fixes that.

func TestEncryptContext_RoundTrips(t *testing.T) {
	e := newTestEncryptor(t)
	ct, err := e.EncryptContext("s3kr3t", "tmdb_api_key")
	if err != nil {
		t.Fatalf("EncryptContext: %v", err)
	}
	got, err := e.DecryptContext(ct, "tmdb_api_key")
	if err != nil {
		t.Fatalf("DecryptContext: %v", err)
	}
	if got != "s3kr3t" {
		t.Errorf("got %q, want %q", got, "s3kr3t")
	}
}

func TestDecryptContext_RejectsRelocatedCiphertext(t *testing.T) {
	// THE point of the change: a ciphertext written for one settings key must
	// not decrypt under another.
	e := newTestEncryptor(t)
	ct, err := e.EncryptContext("s3kr3t", "tmdb_api_key")
	if err != nil {
		t.Fatalf("EncryptContext: %v", err)
	}
	if _, err := e.DecryptContext(ct, "general_config"); err == nil {
		t.Fatal("ciphertext decrypted under a different context — it is still relocatable")
	}
}

func TestDecryptContext_RejectsEmptyContextForBoundCiphertext(t *testing.T) {
	e := newTestEncryptor(t)
	ct, _ := e.EncryptContext("s3kr3t", "tmdb_api_key")
	if _, err := e.DecryptContext(ct, ""); err == nil {
		t.Fatal("bound ciphertext decrypted with an empty context")
	}
	// And the legacy no-AAD path must not open it either.
	if _, err := e.Decrypt(ct); err == nil {
		t.Fatal("bound ciphertext opened by the legacy no-AAD Decrypt")
	}
}

func TestDecrypt_LegacyValuesStillReadable(t *testing.T) {
	// Backward compatibility is the whole reason for a versioned envelope:
	// existing encv1: rows must keep working, or a deploy bricks every stored
	// credential.
	e := newTestEncryptor(t)
	legacy, err := e.Encrypt("old-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := e.Decrypt(legacy)
	if err != nil {
		t.Fatalf("Decrypt legacy: %v", err)
	}
	if got != "old-secret" {
		t.Errorf("got %q, want old-secret", got)
	}
	// A legacy ciphertext must NOT open under the context path.
	if _, err := e.DecryptContext(legacy, "tmdb_api_key"); err == nil {
		t.Fatal("legacy no-AAD ciphertext opened by DecryptContext")
	}
}

func TestEncryptContext_NonceIsFresh(t *testing.T) {
	e := newTestEncryptor(t)
	a, _ := e.EncryptContext("same", "k")
	b, _ := e.EncryptContext("same", "k")
	if a == b {
		t.Fatal("two encryptions produced identical ciphertext — nonce reuse")
	}
}
