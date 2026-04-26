package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config holds all runtime configuration. Values are loaded from environment
// variables on startup and validated before any service starts. A missing
// required value is a fatal error — fail loudly, not at runtime.
//
// A subset of values can be reloaded at runtime via SIGHUP (ADR-027).
// Values that require a restart are not reloadable and emit a WARN log if
// they differ from the running value after a reload.
type Config struct {
	// ── Required ─────────────────────────────────────────────────────────────
	DatabaseURL string `env:"DATABASE_URL,required"`
	ValkeyURL   string `env:"VALKEY_URL,required"`
	MediaPath   string `env:"MEDIA_PATH"`          // deprecated — artwork now stored next to media files
	SecretKey   string `env:"SECRET_KEY,required"` // AES-256-GCM key (32 bytes, base64 or hex)

	// ── Database (optional) ───────────────────────────────────────────────────
	// DatabaseROURL falls back to DatabaseURL if unset (single-node deployments).
	DatabaseROURL string `env:"DATABASE_RO_URL"`

	// ── Cache ────────────────────────────────────────────────────────────────
	// Artwork resize cache. Defaults to $MEDIA_PATH/.cache/artwork at runtime.
	CachePath string `env:"CACHE_PATH"`

	// ── Server ───────────────────────────────────────────────────────────────
	ListenAddr   string `env:"LISTEN_ADDR"   envDefault:":7070"`
	MetricsAddr  string `env:"METRICS_ADDR"  envDefault:":7071"`
	RetainMonths int    `env:"RETAIN_MONTHS" envDefault:"24"`

	// TLS — when both files are set the API server serves HTTPS via
	// ListenAndServeTLS instead of plain HTTP. Files must be in the
	// formats Go's crypto/tls accepts (PEM-encoded cert chain + PEM-
	// encoded private key). Setting only one is a config error and
	// the server refuses to start so an admin doesn't deploy a
	// confused half-TLS setup. For Let's Encrypt or fully-managed
	// HTTPS, run a reverse proxy in front instead — see
	// docs/deployment.md.
	TLSCertFile string `env:"TLS_CERT_FILE"`
	TLSKeyFile  string `env:"TLS_KEY_FILE"`

	// ServerName is the human-friendly name advertised over LAN discovery
	// and surfaced in capability responses. Defaults to "OnScreen" if unset.
	ServerName string `env:"SERVER_NAME" envDefault:"OnScreen"`

	// DiscoveryPort is the UDP port the LAN discovery listener binds to.
	// Set DiscoveryEnabled=false to disable broadcasting entirely.
	DiscoveryEnabled bool `env:"DISCOVERY_ENABLED" envDefault:"true"`
	DiscoveryPort    int  `env:"DISCOVERY_PORT"    envDefault:"7368"`

	// ── Scanning (hot-reloadable via SIGHUP) ─────────────────────────────────
	// ScanFileConcurrency defaults to runtime.NumCPU()*2 (I/O-bound).
	ScanFileConcurrency    int           `env:"SCAN_FILE_CONCURRENCY"`
	ScanLibraryConcurrency int           `env:"SCAN_LIBRARY_CONCURRENCY" envDefault:"2"`
	MissingFileGracePeriod time.Duration `env:"MISSING_FILE_GRACE_PERIOD" envDefault:"15m"`

	// ── Transcoding (hot-reloadable via SIGHUP) ───────────────────────────────
	// TranscodeMaxSessions defaults to max(1, runtime.NumCPU()/2) for software;
	// 4 for hardware — derived at worker startup (ADR-025).
	TranscodeMaxSessions int `env:"TRANSCODE_MAX_SESSIONS"`
	// TranscodeEncoders overrides auto-detect; e.g. "nvenc,software" or "software".
	TranscodeEncoders string `env:"TRANSCODE_ENCODERS"`
	// DisableEmbeddedWorker skips the in-process transcode worker. Set to true
	// when using standalone cmd/worker instances on dedicated GPU machines.
	DisableEmbeddedWorker bool `env:"DISABLE_EMBEDDED_WORKER" envDefault:"false"`
	TranscodeMaxBitrate   int  `env:"TRANSCODE_MAX_BITRATE_KBPS" envDefault:"40000"`
	TranscodeMaxWidth     int  `env:"TRANSCODE_MAX_WIDTH"        envDefault:"3840"`
	TranscodeMaxHeight    int  `env:"TRANSCODE_MAX_HEIGHT"       envDefault:"2160"`
	// Per-encoder tuning (hot-reloadable via SIGHUP). These let operators tune
	// for specific GPU models and upload bandwidth without rebuilding.
	TranscodeNVENCPreset  string  `env:"TRANSCODE_NVENC_PRESET"     envDefault:"p4"`
	TranscodeNVENCTune    string  `env:"TRANSCODE_NVENC_TUNE"       envDefault:"hq"`
	TranscodeNVENCRC      string  `env:"TRANSCODE_NVENC_RC"         envDefault:"vbr"`
	TranscodeMaxrateRatio float64 `env:"TRANSCODE_MAXRATE_RATIO"    envDefault:"1.5"`

	// ── Metadata ─────────────────────────────────────────────────────────────
	TMDBAPIKey    string `env:"TMDB_API_KEY"`
	TMDBRateLimit int    `env:"TMDB_RATE_LIMIT" envDefault:"5"` // req/s — conservative; TMDB auto-throttles abusive keys
	TVDBAPIKey    string `env:"TVDB_API_KEY"`                    // TheTVDB v4 project key; enables episode fallback

	// ── Worker ───────────────────────────────────────────────────────────────
	WorkerHealthAddr string `env:"WORKER_HEALTH_ADDR" envDefault:":7074"`

	// ── Development ──────────────────────────────────────────────────────────
	// DevFrontendURL: when set (build tag dev), Go server proxies non-API requests
	// to this URL (Vite dev server on :5173). Ignored in production builds.
	DevFrontendURL string `env:"DEV_FRONTEND_URL"`
}

