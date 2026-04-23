package config

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

// ── validateSecretKey ───────────────────────────────────────────────────────

func TestValidateSecretKey_Empty(t *testing.T) {
	if err := validateSecretKey(""); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestValidateSecretKey_TooShort(t *testing.T) {
	if err := validateSecretKey("short"); err == nil {
		t.Fatal("expected error for short key")
	}
}

// realKey returns 32 bytes of realistic random-looking material —
// comfortably above the entropy floor. Deterministic so tests stay
// reproducible; NOT used as an actual secret.
func realKey() []byte {
	// Pseudo-random mixture of byte values; not cryptographically random
	// but entropy ≥ 3 bits/byte as measured by checkKeyEntropy.
	return []byte("3f7a9c2e8b1d4f60a5c8e1b4d7f0a3c6")
}

func TestValidateSecretKey_RawBytes32(t *testing.T) {
	if err := validateSecretKey(string(realKey())); err != nil {
		t.Errorf("32-byte raw key should be valid: %v", err)
	}
}

func TestValidateSecretKey_RawBytesLong(t *testing.T) {
	// 64 raw bytes of varied characters; not valid hex (contains 'z').
	key := "3f7a9c2e8b1d4f60a5c8e1b4d7f0a3c63f7a9c2e8b1d4f60a5c8e1b4d7f0a3c6"
	if err := validateSecretKey(key); err != nil {
		t.Errorf("64-byte raw key should be valid: %v", err)
	}
}

func TestValidateSecretKey_Hex64(t *testing.T) {
	key := hex.EncodeToString(realKey()) // 64-char hex = 32 bytes
	if err := validateSecretKey(key); err != nil {
		t.Errorf("hex-encoded 32-byte key should be valid: %v", err)
	}
}

func TestValidateSecretKey_Base64(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(realKey()) // 44 chars
	if err := validateSecretKey(key); err != nil {
		t.Errorf("base64-encoded 32-byte key should be valid: %v", err)
	}
}

func TestValidateSecretKey_Base64NoPadding(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString(realKey()) // 43 chars
	if err := validateSecretKey(key); err != nil {
		t.Errorf("base64-encoded (no padding) 32-byte key should be valid: %v", err)
	}
}

func TestValidateSecretKey_RejectsAllSameByte(t *testing.T) {
	if err := validateSecretKey(strings.Repeat("x", 32)); err == nil {
		t.Error("all-same-byte key should be rejected")
	}
}

func TestValidateSecretKey_RejectsLowEntropy(t *testing.T) {
	// "abababab..." has entropy ~1 bit/byte — well under the 3.0 floor.
	if err := validateSecretKey(strings.Repeat("ab", 16)); err == nil {
		t.Error("low-entropy key should be rejected")
	}
}

func TestValidateSecretKey_31Bytes(t *testing.T) {
	key := strings.Repeat("a", 31)
	if err := validateSecretKey(key); err == nil {
		t.Fatal("expected error for 31-byte key")
	}
}

// ── applyDefaults ───────────────────────────────────────────────────────────

func validConfig() *Config {
	return &Config{
		DatabaseURL: "postgres://localhost/test",
		ValkeyURL:   "redis://localhost:6379",
		// Mix of characters to satisfy the entropy/all-same-byte guards
		// in validateSecretKey. Real keys should be generated via
		// `openssl rand -hex 32` — this is just a deterministic test
		// value that clears the entropy floor.
		SecretKey:   "abcdefghijklmnopqrstuvwxyz012345",
		ListenAddr:  ":7070",
	}
}

func TestApplyDefaults_DatabaseROFallback(t *testing.T) {
	cfg := validConfig()
	cfg.DatabaseROURL = ""
	if err := cfg.applyDefaults(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DatabaseROURL != cfg.DatabaseURL {
		t.Errorf("DatabaseROURL should fall back to DatabaseURL")
	}
}

func TestApplyDefaults_CachePathDefault(t *testing.T) {
	cfg := validConfig()
	cfg.MediaPath = "/media"
	cfg.CachePath = ""
	if err := cfg.applyDefaults(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CachePath == "" {
		t.Error("CachePath should be set")
	}
}

func TestApplyDefaults_TranscodeLimits_Clamped(t *testing.T) {
	cfg := validConfig()
	cfg.TranscodeMaxBitrate = -1
	cfg.TranscodeMaxWidth = 99999
	cfg.TranscodeMaxHeight = 0
	if err := cfg.applyDefaults(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TranscodeMaxBitrate != 40000 {
		t.Errorf("TranscodeMaxBitrate: got %d, want 40000", cfg.TranscodeMaxBitrate)
	}
	if cfg.TranscodeMaxWidth != 3840 {
		t.Errorf("TranscodeMaxWidth: got %d, want 3840", cfg.TranscodeMaxWidth)
	}
	if cfg.TranscodeMaxHeight != 2160 {
		t.Errorf("TranscodeMaxHeight: got %d, want 2160", cfg.TranscodeMaxHeight)
	}
}

func TestApplyDefaults_TranscodeLimits_ValidValues(t *testing.T) {
	cfg := validConfig()
	cfg.TranscodeMaxBitrate = 20000
	cfg.TranscodeMaxWidth = 1920
	cfg.TranscodeMaxHeight = 1080
	if err := cfg.applyDefaults(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.TranscodeMaxBitrate != 20000 {
		t.Errorf("TranscodeMaxBitrate should remain 20000, got %d", cfg.TranscodeMaxBitrate)
	}
	if cfg.TranscodeMaxWidth != 1920 {
		t.Errorf("TranscodeMaxWidth should remain 1920, got %d", cfg.TranscodeMaxWidth)
	}
	if cfg.TranscodeMaxHeight != 1080 {
		t.Errorf("TranscodeMaxHeight should remain 1080, got %d", cfg.TranscodeMaxHeight)
	}
}

// ── HotReloadable ───────────────────────────────────────────────────────────

func TestHotReloadable_RoundTrip(t *testing.T) {
	cfg := &Config{
		ScanFileConcurrency:    8,
		ScanLibraryConcurrency: 2,
		TranscodeMaxSessions:   4,
		TranscodeMaxBitrate:    30000,
		TranscodeMaxWidth:      1920,
		TranscodeMaxHeight:     1080,
	}
	hot := NewHotReloadable(cfg)

	if got := hot.ScanFileConcurrency(); got != 8 {
		t.Errorf("ScanFileConcurrency: got %d, want 8", got)
	}
	if got := hot.ScanLibraryConcurrency(); got != 2 {
		t.Errorf("ScanLibraryConcurrency: got %d, want 2", got)
	}
	if got := hot.TranscodeMaxSessions(); got != 4 {
		t.Errorf("TranscodeMaxSessions: got %d, want 4", got)
	}
	if got := hot.TranscodeMaxBitrate(); got != 30000 {
		t.Errorf("TranscodeMaxBitrate: got %d, want 30000", got)
	}
	if got := hot.TranscodeMaxWidth(); got != 1920 {
		t.Errorf("TranscodeMaxWidth: got %d, want 1920", got)
	}
	if got := hot.TranscodeMaxHeight(); got != 1080 {
		t.Errorf("TranscodeMaxHeight: got %d, want 1080", got)
	}
}

func TestDisableEmbeddedWorker_DefaultFalse(t *testing.T) {
	cfg := &Config{
		DatabaseURL: "postgres://test",
		ValkeyURL:   "redis://test",
		SecretKey:   "12345678901234567890123456789012",
		ListenAddr:  ":7070",
	}
	if err := cfg.applyDefaults(); err != nil {
		t.Fatalf("applyDefaults: %v", err)
	}
	if cfg.DisableEmbeddedWorker {
		t.Error("DisableEmbeddedWorker: want false by default")
	}
}

func TestDisableEmbeddedWorker_SetTrue(t *testing.T) {
	cfg := &Config{
		DatabaseURL:           "postgres://test",
		ValkeyURL:             "redis://test",
		SecretKey:             "12345678901234567890123456789012",
		ListenAddr:            ":7070",
		DisableEmbeddedWorker: true,
	}
	if err := cfg.applyDefaults(); err != nil {
		t.Fatalf("applyDefaults: %v", err)
	}
	if !cfg.DisableEmbeddedWorker {
		t.Error("DisableEmbeddedWorker: want true when explicitly set")
	}
}
