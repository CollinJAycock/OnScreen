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
	// DatabaseURL accepts a multi-host DSN for Postgres HA, e.g.
	//   postgres://u:p@primary:5432,replica:5432/db?target_session_attrs=read-write
	// pgx records the extra hosts as fallbacks and connects to whichever is
	// read-write, re-homing to a promoted primary after a failover (ADR-033). The
	// pool shortens its connection lifetime automatically when fallbacks are
	// present so writes re-home within ~1 min of a graceful switchover.
	DatabaseURL string `env:"DATABASE_URL,required"`
	ValkeyURL   string `env:"VALKEY_URL,required"`

	// ── Valkey HA (optional) ─────────────────────────────────────────────────
	// When VALKEY_SENTINEL_ADDRS is set, the client connects via Valkey Sentinel
	// for automatic master failover instead of the single VALKEY_URL host.
	// VALKEY_URL still supplies auth/db/TLS (its host is ignored in Sentinel
	// mode — Sentinel discovers the live master), so credentials have one source
	// regardless of topology. Leave unset for a single-node deployment.
	ValkeySentinelAddrs    []string `env:"VALKEY_SENTINEL_ADDRS"`
	ValkeySentinelMaster   string   `env:"VALKEY_SENTINEL_MASTER"   envDefault:"onscreen"`
	ValkeySentinelPassword string   `env:"VALKEY_SENTINEL_PASSWORD"`
	SecretKey              string   `env:"SECRET_KEY,required"` // AES-256-GCM key (32 bytes, base64 or hex)

	// ── Database (optional) ───────────────────────────────────────────────────
	// DatabaseROURL falls back to DatabaseURL if unset (single-node deployments).
	DatabaseROURL string `env:"DATABASE_RO_URL"`

	// ── Cache ────────────────────────────────────────────────────────────────
	// Artwork resize cache. Defaults to ~/.onscreen/cache/artwork at runtime.
	CachePath string `env:"CACHE_PATH"`

	// ── Server ───────────────────────────────────────────────────────────────
	ListenAddr string `env:"LISTEN_ADDR"   envDefault:":7070"`
	// MetricsAddr also serves /debug/pprof (heap/goroutine dumps that can hold
	// live tokens/secrets) with no auth, so it binds to loopback by default.
	// Expose it (METRICS_ADDR=:7071 or a management interface) only behind a
	// firewall / private network.
	MetricsAddr  string `env:"METRICS_ADDR"  envDefault:"127.0.0.1:7071"`
	RetainMonths int    `env:"RETAIN_MONTHS" envDefault:"24"`

	// TrustedProxies is the allowlist of peers permitted to set X-Forwarded-*
	// (comma-separated CIDRs or bare IPs, e.g. "127.0.0.1,172.18.0.0/16").
	//
	// Unset means "any loopback or RFC1918 / unique-local peer", which on a
	// typical self-hosted install trusts EVERY client on the LAN — letting one
	// forge X-Forwarded-For to rotate its per-IP rate-limit key (defeating
	// login brute-force protection) or to forge the audit-log IP. Set this to
	// your reverse proxy's address to close that.
	//
	// Left permissive by default on purpose: the proxy is frequently a sibling
	// container on a private address, and refusing it would make the server
	// ignore its own proxy's X-Forwarded-Proto — dropping HSTS and the Secure
	// flag on auth cookies. Tightening the default needs a deployment audit,
	// not a flag flip.
	TrustedProxies []string `env:"TRUSTED_PROXIES" envSeparator:","`

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

	// PublicSegmentBaseURL, when set, prefixes the HLS playlist/segment URLs
	// handed to players so the bandwidth-heavy transcode traffic is fetched from
	// this host — typically a direct connection that bypasses a CDN/tunnel —
	// while the API and UI stay on the main host. Empty = same-origin (default).
	// A trailing slash is tolerated. See docs/deployment.md "Split segment access".
	PublicSegmentBaseURL string `env:"PUBLIC_SEGMENT_BASE_URL"`

	// ServerName is the human-friendly name advertised over LAN discovery
	// and surfaced in capability responses. Defaults to "OnScreen" if unset.
	ServerName string `env:"SERVER_NAME" envDefault:"OnScreen"`

	// DiscoveryPort is the UDP port the LAN discovery listener binds to.
	// Set DiscoveryEnabled=false to disable broadcasting entirely.
	DiscoveryEnabled bool `env:"DISCOVERY_ENABLED" envDefault:"true"`
	DiscoveryPort    int  `env:"DISCOVERY_PORT"    envDefault:"7368"`

	// ── RTMP ingest (live broadcast / "go live") ─────────────────────────────
	// When enabled, an embedded RTMP server accepts OBS/ffmpeg pushes and
	// exposes each authorized stream key as a Live TV channel. The listen port
	// (1935 is the RTMP standard) is separate from the HTTP API port and must
	// be reachable by broadcasters. A bind failure is non-fatal — the rest of
	// the server still starts. RTMPPublicHost is the hostname shown in the
	// broadcast admin UI's ingest URL (rtmp://<host>:<port>/live/<key>); when
	// empty the UI falls back to the request host.
	RTMPEnabled    bool   `env:"RTMP_ENABLED"     envDefault:"true"`
	RTMPListenAddr string `env:"RTMP_LISTEN_ADDR" envDefault:":1935"`
	RTMPPublicHost string `env:"RTMP_PUBLIC_HOST"`

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
	// TranscodeABR turns on the adaptive-bitrate HLS ladder: the server
	// emits a multi-rendition master playlist and the player switches
	// rungs on real-time bandwidth. Off keeps the single-rendition path
	// (one ffmpeg per session) — the cheaper default. TranscodeABRMaxHeight
	// pins the ladder's top rung (0 = up to source resolution) so a
	// bandwidth- or cost-constrained fleet can cap renditions.
	TranscodeABR          bool `env:"TRANSCODE_ABR"            envDefault:"false"`
	TranscodeABRMaxHeight int  `env:"TRANSCODE_ABR_MAX_HEIGHT" envDefault:"0"`
	// TranscodeABRAutoMaxHeight is the soft ladder ceiling for AUTO playback
	// (no explicit client quality pick). Defaults to 1080: a client that hits
	// the transcode ladder is transcoding BECAUSE it can't direct-play, so it
	// doesn't need a 4K rung — and offering one makes the player oscillate up
	// into a 4K decode it can't sustain (each rung switch restarts ffmpeg and
	// re-probes the source over HTTP on a fleet worker), thrashing playback.
	// An explicit quality pick overrides this; the hard TranscodeABRMaxHeight
	// cap still applies on top. Set 0 to let Auto build rungs to source height.
	TranscodeABRAutoMaxHeight int `env:"TRANSCODE_ABR_AUTO_MAX_HEIGHT" envDefault:"1080"`
	// TranscodeQSVDecode opts a worker into Intel Quick Sync hardware HEVC
	// decode (`-hwaccel qsv -c:v hevc_qsv`), offloading the 4K HEVC decode
	// from the CPU. Off by default and opt-in per worker: HW decode has
	// historically been fragile on mainline ffmpeg across sources/drivers
	// (the all-CUDA pipeline was retired for exactly this), so a worker only
	// turns it on once its QSV stack is known good. Decoded frames are
	// downloaded to system memory, so the existing software scale/tonemap
	// chain and the chosen encoder (NVENC/AMF/software) run unchanged — no
	// cross-GPU surface sharing. Only applies to HEVC sources on a re-encode.
	// If a source fails to decode on QSV, disable the flag for that worker.
	TranscodeQSVDecode bool `env:"TRANSCODE_QSV_DECODE" envDefault:"false"`
	// TranscodeQSVVRAM uses the full-VRAM Intel QSV path: QSV decodes into VA
	// surfaces (`-hwaccel qsv -hwaccel_output_format qsv`), vpp_qsv scales in GPU
	// memory, and a QSV encoder reads those surfaces — the Intel analogue of the
	// NVDEC→scale_cuda→NVENC zero-copy path. ON by default ("run in VRAM when
	// possible"): it only activates when a QSV encoder is actually selected (SDR-
	// only, same GPU decodes + encodes), and the worker falls back to software
	// decode per-job if the VRAM pipeline fails before the first segment. Validated
	// on the Intel UHD iGPU and Arc A770. Set false to force the software path.
	TranscodeQSVVRAM bool `env:"TRANSCODE_QSV_VRAM" envDefault:"true"`
	// TranscodeVAAPIVRAM is the VAAPI equivalent of TranscodeQSVVRAM: VAAPI
	// hardware-decodes into VA surfaces (`-hwaccel vaapi -hwaccel_output_format
	// vaapi`), scale_vaapi scales in GPU memory, and a VAAPI encoder reads those
	// surfaces (no software decode, no hwupload). ON by default; activates only when
	// a VAAPI encoder is selected, with the same per-job software fallback. Validated
	// on the Arc A770 — cut host CPU load ~65% vs software decode at equal load. Set
	// false to force the software path.
	TranscodeVAAPIVRAM bool `env:"TRANSCODE_VAAPI_VRAM" envDefault:"true"`
	// TranscodeEncoderFailover lets a worker fail a job over to the next encode
	// provider it has configured when its hardware encoder can't acquire the GPU —
	// most commonly a GeForce NVENC worker hitting the driver's 8-concurrent-session
	// cap, which then spills onto the box's Intel iGPU (QSV), then software. On by
	// default; only fires on boxes that actually have a second provider, so it's a
	// no-op on single-GPU workers. Set false to force a hard failure instead.
	TranscodeEncoderFailover bool `env:"TRANSCODE_ENCODER_FAILOVER" envDefault:"true"`
	// AutoMigrate runs pending embedded DB migrations on startup, before serving.
	// Off by default — most deploys apply migrations as an explicit step (Docker
	// migrate service, installer migrate.sh, `server migrate`). Set true for
	// single-container deploys (e.g. the TrueNAS Custom App) where there's no
	// separate migrate step, so a code update that adds migrations can't end up
	// serving against a stale schema (which surfaces as login 401s — the auth
	// query selects columns the migration adds).
	AutoMigrate bool `env:"AUTO_MIGRATE" envDefault:"false"`
	// PublicAssetCache makes immutable, user-independent assets (resized artwork)
	// emit `Cache-Control: public` instead of `private`, so a shared CDN / cache
	// fronting the server can store them and take the cacheable-majority off the
	// app tier (HA roadmap §4). Off by default — the safe posture is to let only
	// the browser cache auth'd responses. Set true only when a CDN is deployed in
	// front (configure it to key /artwork on the URL, ignoring the ?token= param,
	// since the resized bytes are identical for every user). Object-storage
	// deployments don't need this — those bytes already offload via signed URLs.
	PublicAssetCache bool `env:"PUBLIC_ASSET_CACHE" envDefault:"false"`
	// StaticABREnabled turns on the static-ABR pre-encode background task: it
	// pre-encodes the ABR ladder for the most-played titles to the media store so
	// their segments serve from object storage / CDN instead of the live-transcode
	// fleet (HA roadmap §5). Off by default — a pass spawns ffmpeg encodes and is
	// really only worthwhile with object storage + a CDN in front. StaticABRRoot
	// is the key prefix for the output: leave empty for object storage
	// (bucket-relative), or set a directory for a local static root.
	StaticABREnabled bool   `env:"STATIC_ABR_ENABLED" envDefault:"false"`
	StaticABRRoot    string `env:"STATIC_ABR_ROOT"`
	// SiteID names this deployment's site for multi-site DR / geo-distribution
	// (HA roadmap §6). Surfaced on /health/cluster alongside the Postgres role
	// (primary vs standby) and replication lag so operators and geo-routing can
	// tell which site, and whether it's writable, served a request. Empty for a
	// single-site deployment.
	SiteID string `env:"SITE_ID"`
	// NodeID is this node's stable identity, used to key its row in the
	// node_settings table (per-node config managed from the admin UI). Empty
	// defaults to the host name at startup. This is irreducible bootstrap — a
	// node must know who it is before it can read its own per-node config.
	NodeID string `env:"NODE_ID"`
	// IgnoreNodeDBConfig boots from env/defaults only, skipping the node_settings
	// row entirely. Break-glass for recovering a node locked out by a bad UI
	// value (e.g. an unreachable LISTEN_ADDR).
	IgnoreNodeDBConfig bool `env:"IGNORE_NODE_DB_CONFIG" envDefault:"false"`
	// Per-encoder tuning (hot-reloadable via SIGHUP). These let operators tune
	// for specific GPU models and upload bandwidth without rebuilding.
	TranscodeNVENCPreset  string  `env:"TRANSCODE_NVENC_PRESET"     envDefault:"p4"`
	TranscodeNVENCTune    string  `env:"TRANSCODE_NVENC_TUNE"       envDefault:"hq"`
	TranscodeNVENCRC      string  `env:"TRANSCODE_NVENC_RC"         envDefault:"vbr"`
	TranscodeMaxrateRatio float64 `env:"TRANSCODE_MAXRATE_RATIO"    envDefault:"1.5"`

	// ── Metadata ─────────────────────────────────────────────────────────────
	TMDBAPIKey    string `env:"TMDB_API_KEY"`
	TMDBRateLimit int    `env:"TMDB_RATE_LIMIT" envDefault:"5"` // req/s — conservative; TMDB auto-throttles abusive keys
	TVDBAPIKey    string `env:"TVDB_API_KEY"`                   // TheTVDB v4 project key; enables episode fallback

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
	if c.NodeID == "" {
		if host, err := os.Hostname(); err == nil && host != "" {
			c.NodeID = host
		} else {
			c.NodeID = "node"
		}
	}
	if c.CachePath == "" {
		home, _ := os.UserHomeDir()
		c.CachePath = filepath.Join(home, ".onscreen", "cache", "artwork")
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

// CacheSubdir returns a named subdirectory under the cache root (CachePath).
// Every on-disk cache — artwork, tmdb, animedb, photos, subtitles (+ OCR
// workdirs), trickplay, livetv, dvr — MUST root through here.
//
// This exists to kill a recurring bug: computing a cache path with
// filepath.Dir(CachePath) climbs a level ABOVE the writable cache-volume mount
// (e.g. CACHE_PATH=/var/cache/onscreen → /var/cache, which is root-owned and
// read-only in the locked-down container). That shipped twice — first nearly
// for animedb, then for real in the subtitle/OCR cache, where it silently
// broke every OCR job and OpenSubtitle download with "mkdir … permission
// denied". Joining downward from CachePath can't make that mistake.
func (c *Config) CacheSubdir(name string) string {
	return filepath.Join(c.CachePath, name)
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

	// Apply reloadable changes.
	//
	// Scan concurrency is intentionally NOT reloaded here: it moved to the admin
	// UI (Settings ▸ System) as a restart-required override merged onto cfg at
	// startup. Re-reading the env on SIGHUP would silently revert a UI-set value,
	// so we leave the startup (merged) value in place until the next restart.
	h.mu.Lock()
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
