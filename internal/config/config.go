package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
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
	MediaPath   string `env:"MEDIA_PATH"` // deprecated — artwork now stored next to media files
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
	LogLevel     string `env:"LOG_LEVEL"     envDefault:"info"`
	RetainMonths int    `env:"RETAIN_MONTHS" envDefault:"24"`

	// ── Scanning (hot-reloadable via SIGHUP) ─────────────────────────────────
	// ScanFileConcurrency defaults to runtime.NumCPU()*2 (I/O-bound).
	ScanFileConcurrency    int           `env:"SCAN_FILE_CONCURRENCY"`
	ScanLibraryConcurrency int           `env:"SCAN_LIBRARY_CONCURRENCY" envDefault:"2"`
	MissingFileGracePeriod time.Duration `env:"MISSING_FILE_GRACE_PERIOD" envDefault:"15m"`

	// ── Transcoding (hot-reloadable via SIGHUP) ───────────────────────────────
	// TranscodeMaxSessions defaults to max(1, runtime.NumCPU()/2) for software;
	// 4 for hardware — derived at worker startup (ADR-025).
	TranscodeMaxSessions int    `env:"TRANSCODE_MAX_SESSIONS"`
	// TranscodeEncoders overrides auto-detect; e.g. "nvenc,software" or "software".
	TranscodeEncoders    string `env:"TRANSCODE_ENCODERS"`
	// DisableEmbeddedWorker skips the in-process transcode worker. Set to true
	// when using standalone cmd/worker instances on dedicated GPU machines.
	DisableEmbeddedWorker bool   `env:"DISABLE_EMBEDDED_WORKER" envDefault:"false"`
	TranscodeMaxBitrate   int     `env:"TRANSCODE_MAX_BITRATE_KBPS" envDefault:"40000"`
	TranscodeMaxWidth    int     `env:"TRANSCODE_MAX_WIDTH"        envDefault:"3840"`
	TranscodeMaxHeight   int     `env:"TRANSCODE_MAX_HEIGHT"       envDefault:"2160"`
	// Per-encoder tuning (hot-reloadable via SIGHUP). These let operators tune
	// for specific GPU models and upload bandwidth without rebuilding.
	TranscodeNVENCPreset  string  `env:"TRANSCODE_NVENC_PRESET"     envDefault:"p4"`
	TranscodeNVENCTune    string  `env:"TRANSCODE_NVENC_TUNE"       envDefault:"hq"`
	TranscodeNVENCRC      string  `env:"TRANSCODE_NVENC_RC"         envDefault:"vbr"`
	TranscodeMaxrateRatio float64 `env:"TRANSCODE_MAXRATE_RATIO"    envDefault:"1.5"`

	// ── Metadata ─────────────────────────────────────────────────────────────
	TMDBAPIKey    string `env:"TMDB_API_KEY"`
	TMDBRateLimit int    `env:"TMDB_RATE_LIMIT" envDefault:"20"` // req/s
	TVDBAPIKey    string `env:"TVDB_API_KEY"`                     // TheTVDB v4 project key; enables episode fallback

	// ── Worker ───────────────────────────────────────────────────────────────
	WorkerHealthAddr string `env:"WORKER_HEALTH_ADDR" envDefault:":7074"`

	// ── Observability ────────────────────────────────────────────────────────
	// OTELEndpoint: tracing is disabled if unset.
	OTELEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`

	// ── OAuth / SSO (optional) ──────────────────────────────────────────
	// BaseURL: public URL of the server (e.g. https://media.example.com).
	// Required for OAuth redirect URIs. Falls back to http://localhost:$LISTEN_ADDR.
	BaseURL            string `env:"BASE_URL"`
	GoogleClientID     string `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET"`
	GitHubClientID     string `env:"GITHUB_CLIENT_ID"`
	GitHubClientSecret string `env:"GITHUB_CLIENT_SECRET"`
	DiscordClientID    string `env:"DISCORD_CLIENT_ID"`
	DiscordClientSecret string `env:"DISCORD_CLIENT_SECRET"`

	// ── Email / SMTP (optional) ─────────────────────────────────────────
	SMTPHost     string `env:"SMTP_HOST"`
	SMTPPort     int    `env:"SMTP_PORT"     envDefault:"587"`
	SMTPUsername string `env:"SMTP_USERNAME"`
	SMTPPassword string `env:"SMTP_PASSWORD"`
	SMTPFrom     string `env:"SMTP_FROM"`

	// ── Cross-origin clients ────────────────────────────────────────────
	// CORSAllowedOrigins enables cross-origin XHR from listed origins.
	// Use "*" to allow any origin — safe here because the API authenticates
	// via Authorization: Bearer headers, not cookies. Empty disables CORS
	// entirely (same-origin only), which is the default for web-only deploys.
	CORSAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS" envSeparator:","`

	// ── Development ──────────────────────────────────────────────────────────
	// DevFrontendURL: when set (build tag dev), Go server proxies non-API requests
	// to this URL (Vite dev server on :5173). Ignored in production builds.
	DevFrontendURL string `env:"DEV_FRONTEND_URL"`
}