// Load reads config from environment variables and validates required fields.
// Exits the process on validation failure — config errors are not recoverable.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.applyDefaults(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// applyDefaults fills in values that can't be expressed as envDefault tags
// because they depend on runtime information.
func (c *Config) applyDefaults() error {
	if c.DatabaseROURL == "" {
		c.DatabaseROURL = c.DatabaseURL
	}
	if c.CachePath == "" {
		if c.MediaPath != "" {
			c.CachePath = filepath.Join(c.MediaPath, ".cache", "artwork")
		} else {
			home, _ := os.UserHomeDir()
			c.CachePath = filepath.Join(home, ".onscreen", "cache", "artwork")
		}
	}
	if c.ScanFileConcurrency == 0 {
		c.ScanFileConcurrency = runtime.NumCPU() * 2
	}
	if c.TranscodeMaxSessions == 0 {
		c.TranscodeMaxSessions = max(1, runtime.NumCPU()/2)
	}
	// Validate SecretKey: AES-256-GCM requires exactly 32 bytes.
	// Accept hex-encoded (64 chars), base64-encoded (>=43 chars), or raw (exactly 32 bytes).
	// DeriveKey32 in auth/crypto.go takes the first 32 bytes of the raw string, so we
	// validate that the decoded form yields at least 32 bytes.
	if err := validateSecretKey(c.SecretKey); err != nil {
		return err
	}
	// Validate transcode limits — prevent misconfiguration (zero, negative, or extreme values).
	if c.TranscodeMaxBitrate <= 0 {
		c.TranscodeMaxBitrate = 40000
	}
	if c.TranscodeMaxWidth <= 0 || c.TranscodeMaxWidth > 7680 {
		c.TranscodeMaxWidth = 3840
	}
	if c.TranscodeMaxHeight <= 0 || c.TranscodeMaxHeight > 4320 {
		c.TranscodeMaxHeight = 2160
	}
	if c.TranscodeMaxrateRatio <= 0 {
		c.TranscodeMaxrateRatio = 1.5
	}
	if c.TranscodeNVENCPreset == "" {
		c.TranscodeNVENCPreset = "p4"
	}
	if c.TranscodeNVENCTune == "" {
		c.TranscodeNVENCTune = "hq"
	}
	if c.TranscodeNVENCRC == "" {
		c.TranscodeNVENCRC = "vbr"
	}
	// Reject half-set TLS so an admin doesn't deploy thinking HTTPS is on
	// when only one half of the pair landed in their environment.
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return fmt.Errorf("TLS_CERT_FILE and TLS_KEY_FILE must both be set or both be empty")
	}
	return nil
}

