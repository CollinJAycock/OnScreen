package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewTokenMaker_ValidKey(t *testing.T) {
	key := make([]byte, 32)
	tm, err := NewTokenMaker(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tm == nil {
		t.Fatal("token maker is nil")
	}
}

func TestNewTokenMaker_InvalidKeySize(t *testing.T) {
	_, err := NewTokenMaker([]byte("short"))
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestIssueAndValidateAccessToken(t *testing.T) {
	key := DeriveKey32("test-secret-key-that-is-32-bytes!")
	tm, err := NewTokenMaker(key)
	if err != nil {
		t.Fatalf("NewTokenMaker: %v", err)
	}

	userID := uuid.New()
	claims := Claims{
		UserID:   userID,
		Username: "testuser",
		IsAdmin:  true,
	}

	tokenStr, err := tm.IssueAccessToken(claims)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("token is empty")
	}

	got, err := tm.ValidateAccessToken(tokenStr)
	if err != nil {
		t.Fatalf("ValidateAccessToken: %v", err)
	}

	if got.UserID != userID {
		t.Errorf("UserID: got %v, want %v", got.UserID, userID)
	}
	if got.Username != "testuser" {
		t.Errorf("Username: got %q, want %q", got.Username, "testuser")
	}
	if !got.IsAdmin {
		t.Error("IsAdmin: got false, want true")
	}
	if got.ExpiresAt.Before(time.Now()) {
		t.Error("token already expired")
	}
}

func TestValidateAccessToken_NonAdmin(t *testing.T) {
	key := DeriveKey32("test-secret-key-that-is-32-bytes!")
	tm, _ := NewTokenMaker(key)

	tokenStr, _ := tm.IssueAccessToken(Claims{
		UserID:   uuid.New(),
		Username: "regular",
		IsAdmin:  false,
	})

	got, err := tm.ValidateAccessToken(tokenStr)
	if err != nil {
		t.Fatalf("ValidateAccessToken: %v", err)
	}
	if got.IsAdmin {
		t.Error("IsAdmin: got true, want false")
	}
}

func TestValidateAccessToken_InvalidToken(t *testing.T) {
	key := DeriveKey32("test-secret-key-that-is-32-bytes!")
	tm, _ := NewTokenMaker(key)

	_, err := tm.ValidateAccessToken("not-a-valid-token")
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestValidateAccessToken_WrongKey(t *testing.T) {
	key1 := DeriveKey32("test-secret-key-that-is-32-bytes!")
	key2 := DeriveKey32("different-secret-key-32-bytesXX!")
	tm1, _ := NewTokenMaker(key1)
	tm2, _ := NewTokenMaker(key2)

	tokenStr, _ := tm1.IssueAccessToken(Claims{
		UserID:   uuid.New(),
		Username: "user",
	})

	_, err := tm2.ValidateAccessToken(tokenStr)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestIssueRefreshToken(t *testing.T) {
	raw, hash, err := IssueRefreshToken()
	if err != nil {
		t.Fatalf("IssueRefreshToken: %v", err)
	}
	if raw == "" {
		t.Fatal("raw token is empty")
	}
	if hash == "" {
		t.Fatal("hash is empty")
	}
	if hash == raw {
		t.Fatal("hash should not equal raw token")
	}
	// Verify hash is deterministic.
	if HashToken(raw) != hash {
		t.Error("HashToken(raw) does not match returned hash")
	}
}

func TestIssueRefreshToken_Uniqueness(t *testing.T) {
	raw1, _, _ := IssueRefreshToken()
	raw2, _, _ := IssueRefreshToken()
	if raw1 == raw2 {
		t.Fatal("two refresh tokens should be unique")
	}
}

func TestHashToken_Deterministic(t *testing.T) {
	token := "my-refresh-token"
	h1 := HashToken(token)
	h2 := HashToken(token)
	if h1 != h2 {
		t.Errorf("HashToken not deterministic: %q != %q", h1, h2)
	}
}

func TestDeriveKey32_LongKey(t *testing.T) {
	key := DeriveKey32("this-is-a-key-that-is-definitely-longer-than-32-bytes")
	if len(key) != 32 {
		t.Errorf("key length: got %d, want 32", len(key))
	}
}

func TestDeriveKey32_ShortKey(t *testing.T) {
	key := DeriveKey32("short")
	if len(key) != 32 {
		t.Errorf("key length: got %d, want 32", len(key))
	}
}

func TestEncryptor_RoundTrip(t *testing.T) {
	key := DeriveKey32("test-secret-key-that-is-32-bytes!")
	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}

	plaintext := "my-secret-value"
	ciphertext, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ciphertext == plaintext {
		t.Fatal("ciphertext should differ from plaintext")
	}

	decrypted, err := enc.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if decrypted != plaintext {
		t.Errorf("Decrypt: got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptor_DifferentCiphertexts(t *testing.T) {
	key := DeriveKey32("test-secret-key-that-is-32-bytes!")
	enc, _ := NewEncryptor(key)

	c1, _ := enc.Encrypt("same")
	c2, _ := enc.Encrypt("same")
	if c1 == c2 {
		t.Fatal("same plaintext should produce different ciphertexts (random nonce)")
	}
}

func TestEncryptor_InvalidCiphertext(t *testing.T) {
	key := DeriveKey32("test-secret-key-that-is-32-bytes!")
	enc, _ := NewEncryptor(key)

	_, err := enc.Decrypt("not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestEncryptor_TruncatedCiphertext(t *testing.T) {
	key := DeriveKey32("test-secret-key-that-is-32-bytes!")
	enc, _ := NewEncryptor(key)

	_, err := enc.Decrypt("YQ==") // too short
	if err == nil {
		t.Fatal("expected error for truncated ciphertext")
	}
}

func TestNewEncryptor_InvalidKeySize(t *testing.T) {
	_, err := NewEncryptor([]byte("short"))
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

// The sid claim survives every token kind a session mints, and is absent
// (uuid.Nil) when the claims carry none — PIN-switch tokens have no session.
func TestSessionIDClaimRoundTrip(t *testing.T) {
	tm, err := NewTokenMaker(DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatalf("NewTokenMaker: %v", err)
	}
	sid := uuid.New()
	claims := Claims{UserID: uuid.New(), Username: "u", SessionID: sid}

	access, _ := tm.IssueAccessToken(claims)
	asset, _ := tm.IssueAssetToken(claims)
	stream, _ := tm.IssueStreamToken(claims, uuid.New())
	for name, tok := range map[string]string{"access": access, "asset": asset, "stream": stream} {
		got, err := tm.ValidateAccessToken(tok)
		if err != nil {
			t.Fatalf("%s: validate: %v", name, err)
		}
		if got.SessionID != sid {
			t.Errorf("%s: SessionID = %v, want %v", name, got.SessionID, sid)
		}
	}

	claims.SessionID = uuid.Nil
	bare, _ := tm.IssueAccessToken(claims)
	got, err := tm.ValidateAccessToken(bare)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got.SessionID != uuid.Nil {
		t.Errorf("sessionless token: SessionID = %v, want nil", got.SessionID)
	}
}