// GoogleOAuthEnabled returns true if Google SSO is configured.
func (c *Config) GoogleOAuthEnabled() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

// GitHubOAuthEnabled returns true if GitHub SSO is configured.
func (c *Config) GitHubOAuthEnabled() bool {
	return c.GitHubClientID != "" && c.GitHubClientSecret != ""
}

// DiscordOAuthEnabled returns true if Discord SSO is configured.
func (c *Config) DiscordOAuthEnabled() bool {
	return c.DiscordClientID != "" && c.DiscordClientSecret != ""
}

// SMTPEnabled returns true if SMTP email sending is configured.
func (c *Config) SMTPEnabled() bool {
	return c.SMTPHost != "" && c.SMTPFrom != "" && c.SMTPUsername != "" && c.SMTPPassword != ""
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
	if c.BaseURL == "" {
		c.BaseURL = "http://localhost" + c.ListenAddr
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
	// Validate OAuth pairs — if one half is set, the other must be too.
	if (c.GoogleClientID == "") != (c.GoogleClientSecret == "") {
		return fmt.Errorf("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must both be set (or both empty)")
	}
	if (c.GitHubClientID == "") != (c.GitHubClientSecret == "") {
		return fmt.Errorf("GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET must both be set (or both empty)")
	}
	if (c.DiscordClientID == "") != (c.DiscordClientSecret == "") {
		return fmt.Errorf("DISCORD_CLIENT_ID and DISCORD_CLIENT_SECRET must both be set (or both empty)")
	}
	// If any OAuth provider is enabled, BaseURL must be explicitly configured
	// (the auto-generated localhost fallback is not suitable for redirect URIs).
	if c.GoogleOAuthEnabled() || c.GitHubOAuthEnabled() || c.DiscordOAuthEnabled() {
		if c.BaseURL == "" || c.BaseURL == "http://localhost"+c.ListenAddr {
			return fmt.Errorf("BASE_URL must be set when an OAuth provider is enabled")
		}
	}
	return nil
}

// LogLevelVar returns an slog.LevelVar initialised to the configured log level.
// The caller owns the returned var and can update it on SIGHUP.
func (c *Config) LogLevelVar() (*slog.LevelVar, error) {
	var lv slog.LevelVar
	if err := lv.UnmarshalText([]byte(c.LogLevel)); err != nil {
		return nil, fmt.Errorf("invalid LOG_LEVEL %q: %w", c.LogLevel, err)
	}
	return &lv, nil
}

// HotReloadable holds the subset of Config values that can be reloaded via SIGHUP.
// These fields are safe to read/write concurrently via the Atomic accessors.
type HotReloadable struct {
	mu sync.RWMutex

	logLevel               slog.Level
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
func (h *HotReloadable) Reload(logger *slog.Logger, current *Config, lv *slog.LevelVar) {
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

	// Update log level atomically via slog.LevelVar.
	if next.LogLevel != current.LogLevel {
		var lv2 slog.LevelVar
		if err := lv2.UnmarshalText([]byte(next.LogLevel)); err != nil {
			logger.Warn("invalid LOG_LEVEL after reload, keeping current", "value", next.LogLevel, "err", err)
		} else {
			lv.Set(lv2.Level())
			logger.Info("log level changed", "level", next.LogLevel)
		}
	}

	logger.Info("config reloaded")
}


// validateSecretKey checks that the SECRET_KEY yields at least 32 bytes.
// Tries hex (64-char string), then base64 (>=43 chars), then raw byte length.
// DeriveKey32 in auth/crypto.go uses the same decode order and truncates to 32.
func validateSecretKey(key string) error {
	if len(key) == 0 {
		return fmt.Errorf("SECRET_KEY is required")
	}
	// Try hex decode (64 hex chars = 32 bytes).
	if len(key) == 64 {
		b, err := hex.DecodeString(key)
		if err == nil && len(b) == 32 {
			return nil
		}
	}
	// Try base64 decode (44 chars with padding, or 43 without = 32 bytes).
	if len(key) >= 43 {
		b, err := base64.StdEncoding.DecodeString(key)
		if err == nil && len(b) >= 32 {
			return nil
		}
		b, err = base64.RawStdEncoding.DecodeString(key)
		if err == nil && len(b) >= 32 {
			return nil
		}
	}
	// Raw bytes — at least 32 (DeriveKey32 truncates to first 32).
	if len(key) >= 32 {
		return nil
	}
	return fmt.Errorf("SECRET_KEY must be at least 32 bytes (or hex-encoded 64 chars, or base64-encoded ~44 chars); got %d raw bytes", len(key))
}

func warnIfChanged(logger *slog.Logger, key, old, new string) {
	if old != new {
		logger.Warn("non-reloadable config value changed — restart required", "key", key)
	}
}