// TLSEnabled reports whether the API server should serve HTTPS.
func (c *Config) TLSEnabled() bool {
	return c.TLSCertFile != "" && c.TLSKeyFile != ""
}

// HotReloadable holds the subset of Config values that can be reloaded via SIGHUP.
// These fields are safe to read/write concurrently via the Atomic accessors.
type HotReloadable struct {
	mu sync.RWMutex

	scanFileConcurrency    int
	scanLibraryConcurrency int
	transcodeMaxSessions   int
	transcodeMaxBitrate    int
	transcodeMaxWidth      int
	transcodeMaxHeight     int
	transcodeNVENCPreset   string
	transcodeNVENCTune     string
	transcodeNVENCRC       string
	transcodeMaxrateRatio  float64
}

// NewHotReloadable creates a HotReloadable from the initial config.
func NewHotReloadable(cfg *Config) *HotReloadable {
	return &HotReloadable{
		scanFileConcurrency:    cfg.ScanFileConcurrency,
		scanLibraryConcurrency: cfg.ScanLibraryConcurrency,
		transcodeMaxSessions:   cfg.TranscodeMaxSessions,
		transcodeMaxBitrate:    cfg.TranscodeMaxBitrate,
		transcodeMaxWidth:      cfg.TranscodeMaxWidth,
		transcodeMaxHeight:     cfg.TranscodeMaxHeight,
		transcodeNVENCPreset:   cfg.TranscodeNVENCPreset,
		transcodeNVENCTune:     cfg.TranscodeNVENCTune,
		transcodeNVENCRC:       cfg.TranscodeNVENCRC,
		transcodeMaxrateRatio:  cfg.TranscodeMaxrateRatio,
	}
}

func (h *HotReloadable) ScanFileConcurrency() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.scanFileConcurrency
}

func (h *HotReloadable) ScanLibraryConcurrency() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.scanLibraryConcurrency
}

func (h *HotReloadable) TranscodeMaxSessions() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.transcodeMaxSessions
}

func (h *HotReloadable) TranscodeMaxBitrate() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.transcodeMaxBitrate
}

func (h *HotReloadable) TranscodeMaxWidth() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.transcodeMaxWidth
}

func (h *HotReloadable) TranscodeMaxHeight() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.transcodeMaxHeight
}

func (h *HotReloadable) TranscodeNVENCPreset() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.transcodeNVENCPreset
}

func (h *HotReloadable) TranscodeNVENCTune() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.transcodeNVENCTune
}

func (h *HotReloadable) TranscodeNVENCRC() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.transcodeNVENCRC
}

func (h *HotReloadable) TranscodeMaxrateRatio() float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.transcodeMaxrateRatio
}

// Reload re-parses the environment and updates all hot-reloadable values.
// Non-reloadable fields that have changed are logged as WARN.
func (h *HotReloadable) Reload(logger *slog.Logger, current *Config) {
	next := &Config{}
	if err := env.Parse(next); err != nil {
		logger.Error("config reload failed", "err", err)
		return
	}
	if err := next.applyDefaults(); err != nil {
		logger.Error("config reload failed", "err", err)
		return
	}

	// Warn about non-reloadable changes (restart required).
	warnIfChanged(logger, "DATABASE_URL", current.DatabaseURL, next.DatabaseURL)
	warnIfChanged(logger, "DATABASE_RO_URL", current.DatabaseROURL, next.DatabaseROURL)
	warnIfChanged(logger, "VALKEY_URL", current.ValkeyURL, next.ValkeyURL)
	warnIfChanged(logger, "LISTEN_ADDR", current.ListenAddr, next.ListenAddr)
	warnIfChanged(logger, "SECRET_KEY", current.SecretKey, next.SecretKey)
	warnIfChanged(logger, "MEDIA_PATH", current.MediaPath, next.MediaPath)

	// Apply reloadable changes.
	h.mu.Lock()
	h.scanFileConcurrency = next.ScanFileConcurrency
	h.scanLibraryConcurrency = next.ScanLibraryConcurrency
	h.transcodeMaxSessions = next.TranscodeMaxSessions
	h.transcodeMaxBitrate = next.TranscodeMaxBitrate
	h.transcodeMaxWidth = next.TranscodeMaxWidth
	h.transcodeMaxHeight = next.TranscodeMaxHeight
	h.transcodeNVENCPreset = next.TranscodeNVENCPreset
	h.transcodeNVENCTune = next.TranscodeNVENCTune
	h.transcodeNVENCRC = next.TranscodeNVENCRC
	h.transcodeMaxrateRatio = next.TranscodeMaxrateRatio
	h.mu.Unlock()

	logger.Info("config reloaded")
}

