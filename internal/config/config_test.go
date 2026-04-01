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

func TestValidateSecretKey_RawBytes32(t *testing.T) {
	key := strings.Repeat("x", 32)
	if err := validateSecretKey(key); err != nil {
		t.Errorf("32-byte raw key should be valid: %v", err)
	}
}

func TestValidateSecretKey_RawBytesLong(t *testing.T) {
	key := strings.Repeat("x", 64) // 64 raw bytes but not valid hex
	// "xxxx..." is not valid hex, so hex decode fails, but raw len >= 32 passes.
	if err := validateSecretKey(key); err != nil {
		t.Errorf("64-byte raw key should be valid: %v", err)
	}
}

func TestValidateSecretKey_Hex64(t *testing.T) {
	key := hex.EncodeToString(make([]byte, 32)) // 64-char hex = 32 bytes
	if err := validateSecretKey(key); err != nil {
		t.Errorf("hex-encoded 32-byte key should be valid: %v", err)
	}
}

func TestValidateSecretKey_Base64(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32)) // 44 chars
	if err := validateSecretKey(key); err != nil {
		t.Errorf("base64-encoded 32-byte key should be valid: %v", err)
	}
}

func TestValidateSecretKey_Base64NoPadding(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32)) // 43 chars
	if err := validateSecretKey(key); err != nil {
		t.Errorf("base64-encoded (no padding) 32-byte key should be valid: %v", err)
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
		SecretKey:   strings.Repeat("k", 32),
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

// ── OAuth pair validation ───────────────────────────────────────────────────

func TestApplyDefaults_OAuthPair_GoogleIncomplete(t *testing.T) {
	cfg := validConfig()
	cfg.GoogleClientID = "id-only"
	if err := cfg.applyDefaults(); err == nil {
		t.Fatal("expected error for incomplete Google OAuth pair")
	}
}

func TestApplyDefaults_OAuthPair_GitHubIncomplete(t *testing.T) {
	cfg := validConfig()
	cfg.GitHubClientSecret = "secret-only"
	if err := cfg.applyDefaults(); err == nil {
		t.Fatal("expected error for incomplete GitHub OAuth pair")
	}
}

func TestApplyDefaults_OAuthPair_DiscordIncomplete(t *testing.T) {
	cfg := validConfig()
	cfg.DiscordClientID = "id-only"
	if err := cfg.applyDefaults(); err == nil {
		t.Fatal("expected error for incomplete Discord OAuth pair")
	}
}

func TestApplyDefaults_OAuth_RequiresExplicitBaseURL(t *testing.T) {
	cfg := validConfig()
	cfg.GoogleClientID = "id"
	cfg.GoogleClientSecret = "secret"
	// BaseURL is empty, will be auto-filled to localhost — should fail.
	cfg.BaseURL = ""
	if err := cfg.applyDefaults(); err == nil {
		t.Fatal("expected error: OAuth enabled but BaseURL is localhost fallback")
	}
}

func TestApplyDefaults_OAuth_WithBaseURL(t *testing.T) {
	cfg := validConfig()
	cfg.GoogleClientID = "id"
	cfg.GoogleClientSecret = "secret"
	cfg.BaseURL = "https://media.example.com"
	if err := cfg.applyDefaults(); err != nil {
		t.Errorf("OAuth with explicit BaseURL should be valid: %v", err)
	}
}

func TestApplyDefaults_BothOAuthEmpty_OK(t *testing.T) {
	cfg := validConfig()
	if err := cfg.applyDefaults(); err != nil {
		t.Errorf("no OAuth should be valid: %v", err)
	}
}

// ── OAuth enabled helpers ───────────────────────────────────────────────────

func TestGoogleOAuthEnabled(t *testing.T) {
	c := &Config{GoogleClientID: "id", GoogleClientSecret: "secret"}
	if !c.GoogleOAuthEnabled() {
		t.Error("expected GoogleOAuthEnabled=true")
	}
	c.GoogleClientID = ""
	if c.GoogleOAuthEnabled() {
		t.Error("expected GoogleOAuthEnabled=false when ID is empty")
	}
}

func TestGitHubOAuthEnabled(t *testing.T) {
	c := &Config{GitHubClientID: "id", GitHubClientSecret: "secret"}
	if !c.GitHubOAuthEnabled() {
		t.Error("expected GitHubOAuthEnabled=true")
	}
}

func TestDiscordOAuthEnabled(t *testing.T) {
	c := &Config{DiscordClientID: "id", DiscordClientSecret: "secret"}
	if !c.DiscordOAuthEnabled() {
		t.Error("expected DiscordOAuthEnabled=true")
	}
}

// ── SMTPEnabled ─────────────────────────────────────────────────────────────

func TestSMTPEnabled(t *testing.T) {
	c := &Config{SMTPHost: "smtp.example.com", SMTPFrom: "no-reply@example.com", SMTPUsername: "user", SMTPPassword: "pass"}
	if !c.SMTPEnabled() {
		t.Error("expected SMTPEnabled=true")
	}
	c.SMTPHost = ""
	if c.SMTPEnabled() {
		t.Error("expected SMTPEnabled=false when host is empty")
	}
}

// ── LogLevelVar ─────────────────────────────────────────────────────────────

func TestLogLevelVar_Valid(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		c := &Config{LogLevel: level}
		lv, err := c.LogLevelVar()
		if err != nil {
			t.Errorf("LogLevelVar(%q): unexpected error: %v", level, err)
		}
		if lv == nil {
			t.Errorf("LogLevelVar(%q): returned nil", level)
		}
	}
}

func TestLogLevelVar_Invalid(t *testing.T) {
	c := &Config{LogLevel: "not-a-level"}
	_, err := c.LogLevelVar()
	if err == nil {
		t.Fatal("expected error for invalid log level")
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
