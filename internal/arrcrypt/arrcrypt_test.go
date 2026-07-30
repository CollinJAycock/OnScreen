package arrcrypt

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
)

func testEncryptor(t *testing.T, seed byte) *auth.Encryptor {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed + byte(i)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	return enc
}

func TestSealOpen_RoundTrips(t *testing.T) {
	enc := testEncryptor(t, 1)
	id := uuid.New()

	sealed, err := Seal(enc, id, "sonarr-api-key")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(sealed, "sonarr-api-key") {
		t.Fatal("sealed value still contains the plaintext key")
	}
	if !IsSealed(sealed) {
		t.Error("sealed value lacks the sentinel, so reads would treat it as cleartext")
	}

	got, err := Open(enc, id, sealed)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got != "sonarr-api-key" {
		t.Errorf("round-trip: got %q, want %q", got, "sonarr-api-key")
	}
}

// The row id is the associated data, so a ciphertext lifted out of one service
// row must not decrypt in another — otherwise anyone able to write the table
// could point service B at service A's credential.
func TestOpen_RejectsCiphertextFromAnotherRow(t *testing.T) {
	enc := testEncryptor(t, 1)
	sealed, err := Seal(enc, uuid.New(), "radarr-api-key")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := Open(enc, uuid.New(), sealed); err == nil {
		t.Error("a ciphertext from a different row decrypted cleanly")
	}
}

func TestOpen_RejectsWrongKey(t *testing.T) {
	id := uuid.New()
	sealed, err := Seal(testEncryptor(t, 1), id, "radarr-api-key")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := Open(testEncryptor(t, 9), id, sealed); err == nil {
		t.Error("a ciphertext decrypted under the wrong key")
	}
}

// Rows written before at-rest encryption carry no sentinel and must keep
// working untouched — that is what lets this ship without a data migration.
func TestOpen_PassesThroughLegacyCleartext(t *testing.T) {
	enc := testEncryptor(t, 1)
	got, err := Open(enc, uuid.New(), "legacy-plaintext-key")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got != "legacy-plaintext-key" {
		t.Errorf("got %q, want the stored cleartext back unchanged", got)
	}
}

// A sealed value with no encryptor must fail loudly. Passing it through would
// send base64 to Sonarr as a credential and surface as a baffling 401.
func TestOpen_SealedWithoutEncryptorIsAnError(t *testing.T) {
	sealed, err := Seal(testEncryptor(t, 1), uuid.New(), "k")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if got, err := Open(nil, uuid.New(), sealed); err == nil {
		t.Errorf("got %q with no error; ciphertext must never be used as a credential", got)
	}
}

// No encryptor configured means store as before — the feature degrades to the
// previous behaviour rather than failing writes.
func TestSeal_NilEncryptorStoresCleartext(t *testing.T) {
	got, err := Seal(nil, uuid.New(), "plain")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if got != "plain" {
		t.Errorf("got %q, want %q", got, "plain")
	}
}

// "" means "no credential"; sealing it would turn an empty key into a non-empty
// stored value, and the DTO's api_key_set flag reads exactly that emptiness.
func TestSeal_EmptyKeyStaysEmpty(t *testing.T) {
	got, err := Seal(testEncryptor(t, 1), uuid.New(), "")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty — api_key_set would report a credential that isn't there", got)
	}
}

// Two rows holding the same key must not produce the same ciphertext, or the
// stored table would reveal which services share a credential.
func TestSeal_DistinctCiphertextPerRow(t *testing.T) {
	enc := testEncryptor(t, 1)
	a, err := Seal(enc, uuid.New(), "same-key")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	b, err := Seal(enc, uuid.New(), "same-key")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if a == b {
		t.Error("identical ciphertexts for the same key in different rows")
	}
}