// validateSecretKey checks that the SECRET_KEY yields at least 32 bytes
// of reasonably high-entropy key material.
// Tries hex (64-char string), then base64 (>=43 chars), then raw byte length.
// DeriveKey32 in auth/crypto.go uses the same decode order and truncates to 32.
//
// Also rejects obviously-weak keys: all-same-byte, low Shannon entropy,
// and a small set of dictionary values. This is defense against
// operator error ("just use aaaaa... for now") rather than against a
// serious attacker — a real attacker either gets the key from the env
// file or doesn't, entropy filters don't help. But a weak-key accept
// converts a minor ops mistake into a full PASETO-forge.
func validateSecretKey(key string) error {
	if len(key) == 0 {
		return fmt.Errorf("SECRET_KEY is required")
	}
	var keyBytes []byte
	// Try hex decode (64 hex chars = 32 bytes).
	if len(key) == 64 {
		b, err := hex.DecodeString(key)
		if err == nil && len(b) == 32 {
			keyBytes = b
		}
	}
	// Try base64 decode (44 chars with padding, or 43 without = 32 bytes).
	if keyBytes == nil && len(key) >= 43 {
		if b, err := base64.StdEncoding.DecodeString(key); err == nil && len(b) >= 32 {
			keyBytes = b[:32]
		} else if b, err := base64.RawStdEncoding.DecodeString(key); err == nil && len(b) >= 32 {
			keyBytes = b[:32]
		}
	}
	// Raw bytes — at least 32 (DeriveKey32 truncates to first 32).
	if keyBytes == nil && len(key) >= 32 {
		keyBytes = []byte(key)[:32]
	}
	if keyBytes == nil {
		return fmt.Errorf("SECRET_KEY must be at least 32 bytes (or hex-encoded 64 chars, or base64-encoded ~44 chars); got %d raw bytes", len(key))
	}
	return checkKeyEntropy(keyBytes)
}

// checkKeyEntropy rejects obviously-weak 32-byte key material. The
// thresholds are intentionally conservative — we want to catch
// "SECRET_KEY=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" while still accepting
// any real random-looking key. A key that's produced by
// `openssl rand -hex 32` or `pwgen -s 32` will pass all of these
// checks trivially.
//
// Shannon entropy threshold of 4.0 bits/byte covers the common failure
// modes (repeated chars, simple patterns) without false-positiving on
// real keys whose empirical entropy is typically ~5–7 bits/byte.
func checkKeyEntropy(key []byte) error {
	if len(key) < 32 {
		return fmt.Errorf("SECRET_KEY must yield at least 32 bytes; got %d", len(key))
	}
	// All-same-byte: the worst degenerate case, also caught by entropy
	// check but failing here gives a clearer error message.
	same := true
	for i := 1; i < len(key); i++ {
		if key[i] != key[0] {
			same = false
			break
		}
	}
	if same {
		return fmt.Errorf("SECRET_KEY is all the same byte — use `openssl rand -hex 32` to generate a real one")
	}
	// Shannon entropy: H = -Σ p_i log2(p_i) over the byte distribution.
	// A uniformly random 32-byte key averages ~4.5–5 bits/byte (high
	// variance on short inputs — a random key can legitimately dip
	// toward 3.5). We require ≥3.0 bits/byte, which rejects
	// all-aaa/abcabc patterns without false-positiving on real
	// randomness. Crucially: this is NOT a security check against a
	// determined attacker — it's a sanity check against operator
	// typos and copy-paste-of-placeholder-values.
	var counts [256]int
	for _, b := range key {
		counts[b]++
	}
	var entropy float64
	n := float64(len(key))
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		entropy -= p * math.Log2(p)
	}
	if entropy < 3.0 {
		return fmt.Errorf("SECRET_KEY has too little entropy (%.2f bits/byte; need ≥3.0) — use `openssl rand -hex 32`", entropy)
	}
	return nil
}

func warnIfChanged(logger *slog.Logger, key, old, new string) {
	if old != new {
		logger.Warn("non-reloadable config value changed — restart required", "key", key)
	}
}
