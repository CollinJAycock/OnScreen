// cmd/server is the OnScreen API server entrypoint.
// It wires all dependencies, starts the HTTP server, and handles graceful shutdown.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	httppprof "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/onscreen/onscreen/internal/api"
	"github.com/onscreen/onscreen/internal/api/middleware"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/artwork"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/db"
	"github.com/onscreen/onscreen/internal/db/gen"
	dbmigrations "github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/discovery"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/people"
	"github.com/onscreen/onscreen/internal/domain/ratings"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/domain/watchevent"
	"github.com/onscreen/onscreen/internal/domain/watchlimit"
	"github.com/onscreen/onscreen/internal/domain/watchstatus"
	"github.com/onscreen/onscreen/internal/email"
	"github.com/onscreen/onscreen/internal/intromarker"
	"github.com/onscreen/onscreen/internal/livetv"
	"github.com/onscreen/onscreen/internal/lyrics"
	"github.com/onscreen/onscreen/internal/mediastore"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/anilist"
	"github.com/onscreen/onscreen/internal/metadata/animedb"
	"github.com/onscreen/onscreen/internal/metadata/audiodb"
	"github.com/onscreen/onscreen/internal/metadata/coverartarchive"
	"github.com/onscreen/onscreen/internal/metadata/tmdb"
	"github.com/onscreen/onscreen/internal/metadata/tvdb"
	"github.com/onscreen/onscreen/internal/notification"
	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/photoimage"
	"github.com/onscreen/onscreen/internal/plugin"
	"github.com/onscreen/onscreen/internal/preencode"
	"github.com/onscreen/onscreen/internal/requests"
	"github.com/onscreen/onscreen/internal/safehttp"
	"github.com/onscreen/onscreen/internal/scanner"
	"github.com/onscreen/onscreen/internal/scheduler"
	"github.com/onscreen/onscreen/internal/scrobble"
	"github.com/onscreen/onscreen/internal/staticabr"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/subtitles"
	"github.com/onscreen/onscreen/internal/subtitles/ocr"
	"github.com/onscreen/onscreen/internal/transcode"
	"github.com/onscreen/onscreen/internal/trickplay"
	"github.com/onscreen/onscreen/internal/valkey"
	"github.com/onscreen/onscreen/internal/worker"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql "pgx" driver for goose migrate
	"github.com/pressly/goose/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// version and buildTime are injected by the Makefile via ldflags.
var (
	version   = "dev"
	buildTime = "unknown"
)

// defaultTMDBAPIKey / defaultOpenSubtitlesAPIKey are OnScreen's own registered
// application keys, injected at release-build time via ldflags
// (-X main.defaultTMDBAPIKey=...). They're the LAST fallback — an operator's
// own key (Settings, then env) always wins — so a fresh install gets working
// metadata + subtitle search without every operator registering and BYO-ing a
// personal key (which is what got one rate-limited/banned at app scale: TMDB
// and OpenSubtitles rate-limit per-IP for a registered application, so a shared
// application key spread across many self-hosters behaves, the Jellyfin model).
// Empty on an un-stamped build, in which case behaviour is unchanged
// (operator-key-or-nothing).
var (
	defaultTMDBAPIKey          = ""
	defaultOpenSubtitlesAPIKey = ""
)

func main() {
	// `server.exe migrate` applies the embedded DB migrations and exits.
	// By default the server does NOT auto-migrate (it only gates on schema
	// version); deployments apply migrations explicitly — Docker via
	// `goose up`, the Linux installer via migrate.sh, and the Windows
	// installer by invoking this subcommand from postinstall before the
	// service starts. The exception is AUTO_MIGRATE=true (see run()), for
	// single-container deploys with no separate migrate step.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := runMigrate(); err != nil {
			fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// runMigrate applies all pending embedded migrations to DATABASE_URL using
// goose, then returns. Idempotent — goose only runs what's missing.
func runMigrate() error {
	loadDotEnv()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL not set")
	}
	db, err := goose.OpenDBWithDriver("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(dbmigrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	fmt.Println("migrations applied")
	return nil
}

// loadDotEnv populates process env vars from a `.env` file if one exists.
//
// Search order: working directory first, then the directory the binary
// itself lives in. Existing env vars always win — vars set by WinSW
// (service deploy), Docker, or a shell `set`/`export` are not
// overwritten. Missing file is silent; unreadable file is silent.
//
// Format mirrors `.env.dev` and the PowerShell start.ps1 parser:
//   - blank lines and `#` comments are ignored
//   - `KEY=value` and `export KEY=value` both work
//   - surrounding double-or-single quotes are stripped
func loadDotEnv() {
	candidates := []string{".env"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".env"))
	}
	for _, path := range candidates {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "export ")
			eq := strings.IndexByte(line, '=')
			if eq <= 0 {
				continue
			}
			key := strings.TrimSpace(line[:eq])
			val := strings.TrimSpace(line[eq+1:])
			if (strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`)) ||
				(strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`)) {
				val = val[1 : len(val)-1]
			}
			if _, set := os.LookupEnv(key); !set {
				_ = os.Setenv(key, val)
			}
		}
		_ = f.Close()
		return // first hit wins
	}
}

func run() error {
	// Auto-load .env so the binary works out of the box from a fresh
	// install dir without needing a wrapper script. Looks first in the
	// current working directory (dev/start.ps1 case), then next to the
	// executable (C:\OnScreen\server.exe invoked from anywhere else).
	// Existing env vars always win — WinSW-injected service env and
	// shell-exported vars take precedence over the file.
	loadDotEnv()

	// ── Config ────────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// ── Bootstrap settings (one-shot pgx.Conn, no pool) ──────────────────────
	// General config (BaseURL, LogLevel, CORS) lives in server_settings; we
	// read it before the logger and HTTP handlers are built. Missing row /
	// missing table degrades to GeneralConfig{} so fresh installs still boot.
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 5*time.Second)
	generalCfg := settings.LoadGeneralConfig(bootCtx, cfg.DatabaseURL)
	bootCancel()

	// ── Logging ───────────────────────────────────────────────────────────────
	logLevel, err := observability.NewLogLevelVar(generalCfg.LogLevel)
	if err != nil {
		return fmt.Errorf("log level: %w", err)
	}
	logger, logBuffer := observability.NewLogger(logLevel)
	slog.SetDefault(logger)

	logger.Info("starting onscreen server", "version", version, "build_time", buildTime)

	// AUTO_MIGRATE: apply pending embedded migrations before anything queries the
	// DB. For single-container deploys (e.g. the TrueNAS Custom App) with no
	// separate migrate step — without it, a code update that adds migrations
	// serves against a stale schema and login 401s. Fail fast if it errors:
	// better to not boot than to serve broken auth that looks like bad passwords.
	if cfg.AutoMigrate {
		logger.Info("AUTO_MIGRATE set — applying pending DB migrations")
		if err := runMigrate(); err != nil {
			return fmt.Errorf("auto-migrate: %w", err)
		}
	}

	// Make the bundled ffmpeg (next to server.exe in the installer layout)
	// resolvable for the embedded worker + live-tv/trickplay exec calls, even
	// when this process didn't inherit the service XML's PATH.
	transcode.EnsureFFmpegOnPath(logger)

	// Resolve BaseURL — settings value wins; fall back to localhost on the
	// configured listen addr so the discovery info and OAuth redirects have
	// a sensible default before an admin sets the public URL.
	baseURL := generalCfg.BaseURL
	if baseURL == "" {
		scheme := "http"
		if cfg.TLSEnabled() {
			scheme = "https"
		}
		baseURL = scheme + "://localhost" + cfg.ListenAddr
	}
	corsAllowedOrigins := generalCfg.CORSAllowedOrigins

	// ── Prometheus ────────────────────────────────────────────────────────────
	promReg := prometheus.NewRegistry()
	promReg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	metrics := observability.NewMetrics(promReg)

	// ── Database (rw + ro, ADR-021) ───────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── OpenTelemetry tracing ────────────────────────────────────────────────
	// Tracing config lives in server_settings (admin Settings → Observability).
	// We bootstrap a one-shot pgx.Conn here — no pool, no instrumentation —
	// because the TracerProvider must be in place BEFORE the instrumented
	// pgxpool is built (otelpgx caches the global TP at NewWithConfig time).
	// Disabled when no Endpoint is set; missing settings table degrades to off.
	otelCfg := settings.LoadOTelConfig(ctx, cfg.DatabaseURL)
	otelEndpoint := ""
	if otelCfg.Enabled {
		otelEndpoint = otelCfg.Endpoint
	}
	tp, err := observability.NewTracerProvider(ctx, observability.TracerOptions{
		Endpoint:       otelEndpoint,
		ServiceName:    "onscreen",
		ServiceVersion: version,
		DeploymentEnv:  otelCfg.DeploymentEnv,
		SampleRatio:    otelCfg.SampleRatio,
	})
	if err != nil {
		return fmt.Errorf("tracing: %w", err)
	}
	defer observability.ShutdownTracer(context.Background(), tp)
	if tp != nil {
		logger.Info("otel tracing enabled", "endpoint", otelCfg.Endpoint, "sample_ratio", otelCfg.SampleRatio)
	}

	// Time every query into onscreen_db_query_duration_seconds, labelled by SQL
	// verb (bounded cardinality). Wraps the otelpgx tracer, doesn't replace it.
	dbObserver := db.WithQueryObserver(func(verb string, seconds float64) {
		metrics.DBQueryDuration.WithLabelValues(verb).Observe(seconds)
	})

	rwPool, err := db.NewPool(ctx, cfg.DatabaseURL, dbObserver)
	if err != nil {
		return fmt.Errorf("rw db pool: %w", err)
	}
	defer rwPool.Close()

	roPool, err := db.NewPool(ctx, cfg.DatabaseROURL, dbObserver)
	if err != nil {
		return fmt.Errorf("ro db pool: %w", err)
	}
	defer roPool.Close()

	// ── Valkey ────────────────────────────────────────────────────────────────
	// Sentinel (HA) when VALKEY_SENTINEL_ADDRS is set, else a single node.
	var valkeyClient *valkey.Client
	if len(cfg.ValkeySentinelAddrs) > 0 {
		logger.Info("connecting to Valkey via Sentinel", "master", cfg.ValkeySentinelMaster, "sentinels", cfg.ValkeySentinelAddrs)
		valkeyClient, err = valkey.NewFailover(ctx, cfg.ValkeySentinelMaster, cfg.ValkeySentinelAddrs, cfg.ValkeySentinelPassword, cfg.ValkeyURL)
	} else {
		valkeyClient, err = valkey.New(ctx, cfg.ValkeyURL)
	}
	if err != nil {
		return fmt.Errorf("valkey: %w", err)
	}
	defer valkeyClient.Close()

	// ── Auth ──────────────────────────────────────────────────────────────────
	secretKey := auth.DeriveKey32(cfg.SecretKey)
	tokenMaker, err := auth.NewTokenMaker(secretKey)
	if err != nil {
		return fmt.Errorf("token maker: %w", err)
	}
	encryptor, err := auth.NewEncryptor(secretKey)
	if err != nil {
		return fmt.Errorf("encryptor: %w", err)
	}

	// ── Rate limiter ──────────────────────────────────────────────────────────
	rateLimiter := valkey.NewRateLimiter(valkeyClient, logger,
		func() { metrics.RateLimitFailOpen.Inc() })

	// ── Domain services ───────────────────────────────────────────────────────
	rwQ := &libraryAdapter{q: gen.New(rwPool)}
	roQ := &libraryAdapter{q: gen.New(roPool)}
	rwMQ := &mediaAdapter{q: gen.New(rwPool)}
	roMQ := &mediaAdapter{q: gen.New(roPool)}

	mediaSvc := media.NewService(rwMQ, roMQ, logger)

	// ── Settings service ─────────────────────────────────────────────────────
	// WithEncryptor turns on AES-256-GCM at-rest for secret-bearing keys
	// (TMDB/TVDB API keys, OIDC client_secret, SAML SP private key, LDAP
	// bind password, SMTP password, OpenSubtitles password). Existing
	// plaintext rows keep working — they migrate to encrypted form on
	// the next admin save of that setting.
	settingsSvc := settings.New(rwPool, logger).WithEncryptor(encryptor)

	// Merge cluster-wide System settings over the env-derived config: a value set
	// in the admin UI wins, otherwise the env var / built-in default stands. Done
	// here, before any consumer reads cfg, so the rest of startup is unchanged.
	// Only cluster-wide, startup-read knobs live here — node/site-specific config
	// (connection strings, bind addrs, paths, SITE_ID, per-worker hardware) stays
	// env, since server_settings replicates across sites in multi-site DR.
	applySystemSettings(ctx, cfg, settingsSvc.System(ctx), logger)
	// Same idea for the transcode output ceilings (Settings ▸ Transcode).
	applyTranscodeConfig(cfg, settingsSvc.TranscodeConfigGet(ctx), logger)
	// Per-node config (Settings ▸ Nodes): this node's row wins over env, unless
	// IGNORE_NODE_DB_CONFIG is set (break-glass for a bad bind address).
	if !cfg.IgnoreNodeDBConfig {
		applyNodeSettings(cfg, settingsSvc.NodeSettingsGet(ctx, cfg.NodeID), logger)
	}
	// UI-managed HTTPS: serve TLS from an admin-uploaded cert when no env TLS
	// file paths are set. nil → plain HTTP (or env-file TLS below).
	dbTLSConfig := loadDBTLSConfig(ctx, settingsSvc, cfg, logger)

	// ── Hot-reloadable config (ADR-027) ───────────────────────────────────────
	// Built from the *merged* cfg so a UI-set scan concurrency is the startup
	// value. SIGHUP reload no longer mutates scan concurrency (config.Reload), so
	// the env value can't silently clobber the UI override on a reload.
	hot := config.NewHotReloadable(cfg)
	config.WatchSIGHUP(logger, hot, cfg)

	// Seed TMDB key from environment on first run (won't overwrite a DB value).
	if cfg.TMDBAPIKey != "" {
		if settingsSvc.TMDBAPIKey(ctx) == "" {
			if err := settingsSvc.SetTMDBAPIKey(ctx, cfg.TMDBAPIKey); err != nil {
				logger.Warn("failed to seed TMDB key from env", "err", err)
			}
		}
	}

	// ── Artwork ───────────────────────────────────────────────────────────────
	// Pre-flight the cache directory at boot — when it's not writable (the
	// most common cause is a container running as a non-root user that can't
	// create `~/.onscreen/cache/artwork` because `~` is read-only or
	// non-existent), every artwork request silently re-resizes from scratch
	// (animedb hits the same path and complains too). Log a loud error
	// once at startup with the actionable fix so the operator doesn't have
	// to diagnose it via slow-page symptoms.
	if err := os.MkdirAll(cfg.CachePath, 0o755); err != nil {
		logger.Error("artwork cache path is not writable — resize results will NOT be cached, "+
			"every artwork request will re-decode the source. Set CACHE_PATH to a writable directory "+
			"(e.g. /var/lib/onscreen/cache) and restart.",
			"cache_path", cfg.CachePath, "err", err)
	} else if probe, err := os.CreateTemp(cfg.CachePath, "writable-probe-*.tmp"); err != nil {
		logger.Error("artwork cache path exists but rejects writes — same impact as above (no caching).",
			"cache_path", cfg.CachePath, "err", err)
	} else {
		_ = probe.Close()
		_ = os.Remove(probe.Name())
	}
	artworkMgr := artwork.New(cfg.CachePath).WithLogger(logger)

	// ── Photo image server ────────────────────────────────────────────────────
	// Shares the cache root with artwork but uses a different subdirectory so
	// the two pipelines don't collide on cache key SHA prefixes.
	photoImageSrv := photoimage.New(cfg.CacheSubdir("photos"))

	// ── Metadata enricher ─────────────────────────────────────────────────────
	// agentFn is called per newly discovered file and returns a TMDB client
	// built from the current DB setting, or nil if no key is configured.
	// This allows changing the API key at runtime without restarting.
	var (
		agentMu    sync.Mutex
		agentKey   string
		agentCache metadata.Agent
	)
	agentFn := func() metadata.Agent {
		// Use a non-cancellable context so that scan goroutines (which outlive
		// the signal context) can still read the TMDB key during shutdown drain.
		key := settingsSvc.TMDBAPIKey(context.WithoutCancel(ctx))
		if key == "" {
			key = cfg.TMDBAPIKey // fallback: env var (used before migration runs)
		}
		if key == "" {
			key = defaultTMDBAPIKey // last resort: OnScreen's bundled application key
		}
		agentMu.Lock()
		defer agentMu.Unlock()
		// Don't poison a previously-good cache with an empty live read.
		// settings.get returns "" on transient DB errors as well as "key
		// not set" — under load (bulk re-enrich hammering this) the
		// pool can drop a query, return "", and the old code would
		// cache nil indefinitely until the read returned something
		// different. Real bug from QA: 18 of 20 bulk re-enrich items
		// silently no-op'd against an apparently-unconfigured TMDB
		// agent while the row was sitting in server_settings the
		// whole time. The /api/v1/settings PATCH path explicitly
		// clears the agent on a real key change (see SetTMDBAPIKey
		// callers), so the only legitimate "go to nil" trigger is
		// admin action, not a transient read failure.
		if key == "" {
			return agentCache
		}
		if key != agentKey {
			agentKey = key
			// The rate provider is read live by the client on every
			// request, so admin-UI changes to System ▸ TMDB Rate Limit
			// take effect without a key rotation or server restart.
			agentCache = tmdb.New(key, cfg.TMDBRateLimit, "").
				WithRateProvider(func() int { return cfg.TMDBRateLimit }).
				// Disk-backed response cache (tmdb subdir under the cache
				// volume, same convention as animedb/photos). Slashes the
				// repeat traffic that got a real key revoked — see cache.go.
				WithCacheDir(cfg.CacheSubdir("tmdb"))
		}
		return agentCache
	}
	// scanPathsFn returns all active library scan paths — used by the enricher
	// to convert absolute artwork paths to paths relative to the library root.
	// Initially returns an empty slice; once libSvc is created we replace it.
	var scanPathsFn func() []string
	scanPathsFn = func() []string { return nil }
	// artworkRootsFn returns the same paths grouped by library so the artwork
	// handler can ACL-check against the owning library before serving a file.
	// Reassigned below once libSvc exists. Unlike scanPathsFn it isn't passed
	// into any closure before the reassignment, so no placeholder is needed.
	var artworkRootsFn func() []api.ArtworkRoot
	metaAgent := scanner.NewEnricher(agentFn, artworkMgr, mediaSvc, func() []string { return scanPathsFn() }, logger)

	// Wire TVDB fallback — reads key from DB setting, falls back to env var.
	// Uses lazy init so the key can be set at runtime via the settings UI.
	var (
		tvdbMu    sync.Mutex
		tvdbKey   string
		tvdbCache scanner.TVDBFallback
	)
	metaAgent.SetTVDBFallbackFn(func() scanner.TVDBFallback {
		key := settingsSvc.TVDBAPIKey(context.WithoutCancel(ctx))
		if key == "" {
			key = cfg.TVDBAPIKey
		}
		if key == "" {
			return nil
		}
		tvdbMu.Lock()
		defer tvdbMu.Unlock()
		if key != tvdbKey {
			tvdbKey = key
			tvdbCache = tvdb.New(key)
			logger.Info("tvdb fallback enabled for TV episode enrichment")
		}
		return tvdbCache
	})

	// Wire AniList anime metadata client. No API key required (read
	// queries are open). AniList sits between TMDB and TVDB in the
	// show-enrichment fallback chain — anime is the most common
	// reason TMDB returns no result for a TV show, and AniList is
	// anime-native. Singleton client; lazy factory shape kept for
	// parity with TVDB and future per-library opt-out.
	anilistClient := anilist.New()
	metaAgent.SetAniListFn(func() scanner.AniListAgent { return anilistClient })

	// Offline title→AniList-ID resolver (manami-project anime-offline-
	// database). Recovers fansub-style folder names AniList live
	// search misses ("Akame ga Kill Theater" → "Akame ga Kill!
	// Gaiden: Theater"). The dataset auto-refreshes weekly; first
	// load downloads ~30 MB JSON, subsequent boots hit the cached
	// file. Cache lives in an "animedb" subdir UNDER the cache root —
	// same convention as photos/livetv/dvr — so the existing writable
	// volume mount (CACHE_PATH, /var/cache/onscreen in the container)
	// covers it without new env wiring. (Using filepath.Dir here would
	// put it a level up, e.g. /var/cache, which is root-owned in the
	// locked-down container and not writable by the runtime user.)
	// Open is best-effort: if even this dir is read-only it falls back
	// to a temp dir, and a total failure logs a warning while live
	// AniList search remains the resolution path.
	animeDBCachePath := cfg.CacheSubdir("animedb")
	animeDBClient := animedb.New(animeDBCachePath, logger)
	go func() {
		// Run in background so a slow first-time download doesn't block
		// boot. Subsequent calls to Lookup return false until Open
		// completes — that's the fall-through-to-live-search behaviour
		// we want during the grace window.
		openCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := animeDBClient.Open(openCtx); err != nil {
			logger.Warn("animedb open failed; offline fallback disabled until next refresh",
				"err", err)
		}
	}()
	metaAgent.SetAnimeDBFn(func() scanner.AnimeDBLookup { return animeDBClient })

	// Per-library is_anime flag promotes AniList from fallback to
	// primary on libraries the admin has flagged as anime. Reads
	// against the read replica (the flag is only updated from the
	// library settings PATCH path; staleness across replication lag
	// just means the next scan picks the new value up). The
	// libraryAdapter satisfies scanner.LibraryAnimeChecker via its
	// IsLibraryAnime method.
	metaAgent.SetLibraryAnimeCheckerFn(func() scanner.LibraryAnimeChecker { return roQ })

	// Wire TheAudioDB for music enrichment — no API key required.
	audiodbClient := audiodb.New()
	metaAgent.SetMusicAgentFn(func() metadata.MusicAgent { return audiodbClient })

	// Cover Art Archive fallback runs after TheAudioDB for albums with
	// MusicBrainz IDs in their tags but no TheAudioDB match. CAA
	// indexes indie / classical / compilation releases TheAudioDB
	// doesn't track, closing a chunk of the "missing album art" gap.
	// No API key required.
	caaClient := coverartarchive.New()
	metaAgent.SetAlbumCoverByMBIDFn(func() scanner.AlbumCoverByMBIDAgent { return caaClient })

	libScanner := scanner.New(mediaSvc, metaAgent, hot, logger).WithMetrics(metrics)
	notifBrokerEarly := notification.NewBroker()
	notifServiceEarly := notification.NewService(gen.New(rwPool), notifBrokerEarly, logger)
	libEnqueuer := &scanEnqueuer{
		scanner:       libScanner,
		libSvc:        nil,
		db:            gen.New(rwPool),
		logger:        logger,
		serverCtx:     ctx,
		watchers:      make(map[uuid.UUID]*scanner.Watcher),
		notifService:  notifServiceEarly,
		settingsSvc:   settingsSvc,
		introDetector: intromarker.New(rwPool, mediaSvc, logger),
	}
	libSvc := library.NewService(rwQ, roQ, libEnqueuer, logger)
	libEnqueuer.libSvc = libSvc

	// Now that libSvc exists, wire up scanPathsFn for artwork path resolution
	// and artworkRootsFn for ACL-scoped artwork serving.
	scanPathsFn = func() []string {
		libs, err := libSvc.List(context.WithoutCancel(ctx))
		if err != nil {
			return nil
		}
		var paths []string
		for _, lib := range libs {
			paths = append(paths, lib.Paths...)
		}
		return paths
	}
	artworkRootsFn = func() []api.ArtworkRoot {
		libs, err := libSvc.List(context.WithoutCancel(ctx))
		if err != nil {
			return nil
		}
		roots := make([]api.ArtworkRoot, 0, len(libs))
		for _, lib := range libs {
			roots = append(roots, api.ArtworkRoot{LibraryID: lib.ID, Paths: lib.Paths})
		}
		return roots
	}

	// Start watching all libraries that already exist (from a previous run).
	if existingLibs, err := libSvc.List(ctx); err == nil {
		for _, lib := range existingLibs {
			lib := lib
			libEnqueuer.watchLibrary(lib.ID, lib.Paths)
		}
	} else {
		logger.Warn("could not load libraries for fs watching", "err", err)
	}

	// External scrobbling (ListenBrainz) — per-user, opt-in. The dispatcher is
	// fed completed-play events by the watch-event service below and submits
	// listens through the SSRF-guarded public client.
	scrobbleStore := scrobble.NewStore(rwPool, encryptor)
	scrobbleSvc := scrobble.NewService(scrobbleStore, scrobbleStore, safehttp.Default(), logger)
	scrobbleHandler := v1.NewScrobbleHandler(scrobbleStore)

	// Parental watch limits — per-user daily cap + allowed-hours, enforced at
	// the progress + transcode-start chokepoints and surfaced to clients via
	// the watch-limit endpoints. Raw-SQL store over the read-write pool.
	watchLimitStore := watchlimit.NewStore(rwPool)
	watchLimitHandler := v1.NewWatchLimitHandler(watchLimitStore)

	// Watch event service (Phase 2).
	rwWQ := &watchEventAdapter{q: gen.New(rwPool)}
	roWQ := &watchEventAdapter{q: gen.New(roPool)}
	watchSvc := watchevent.NewService(rwWQ, roWQ, logger).
		WithMetrics(metrics).
		WithScrobbleHook(scrobbleSvc.OnScrobble)

	// Watching-status mirror — Plan to Watch / Watching / Completed /
	// On Hold / Dropped. Generic per-(user, item) feature shipped to
	// land anime-tracker UX while applying to every type.
	watchStatusSvc := watchstatus.New(&watchStatusAdapter{q: gen.New(rwPool)})

	// ── Transcode session store + segment token (Phase 2) ─────────────────────
	sessionStore := transcode.NewSessionStore(valkeyClient).WithMetrics(metrics)
	segTokenMgr := transcode.NewSegmentTokenManager(valkeyClient)

	// ── API handlers ──────────────────────────────────────────────────────────
	authSvc := &authService{
		db:          gen.New(rwPool),
		tokens:      tokenMaker,
		enc:         encryptor, // at-rest encryption for the TOTP secret
		logger:      logger,
		rateLimiter: rateLimiter, // per-username brute-force throttle
		// usernamePepper keys the HMAC used for Valkey rate-limit keys
		// and any operator-log fields that would otherwise carry raw
		// attempted usernames. SECRET_KEY is already a per-deployment
		// secret so re-using it as the pepper costs nothing and avoids
		// introducing a separate config knob.
		usernamePepper: secretKey,
	}

	authMiddleware := middleware.NewAuthenticator(tokenMaker).
		WithEpochReader(&sessionEpochAdapter{q: gen.New(roPool)})

	auditLogger := audit.New(gen.New(rwPool), logger)
	libHandler := v1.NewLibraryHandler(libSvc, logger).
		WithMedia(mediaSvc).
		WithDetector(libEnqueuer.introDetector).
		WithAudit(auditLogger)
	webhookSvc := newWebhookService(gen.New(rwPool), encryptor, logger)
	webhookHandler := v1.NewWebhookHandler(webhookSvc, logger).WithAudit(auditLogger)

	authHandler := v1.NewAuthHandler(authSvc, logger).WithAudit(auditLogger)
	totpHandler := v1.NewTOTPHandler(authSvc, logger).WithAudit(auditLogger)

	// Native device pairing — short-lived PIN codes stored in Valkey, claimed
	// by an authenticated browser session to authorise a TV/phone.
	pairHandler := v1.NewPairHandler(
		&pairStore{v: valkeyClient},
		pairTokenIssuer(authSvc, gen.New(rwPool)),
		logger,
	)

	userSvc := newUserService(gen.New(rwPool))

	userHandler := v1.NewUserHandler(userSvc).
		WithDB(gen.New(rwPool)).
		WithTokenMaker(tokenMaker, logger).
		WithAudit(auditLogger).
		WithLibraryAccess(&userLibraryAccessAdapter{lib: libSvc, q: gen.New(roPool)}).
		WithSegmentTokenRevoker(segTokenMgr).
		WithPINThrottle(rateLimiter).
		WithPinSwitchPolicy(settingsSvc)
	fsHandler := v1.NewFSHandler()
	settingsHandler := v1.NewSettingsHandler(settingsSvc, logger).WithAudit(auditLogger)
	settingsHandler.SetWorkerLister(sessionStore)
	// Effective System knobs (env merged with any stored overrides above) so the
	// System settings page shows running values where no override is set.
	settingsHandler.SetSystemDefaults(systemEffective(cfg))
	// Effective transcode output ceilings, so the Transcode page shows the
	// running caps when the stored TranscodeConfig values are unset (0).
	settingsHandler.SetTranscodeDefaults(transcodeEffective(cfg))
	// Lets the TLS endpoint report the env-file source and block UI uploads.
	settingsHandler.SetTLSEnvConfigured(cfg.TLSEnabled())
	// Identifies the current node + its effective per-node config for Settings ▸ Nodes.
	settingsHandler.SetNodeIdentity(cfg.NodeID, nodeEffective(cfg))
	// Worker-node connection secrets, revealed via step-up reauth (authSvc).
	settingsHandler.SetWorkerCredentials(authSvc, cfg.DatabaseURL, cfg.ValkeyURL, cfg.SecretKey)
	auditHandler := v1.NewAuditHandler(gen.New(roPool), logger)
	streamTracker := streaming.NewValkeyTracker(valkeyClient)
	analyticsHandler := v1.NewAnalyticsHandler(gen.New(roPool), logger)
	hubHandler := v1.NewHubHandler(gen.New(roPool), logger).
		WithLibraryAccess(libSvc).
		WithLibraries(libSvc).
		WithEpisodePoster(gen.New(roPool))
	searchHandler := v1.NewSearchHandler(gen.New(roPool), logger).WithLibraryAccess(libSvc).WithEpisodePoster(gen.New(roPool))
	historyHandler := v1.NewHistoryHandler(gen.New(roPool), logger).WithLibraryAccess(libSvc).WithEpisodePoster(gen.New(roPool))
	nativeSessionsHandler := v1.NewNativeSessionsHandler(sessionStore, streamTracker, gen.New(roPool), logger)
	// Stable per-server identifier surfaced on the public capabilities endpoint,
	// UDP discovery, and webhook payloads. Persisted in settings rather than
	// derived from the master SECRET_KEY (the old approach published a bare hash
	// of the secret and re-derived it every boot). resolveMachineID seeds it once.
	machineID := resolveMachineID(ctx, settingsSvc, gen.New(rwPool), cfg.SecretKey, logger)

	// Capabilities — public describing-document for native clients that just
	// found the server via discovery. Reads settings on each call so toggling
	// OIDC in the admin UI takes effect immediately.
	capsProvider := &capabilitiesProvider{
		cfg:       cfg,
		version:   version,
		machineID: machineID,
		settings:  settingsSvc,
	}
	capabilitiesHandler := v1.NewCapabilitiesHandler(capsProvider)

	pluginRegistry := plugin.NewRegistry(gen.New(rwPool))
	pluginDispatcher := plugin.NewNotificationDispatcher(pluginRegistry, logger, auditLogger)
	pluginHandler := v1.NewPluginHandler(pluginRegistry, pluginDispatcher, logger).WithAudit(auditLogger)
	webhookDispatcher := worker.NewWebhookDispatcher(
		gen.New(rwPool),
		mediaSvc,
		encryptor,
		worker.WebhookServerInfo{Title: "OnScreen", MachineID: machineID},
		logger,
	).WithPluginNotifier(pluginDispatcher).WithMetrics(metrics)
	libEnqueuer.webhookDispatcher = webhookDispatcher
	matchAdapter := &matchSearchAdapter{enricher: metaAgent}
	arrAdapter := &arrLibraryAdapter{libSvc: libSvc, scanner: libEnqueuer}
	arrHandler := v1.NewArrHandler(settingsSvc, arrAdapter, logger)
	favoritesHandler := v1.NewFavoritesHandler(gen.New(rwPool), logger).WithLibraryAccess(libSvc)
	favoritesChecker := &favoritesChecker{q: gen.New(roPool)}
	// Media-byte backend (HA roadmap §3). Built from persisted storage settings —
	// defaults to local FS when object storage isn't enabled, so existing installs
	// are unchanged. Wrapped in a Provider so the storage settings endpoint can
	// hot-swap the backend (e.g. enable S3) without a restart.
	initialStore, err := v1.BuildMediaStore(settingsSvc.Storage(ctx))
	if err != nil {
		logger.Warn("media store init from settings failed; using local FS", "err", err)
		initialStore = mediastore.Local{}
	}
	mediaStoreProvider := mediastore.NewProvider(initialStore)
	settingsHandler.SetMediaStoreProvider(mediaStoreProvider)
	// Read source artwork through the same backend (resize cache stays local).
	artworkMgr.WithMediaStore(mediaStoreProvider)
	// Scanner reads discovery + stat + hash + ffprobe source through the store too,
	// so object-storage libraries scan (movies/shows/anime fully; types needing
	// embedded-tag/cover reads degrade to online enrichment for remote sources).
	libScanner.WithMediaStore(mediaStoreProvider)

	nativeTranscodeHandler := v1.NewNativeTranscodeHandler(sessionStore, segTokenMgr, mediaSvc, cfg, logger).
		WithLibraryAccess(libSvc).
		WithWatchLimit(watchLimitStore).
		WithAudit(auditLogger).
		// Lets a remote worker without shared storage pull the source from this
		// server over HTTP (a per-file stream token in the job's SourceURL).
		WithStreamTokenMaker(tokenMaker).
		// Prefer an object-storage / CDN signed source URL when configured.
		WithMediaStore(mediaStoreProvider).
		// Per-user admin caps (concurrent streams + bitrate), read at start.
		WithUserCaps(streamCapsAdapter{q: gen.New(roPool)})

	// Attach the SECRET_KEY-derived bearer to every API→worker segment/seghead
	// proxy request, matching what each worker's segment server now requires.
	v1.SetWorkerSegToken(transcode.SegmentProxyToken(cfg.SecretKey))

	// ── Trickplay (seekbar thumbnail previews) ───────────────────────────────
	// rootDir holds sprite_NNN.jpg + index.vtt per item. Lives alongside the
	// artwork resize cache; both are regenerable and safe to nuke.
	trickplayRoot := cfg.CachePath
	if trickplayRoot == "" {
		trickplayRoot = filepath.Join(os.TempDir(), "onscreen-trickplay")
	} else {
		trickplayRoot = filepath.Join(filepath.Dir(trickplayRoot), "trickplay")
	}
	trickplayStore := trickplay.NewStore(rwPool)
	trickplayGen := trickplay.NewWithService(trickplayRoot, trickplayStore, mediaSvc, logger)
	trickplaySvc := trickplay.NewService(trickplayGen, trickplayStore)
	trickplayHandler := v1.NewTrickplayHandler(trickplaySvc, mediaSvc, logger).
		WithLibraryAccess(libSvc)

	// ── External subtitles (OpenSubtitles, etc.) ─────────────────────────────
	// On-disk *.vtt files keyed by file id, plus the OCR working dirs. Roots
	// under the writable cache volume via CacheSubdir (see its doc — this path
	// is exactly where the filepath.Dir misuse silently broke OCR + downloads).
	subtitleCacheRoot := cfg.CacheSubdir("subtitles")
	if cfg.CachePath == "" {
		subtitleCacheRoot = filepath.Join(os.TempDir(), "onscreen-subtitles")
	}
	// Provider is dynamic: it re-reads settings on each call and rebuilds the
	// underlying client when credentials change, so users don't need to restart
	// the server after adding or updating an OpenSubtitles key.
	subtitleProvider := subtitles.NewDynamicProvider(func(ctx context.Context) subtitles.OpenSubtitlesCreds {
		osc := settingsSvc.OpenSubtitles(ctx)
		apiKey := osc.APIKey
		if apiKey == "" {
			// Fall back to OnScreen's bundled application key so an operator who
			// enables subtitle search doesn't also have to register their own
			// key. Enabled stays operator-controlled — the bundled key removes
			// the registration friction, not the opt-in.
			apiKey = defaultOpenSubtitlesAPIKey
		}
		return subtitles.OpenSubtitlesCreds{
			Enabled:  osc.Enabled,
			APIKey:   apiKey,
			Username: osc.Username,
			Password: osc.Password,
		}
	}, "")
	subtitleSvc := subtitles.New(subtitleProvider, gen.New(rwPool), subtitleCacheRoot, logger)
	// OCR engine wires PGS/VOBSUB → WebVTT via ffmpeg + tesseract. The binaries
	// ship in the runtime image; if they're missing here (dev box without them
	// installed) OCR endpoints return ErrNoOCR — no crash at startup.
	ocrEngine := &ocr.Engine{Logger: logger}
	if err := ocrEngine.Available(); err != nil {
		logger.Warn("ocr disabled — required binaries missing", "err", err)
	} else {
		subtitleSvc.SetOCR(ocrEngine)
		logger.Info("subtitle OCR enabled (ffmpeg + tesseract on PATH)")
	}
	subtitleHandler := v1.NewSubtitleHandler(subtitleSvc, mediaSvc, logger).
		WithLibraryAccess(libSvc)

	// People service has to land here (vs the per-handler section
	// below) so the items handler can hook the cast/crew refresher
	// into ApplyMatch's background goroutine. peopleHandler is wired
	// further down with the rest of the v1 handlers.
	peopleQ := &peopleAdapter{q: gen.New(rwPool)}
	peopleAgentFn := func() people.Agent {
		a := agentFn()
		if a == nil {
			return nil
		}
		if pa, ok := a.(people.Agent); ok {
			return pa
		}
		return nil
	}
	peopleSvc := people.New(peopleQ, peopleAgentFn)

	ratingsSvc := ratings.New(gen.New(rwPool))
	itemHandler := v1.NewItemHandler(mediaSvc, watchSvc, sessionStore, metaAgent, matchAdapter, webhookDispatcher, favoritesChecker, streamTracker, logger).
		WithEpisodePoster(gen.New(roPool)).
		WithLibraryAccess(libSvc).
		WithWatchLimit(watchLimitStore).
		WithMarkers(intromarker.NewStore(rwPool)).
		WithExternalSubtitles(subtitleSvc).
		WithSubtitleCache(subtitleCacheRoot).
		WithSyncBroker(notifBrokerEarly).
		WithAudit(auditLogger).
		WithStreamTokenMaker(tokenMaker).
		WithPosterPicker(metaAgent).
		WithSubtreeDeleter(&subtreeDeleter{q: gen.New(rwPool)}).
		WithCreditsRefresher(peopleSvc).
		WithDownloadGate(settingsSvc).
		WithRatings(ratingsSvc).
		WithMediaStore(mediaStoreProvider)

	photosHandler := v1.NewPhotosHandler(mediaSvc, photoImageSrv, logger).
		WithLibraryAccess(libSvc)

	booksHandler := v1.NewBookHandler(mediaSvc, logger).
		WithLibraryAccess(libSvc)

	// ── Live TV ──────────────────────────────────────────────────────────────
	// Phase A: tuner abstraction + HDHomeRun and M3U drivers; channels list +
	// now/next display + HLS proxy. DVR scheduling lives in Phase B and slots
	// in here through the same service.
	liveTVRegistry := livetv.NewRegistry()
	liveTVRegistry.Register(livetv.TunerTypeHDHomeRun, livetv.HDHomeRunFactory)
	liveTVRegistry.Register(livetv.TunerTypeM3U, livetv.M3UFactory)

	// Embedded RTMP ingest ("go live"): broadcasters push via OBS/ffmpeg to
	// rtmp://host:1935/live/<key>; each authorized stream key surfaces as a
	// Live TV channel through the same Driver → HLS proxy → player path (and
	// records to the DVR for free). The factory needs the shared server
	// instance; the authorizer needs the service, so the server is started
	// after the service is constructed below.
	var rtmpServer *livetv.RTMPServer
	if cfg.RTMPEnabled {
		rtmpServer = livetv.NewRTMPServer(cfg.RTMPListenAddr, logger)
		liveTVRegistry.Register(livetv.TunerTypeRTMP, livetv.RTMPFactory(rtmpServer))
	}

	liveTVSvc := livetv.NewService(newLiveTVAdapter(gen.New(rwPool)), liveTVRegistry, logger)

	if rtmpServer != nil {
		rtmpServer.SetAuthorizer(liveTVSvc.AuthorizeRTMPKey)
		if err := rtmpServer.Start(); err != nil {
			// Non-fatal: a port conflict shouldn't take down the whole
			// server. Live TV (tuners, DVR) still works; only push ingest
			// is unavailable.
			logger.Warn("rtmp ingest disabled (listen failed)",
				"addr", cfg.RTMPListenAddr, "err", err)
			rtmpServer = nil
		} else {
			defer rtmpServer.Close()
		}
	}
	// Encoder selection is deferred until after the encoder detection
	// block below — see liveTVProxy/liveTVHandler construction there.
	var liveTVProxy *livetv.HLSProxy
	var liveTVHandler *v1.LiveTVHandler

	// ── Embedded transcode worker ─────────────────────────────────────────────
	// Runs FFmpeg in-process so a separate cmd/worker binary is not required for
	// single-node deployments. Encoder detection is best-effort; falls back to
	// libx264 software encoding if ffmpeg/hardware is unavailable.
	//
	// Priority: fleet config (DB) > DISABLE_EMBEDDED_WORKER env > default enabled.
	fleetCfg := settingsSvc.WorkerFleet(ctx)

	// Always auto-detect hardware for the settings UI encoder dropdown.
	allEncoders, err := transcode.DetectEncoders(ctx, "")
	if err != nil {
		logger.Warn("encoder detection failed, defaulting to software", "err", err)
		allEncoders = []transcode.Encoder{transcode.EncoderSoftware}
	}
	settingsHandler.SetDetectedEncoders(transcode.EncoderEntries(ctx, allEncoders))
	settingsHandler.SetEmbeddedDisabled(cfg.DisableEmbeddedWorker)

	// Pick a Live TV video encoder: first H.264 encoder available,
	// preferring hardware. Broadcast TS is typically MPEG-2 (US OTA) or
	// H.264 (newer); browsers can't play MPEG-2 in HLS, so we always
	// re-encode to H.264. Hardware encoders make this near-free.
	liveTVEncoder := "libx264"
	for _, e := range allEncoders {
		if e == transcode.EncoderNVENC || e == transcode.Encoder("h264_amf") || e == transcode.Encoder("h264_qsv") {
			liveTVEncoder = string(e)
			break
		}
	}
	logger.Info("live tv encoder selected", "encoder", liveTVEncoder)
	liveTVProxy = livetv.NewHLSProxy(livetv.HLSConfig{
		Dir:          cfg.CacheSubdir("livetv"),
		VideoEncoder: liveTVEncoder,
	}, liveTVSvc, logger)
	liveTVHandler = v1.NewLiveTVHandler(liveTVSvc, logger).
		WithStreamProxy(liveTVProxy).
		// Parental watch limits apply to live viewing too — the live tab was
		// the one playback surface entirely outside the policy.
		WithWatchLimit(watchLimitStore)
	if rtmpServer != nil {
		// Enables the broadcast ("go live") admin endpoints: ingest URL +
		// live status for RTMP devices.
		liveTVHandler = liveTVHandler.WithRTMP(rtmpServer, cfg.RTMPPublicHost, cfg.RTMPListenAddr)
	}

	// DVR: matcher + recording worker. Recordings land in
	// {CachePath}/dvr by default — users should point a "dvr" library
	// at that path so the scanner surfaces finalized captures.
	dvrQueries := newDVRAdapter(gen.New(rwPool), newLiveTVAdapter(gen.New(rwPool)))
	dvrSvc := livetv.NewDVRService(dvrQueries, liveTVSvc,
		cfg.CacheSubdir("dvr"), logger)
	dvrWorker := livetv.NewDVRWorker(livetv.DVRWorkerConfig{
		RecordDir: cfg.CacheSubdir("dvr"),
	}, dvrQueries, liveTVSvc,
		// Library resolver: find the first enabled library of type 'dvr'.
		func(ctx context.Context) (uuid.UUID, error) {
			libs, err := libSvc.List(ctx)
			if err != nil {
				return uuid.Nil, err
			}
			for _, l := range libs {
				if l.Type == "dvr" {
					return l.ID, nil
				}
			}
			return uuid.Nil, nil
		},
		&dvrMediaCreator{svc: mediaSvc},
		logger)
	go func() {
		// Run forever; context cancellation on server shutdown stops it.
		_ = dvrWorker.Run(ctx, 5*time.Second)
	}()
	liveTVHandler.WithDVR(dvrSvc)

	// Lyrics: tag-sourced at scan time, LRCLIB fallback on first GET.
	lyricsHandler := v1.NewLyricsHandler(
		&lyricsStoreAdapter{q: gen.New(rwPool)},
		&lyricsItemAdapter{svc: mediaSvc},
		lyrics.NewLRCLIBClient(),
		logger,
	).WithLibraryAccess(libSvc)

	// Populate runtime-detected capabilities now that Live TV + encoder
	// detection are both wired. The capabilities handler is published
	// via Handlers struct construction which happens later, but
	// Capabilities() isn't called until the HTTP server starts listening,
	// so setting fields here is safe without locking.
	// Max concurrent transcodes is per-worker; report the embedded
	// worker's cap since it's the only one guaranteed to exist. Remote
	// workers that join later can lift the ceiling — clients shouldn't
	// treat this as a hard wall.
	maxTranscodes := 12
	capsProvider.setRuntimeDetected(
		true, // Live TV is always wired in this build
		0,    // tune count is per-tuner; aggregate not meaningful here
		transcode.EncoderNames(allEncoders),
		maxTranscodes,
	)

	embeddedEnabled := fleetCfg.EmbeddedEnabled && !cfg.DisableEmbeddedWorker
	var embeddedWorker *transcode.Worker
	if embeddedEnabled {
		// Encoder priority: fleet config > DB transcode_encoders > env > auto-detect.
		encoderOverride := fleetCfg.EmbeddedEncoder
		if encoderOverride == "" {
			encoderOverride = settingsSvc.TranscodeEncoders(ctx)
		}
		if encoderOverride == "" {
			encoderOverride = cfg.TranscodeEncoders
		}
		var encoders []transcode.Encoder
		if encoderOverride != "" {
			encoders = transcode.ParseOverride(encoderOverride)
		} else {
			encoders = allEncoders
		}
		// Safety: never use an encoder that wasn't actually detected.
		encoders = transcode.FilterAvailable(encoders, allEncoders)
		logger.Info("transcode encoders", "active", transcode.EncoderNames(encoders), "detected", transcode.EncoderNames(allEncoders))
		// Session cap: admin fleet override (set live + persisted via the UI)
		// wins over the TRANSCODE_MAX_SESSIONS env default.
		embeddedMaxSessions := cfg.TranscodeMaxSessions
		if fleetCfg.EmbeddedMax > 0 {
			embeddedMaxSessions = fleetCfg.EmbeddedMax
		}
		embeddedWorker = transcode.NewWorker(
			transcode.WorkerID(),
			"127.0.0.1:7073",
			sessionStore,
			encoders,
			embeddedMaxSessions,
			transcode.EncoderOpts{
				NVENCPreset:  cfg.TranscodeNVENCPreset,
				NVENCTune:    cfg.TranscodeNVENCTune,
				NVENCRC:      cfg.TranscodeNVENCRC,
				MaxrateRatio: cfg.TranscodeMaxrateRatio,
			},
			logger,
		)

		embeddedWorker.SetNodeID(cfg.NodeID)
		// Hardware-encoder fail-over (TRANSCODE_ENCODER_FAILOVER, default on): on a
		// box with two encode providers (e.g. an NVIDIA dGPU + AMD/Intel iGPU) a job
		// that can't acquire the primary GPU retries on the next provider instead of
		// failing. No-op when the box has a single provider.
		embeddedWorker.SetEncoderFailover(cfg.TranscodeEncoderFailover)
		// Require the SECRET_KEY-derived bearer on the embedded worker's segment
		// server too — it binds loopback, but enforce uniformly with remote workers.
		embeddedWorker.SetSegmentAuthToken(transcode.SegmentProxyToken(cfg.SecretKey))
		// Wire embedded worker into transcode handler so Stop can kill FFmpeg immediately.
		nativeTranscodeHandler.SetSessionKiller(embeddedWorker)
		// Wire into settings so the fleet UI can retune its session cap live.
		settingsHandler.SetEmbeddedWorker(embeddedWorker)
	} else {
		logger.Info("embedded transcode worker disabled — using remote workers only")
	}

	// ── Email / SMTP (settings-driven) ───────────────────────────────────────
	// Sender resolves SMTPConfig on every Send so admins can rotate creds and
	// flip Enabled from the UI without a restart. Sender is always non-nil;
	// callers gate flows on emailSender.Enabled(ctx).
	emailSender := email.NewSender(func(ctx context.Context) email.Config {
		c := settingsSvc.SMTP(ctx)
		return email.Config{
			Enabled:  c.Enabled,
			Host:     c.Host,
			Port:     c.Port,
			Username: c.Username,
			Password: c.Password,
			From:     c.From,
		}
	})

	emailHandler := v1.NewEmailHandler(emailSender, logger)
	passwordResetDB := &passwordResetAdapter{q: gen.New(rwPool)}
	passwordResetHandler := v1.NewPasswordResetHandler(passwordResetDB, emailSender, baseURL, logger).
		WithSegmentTokenRevoker(segTokenMgr).
		WithAudit(auditLogger)
	inviteDB := &inviteAdapter{q: gen.New(rwPool)}
	inviteHandler := v1.NewInviteHandler(inviteDB, emailSender, baseURL, logger).WithAudit(auditLogger)

	// ── OIDC + SAML + LDAP (settings-driven, always wired) ────────────────────
	// All three pull config from server_settings on each request, so admins
	// enable and reconfigure them through the UI without a restart.
	oidcSvc := v1.NewOIDCAuthService(gen.New(rwPool), authSvc.issueTokenPair, logger)
	oidcAuthHandler := v1.NewOIDCHandler(settingsSvc, oidcSvc, baseURL, logger).WithAudit(auditLogger)
	samlSvc := v1.NewSAMLAuthService(gen.New(rwPool), authSvc.issueTokenPair, logger)
	// HA-aware SAML request tracker — Valkey-backed so an AuthnRequest
	// minted on one OnScreen instance can be validated by an ACS
	// callback that hits a different instance behind a load balancer.
	// v2.1 Track A item 2. Single-instance dev still works either
	// way; using the Valkey tracker unconditionally keeps the prod
	// shape identical and removes the "works locally, breaks in HA"
	// surprise.
	samlAuthHandler := v1.NewSAMLHandler(settingsSvc, samlSvc, baseURL, logger).
		WithRequestTracker(v1.NewValkeySAMLRequestTracker(valkeyClient)).
		WithAudit(auditLogger)
	ldapSvc := v1.NewLDAPAuthService(settingsSvc, v1.DefaultLDAPDialer{}, gen.New(rwPool), authSvc.issueTokenPair, logger).
		WithThrottle(rateLimiter)
	ldapAuthHandler := v1.NewLDAPHandler(settingsSvc, ldapSvc, logger).WithAudit(auditLogger)

	// ── Notifications ────────────────────────────────────────────────────────
	_ = notifServiceEarly // used by scanEnqueuer above
	notifHandler := v1.NewNotificationHandler(gen.New(roPool), notifBrokerEarly, logger)

	// ── Cross-device playback transfer ───────────────────────────────────────
	playbackHandler := v1.NewPlaybackHandler(gen.New(roPool), notifBrokerEarly, logger)

	// ── Maintenance (admin one-shot operations) ──────────────────────────────
	maintenanceHandler := v1.NewMaintenanceHandler(mediaSvc, libSvc, rwMQ, metaAgent, logger)
	expectedSchemaVersion, err := dbmigrations.Highest()
	if err != nil {
		logger.Error("scan embedded migrations for schema version", "err", err)
		os.Exit(1)
	}
	backupHandler := v1.NewBackupHandler(cfg.DatabaseURL, expectedSchemaVersion, dbmigrations.FS, logger).WithAudit(auditLogger)

	// ── Media-request workflow + arr-services admin ──────────────────────────
	// Requests fan out to the arr instances configured in the arr_services
	// table (multi-instance from day one — separate 4K Radarr alongside the
	// 1080p one, etc.). The TMDB agent is consulted both for the title
	// snapshot at create time and to resolve TVDB ids for Sonarr lookups.
	requestsTMDB := &requestsTMDBAdapter{agentFn: agentFn}
	requestsSvc := requests.NewService(gen.New(rwPool), requestsTMDB, notifServiceEarly, logger).
		WithEncryptor(encryptor)
	requestsHandler := v1.NewRequestHandler(requestsSvc, logger).WithAudit(auditLogger)
	arrServicesHandler := v1.NewArrServicesHandler(gen.New(rwPool), logger).
		WithAudit(auditLogger).
		WithEncryptor(encryptor)
	// Discover reuses the same adapter so admins toggling the TMDB key
	// flow through to search results without a server restart.
	discoverHandler := v1.NewDiscoverHandler(gen.New(roPool), requestsTMDB, requestsSvc, logger)
	// Back-fill the scan enqueuer so post-scan goroutines can settle
	// any media-requests whose download just landed, and let the arr
	// webhook also fire a reconcile on Download events.
	libEnqueuer.requestsSvc = requestsSvc
	arrHandler.WithRequestReconciler(requestsSvc)

	// ── Scheduler (cron-driven admin tasks) ──────────────────────────────────
	// Registry holds handler implementations keyed by task_type. Built-ins
	// are registered here; future plugin-provided handlers register against
	// the same registry.
	schedRegistry := scheduler.NewRegistry()
	schedRegistry.Register("backup_database", scheduler.NewBackupHandler(cfg.DatabaseURL))
	libIDLister := scheduler.LibraryListerFunc(func(ctx context.Context) ([]uuid.UUID, error) {
		libs, err := libSvc.List(ctx)
		if err != nil {
			return nil, err
		}
		ids := make([]uuid.UUID, len(libs))
		for i, l := range libs {
			ids[i] = l.ID
		}
		return ids, nil
	})
	schedRegistry.Register("scan_library", scheduler.NewScanHandler(libEnqueuer, libIDLister))
	schedRegistry.Register("ocr_subtitles", scheduler.NewOCRHandler(
		mediaSvc,
		subtitleSvc,
		libIDLister,
		&ocrExistsChecker{q: gen.New(roPool)},
	))
	// EPG refresh: wakes every few minutes and pulls any EPG source
	// whose last_pull_at + refresh_interval_min is in the past.
	schedRegistry.Register("epg_refresh", scheduler.NewEPGRefreshHandler(liveTVSvc))
	// DVR matcher runs every minute, scans enabled schedules against
	// the upcoming EPG window, and upserts scheduled recordings.
	schedRegistry.Register("dvr_match", scheduler.NewDVRMatcherHandler(dvrSvc))
	// Retention purge runs daily off-peak, deletes recordings past
	// their schedule's retention_days window + the files on disk.
	schedRegistry.Register("dvr_retention", scheduler.NewDVRRetentionHandler(dvrSvc))
	// Periodic art-refresh sweep: catches partially-enriched libraries
	// that scanned before the operator added a TMDB key, after the
	// agent recovered from a transient outage, etc. Runs every couple
	// of hours so the recovery window is "by morning" not "operator
	// goes hunting for an admin endpoint".
	schedRegistry.Register("refresh_missing_art",
		scheduler.NewRefreshMissingArtHandler(mediaSvc, metaAgent, logger))
	// Rebuild the watch_plays materialized view off the request path so the
	// analytics dashboard reads a cheap indexed copy instead of recomputing a
	// full-history lead() window on every load (migration 00004).
	schedRegistry.Register("refresh_watch_plays",
		scheduler.NewRefreshWatchPlaysHandler(rwPool, logger))
	// Static-ABR pre-encode (HA roadmap §5). Off by default — wired only when
	// STATIC_ABR_ENABLED is set, since each pass spawns ffmpeg encodes and is
	// really worthwhile only with object storage + a CDN. It pre-encodes the
	// hottest titles' ABR ladders to the media store so their segments serve
	// statically instead of from the live-transcode fleet.
	if cfg.StaticABREnabled {
		staticSvc := staticabr.NewService(
			staticPopularity{q: gen.New(roPool)},
			staticResolver{media: mediaSvc},
			staticabr.StoreChecker{Store: mediaStoreProvider},
			preencode.New(mediaStoreProvider, cfg.StaticABRRoot, "ffmpeg", 6 /*seg sec*/, 0 /*no height cap*/, logger),
			3 /*min plays*/, 10 /*titles per run*/, logger,
		)
		schedRegistry.Register("static_abr_preencode", scheduler.NewStaticABRPreencodeHandler(staticSvc))
		// Serve pre-encoded ladders from the store on the playback path.
		nativeTranscodeHandler.WithStaticABR(cfg.StaticABRRoot)
		// Local time (see seedSystemTasks): keep the seeded first fire on the same
		// wall-clock the steady-state scheduler will use.
		if next, err := scheduler.NextRun("17 4 * * *", time.Now()); err == nil {
			if err := gen.New(rwPool).EnsureSystemTask(ctx, gen.EnsureSystemTaskParams{
				Name:      "Static-ABR pre-encode",
				TaskType:  "static_abr_preencode",
				CronExpr:  "17 4 * * *", // daily, off-peak
				Enabled:   true,
				NextRunAt: pgtype.Timestamptz{Time: next, Valid: true},
			}); err != nil {
				logger.WarnContext(ctx, "seed static_abr task", "err", err)
			}
		}
		logger.InfoContext(ctx, "static-ABR pre-encode enabled", "root", cfg.StaticABRRoot)
	}
	// Seed the scheduled_tasks rows our handlers depend on. Idempotent:
	// admin edits to existing rows are preserved (EnsureSystemTask uses
	// WHERE NOT EXISTS on task_type), so this is safe on every boot.
	// Without it, a fresh install has handlers registered in memory but
	// nothing to trigger them — DVR silently stops recording.
	seedSystemTasks(ctx, gen.New(rwPool), logger)
	sched := scheduler.New(scheduler.NewPgxQuerier(rwPool), schedRegistry, logger)
	tasksHandler := v1.NewTasksHandler(gen.New(rwPool), schedRegistry, logger)

	// Jobs status feed — drives the frontend "scanning…", "N items
	// missing art", "N items unmatched" banner. Reads from the live
	// scanEnqueuer for in-flight scans and from mediaSvc for the
	// snapshot counts; both are cheap. Mounted at /api/v1/jobs.
	jobsHandler := v1.NewJobsHandler(libEnqueuer, mediaSvc, &jobsLibNamer{libSvc: libSvc}, logger)

	// ── People (cast/crew) handler — lazy TMDB fetch on first item-detail
	// view. peopleSvc itself is constructed earlier (above the items
	// handler) so the items handler can wire it as a credits refresher.
	peopleHandler := v1.NewPeopleHandler(peopleSvc, &peopleItemLookup{
		svc:      mediaSvc,
		agentFn:  agentFn,
		enricher: metaAgent,
		logger:   logger,
	}, logger).WithLibraryAccess(libSvc)

	// ── Router ────────────────────────────────────────────────────────────────
	h := &api.Handlers{
		Library:         libHandler,
		Webhook:         webhookHandler,
		Auth:            authHandler,
		TOTP:            totpHandler,
		User:            userHandler,
		FS:              fsHandler,
		Settings:        settingsHandler,
		Analytics:       analyticsHandler,
		NativeSessions:  nativeSessionsHandler,
		Hub:             hubHandler,
		Search:          searchHandler,
		History:         historyHandler,
		Items:           itemHandler,
		ItemsAdmin:      v1.NewItemBulkAdminHandler(gen.New(rwPool), metaAgent, logger).WithAudit(auditLogger),
		WatchStatus:     v1.NewWatchStatusHandler(watchStatusSvc, logger),
		Photos:          photosHandler,
		Books:           booksHandler,
		Trickplay:       trickplayHandler,
		Subtitles:       subtitleHandler,
		NativeTranscode: nativeTranscodeHandler,
		Collections:     v1.NewCollectionHandler(gen.New(rwPool), logger).WithLibraryAccess(libSvc),
		Playlists:       v1.NewPlaylistHandler(gen.New(rwPool), logger).WithLibraryAccess(libSvc),
		PhotoAlbums:     v1.NewPhotoAlbumHandler(gen.New(rwPool), logger).WithLibraryAccess(libSvc),
		LiveTV:          liveTVHandler,
		Lyrics:          lyricsHandler,
		Arr:             arrHandler,
		OIDCAuth:        oidcAuthHandler,
		SAMLAuth:        samlAuthHandler,
		LDAPAuth:        ldapAuthHandler,
		Audit:           auditHandler,
		Email:           emailHandler,
		PasswordReset:   passwordResetHandler,
		Invite:          inviteHandler,
		Notifications:   notifHandler,
		Playback:        playbackHandler,
		Scrobble:        scrobbleHandler,
		WatchLimit:      watchLimitHandler,
		Maintenance:     maintenanceHandler,
		Backup:          backupHandler,
		Tasks:           tasksHandler,
		Jobs:            jobsHandler,
		People:          peopleHandler,
		Plugins:         pluginHandler,
		Pair:            pairHandler,
		Logs:            v1.NewLogsHandler(logBuffer),
		Debug:           v1.NewDebugHandler(logger).WithExplain(roPool),
		Capabilities:    capabilitiesHandler,
		ArrServices:     arrServicesHandler,
		Requests:        requestsHandler,
		Discover:        discoverHandler,
		Favorites:       favoritesHandler,
		StreamTracker:   streamTracker,
		Artwork:         artworkMgr,
		ArtworkRoots:    artworkRootsFn,
		LibraryAccess:   libSvc,
		// Resolves an artwork file's owning content rating so the artwork
		// server can enforce the per-user ceiling (see router.go). Read-only
		// pool; fails open (found=false) on miss or query error.
		ArtworkContentRating: func(ctx context.Context, libraryID uuid.UUID, relPath string) (string, bool) {
			rel := relPath
			rating, err := gen.New(roPool).GetArtworkContentRating(ctx, gen.GetArtworkContentRatingParams{
				LibraryID: libraryID,
				ArtPath:   &rel,
			})
			if err != nil || rating == nil {
				return "", false
			}
			return *rating, true
		},
		// Closure (not a static bool copy) so the artwork handler picks
		// up admin-UI toggles of public_asset_cache without a restart —
		// system_settings.applyToConfig mutates cfg.PublicAssetCache on
		// every settings save, and this captures cfg by pointer.
		PublicAssetCache:   func() bool { return cfg.PublicAssetCache },
		Logger:             logger,
		Metrics:            metrics,
		Auth_mw:            authMiddleware,
		Impersonate:        &impersonationAdapter{q: gen.New(rwPool)},
		ViewAsAuditor:      &viewAsAuditAdapter{audit: auditLogger},
		RateLimiter:        rateLimiter,
		CORSAllowedOrigins: corsAllowedOrigins,
		DevFrontendURL:     cfg.DevFrontendURL,
	}
	// Fail fast if a security-critical handler was built without its
	// per-library access checker — a nil checker fails open (serves every
	// library), so a forgotten .WithLibraryAccess() wiring must not boot.
	if err := h.ValidateLibraryAccess(); err != nil {
		logger.Error("startup: per-library access wiring check failed", "err", err)
		os.Exit(1)
	}
	// Trusted-proxy allowlist for X-Forwarded-*. A typo must fail startup
	// rather than silently trust nothing (breaking a proxied deployment)
	// or fall back to trusting everything.
	if err := middleware.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		logger.Error("startup: TRUSTED_PROXIES is invalid", "err", err)
		os.Exit(1)
	}
	if !middleware.TrustedProxiesConfigured() {
		logger.Warn("TRUSTED_PROXIES is unset: X-Forwarded-* is accepted from ANY " +
			"loopback or private-range peer. On a LAN install that is every client, " +
			"which lets one forge X-Forwarded-For to rotate its rate-limit key or the " +
			"audit-log IP. Set TRUSTED_PROXIES to your reverse proxy address.")
	}
	router := api.NewRouter(h)

	// ── Health endpoints ──────────────────────────────────────────────────────
	// Migration status fn: checks at startup AND on every /health/ready call,
	// so an operator who runs `docker exec ... goose up` sees the gate clear
	// without a container restart. Failures (e.g. goose_db_version missing on
	// a fresh DB) are reported as "unknown" rather than blocking readiness.
	versionQuerier := &db.PingablePool{Pool: rwPool}
	migrationStatusFn := func() (expected, applied, pending int64, ok bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
		defer cancel()
		st, err := observability.CheckMigrations(ctx, versionQuerier, dbmigrations.FS)
		if err != nil {
			return 0, 0, 0, false
		}
		return st.Expected, st.Applied, st.Pending, true
	}
	if st, err := observability.CheckMigrations(context.Background(), versionQuerier, dbmigrations.FS); err != nil {
		logger.Warn("could not check migration status at startup", "err", err)
	} else if st.Pending > 0 {
		logger.Error("schema is behind code — run `goose up` against the DB before serving traffic",
			"applied", st.Applied, "expected", st.Expected, "pending", st.Pending)
	} else {
		logger.Info("migration status", "applied", st.Applied, "expected", st.Expected)
	}

	liveH, readyH := observability.HealthHandler(
		versionQuerier,
		valkeyClient,
		migrationStatusFn,
		logger,
	)

	mainMux := http.NewServeMux()
	mainMux.Handle("/", router)
	mainMux.HandleFunc("/health/live", liveH)
	mainMux.HandleFunc("/health/ready", readyH)
	// Build provenance — what commit + build time is actually running, plus the
	// Go runtime. Stamped by ldflags (Makefile / Dockerfile build-args); reads
	// "dev"/"unknown" on an un-stamped local build. Lets a deploy be verified
	// with one curl instead of inferring the build from behaviour.
	mainMux.HandleFunc("/health/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"version":    version,
			"build_time": buildTime,
			"go":         runtime.Version(),
		})
	})
	// Multi-site DR surface: this node's site, Postgres role (primary/standby),
	// and replication lag (HA roadmap §6). Read by operators / geo-routing.
	mainMux.HandleFunc("/health/cluster", observability.ClusterStatusHandler(cfg.SiteID, rwPool, logger))

	// ── Metrics server (separate port, ADR) ──────────────────────────────────
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", observability.MetricsHandler(promReg))
	metricsMux.HandleFunc("/health/live", liveH)

	// pprof handlers — registered on the metrics port (NOT the public
	// API port) so they share the same operator-only attack surface
	// as Prometheus scrapes. Operators expose this port to the
	// monitoring network, never to the internet.
	//
	// Endpoints (under /debug/pprof/):
	//   - heap, allocs       — RSS / allocation profiling
	//   - goroutine          — find leaked goroutines + blocked sites
	//   - profile?seconds=30 — CPU sampling profile
	//   - trace?seconds=5    — execution trace for blocking analysis
	//
	// Registering by hand instead of `_ "net/http/pprof"` so the
	// handlers attach to our metricsMux, not the global DefaultServeMux
	// (which the API server doesn't use either way).
	metricsMux.HandleFunc("/debug/pprof/", httppprof.Index)
	metricsMux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
	metricsMux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
	metricsMux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
	metricsMux.HandleFunc("/debug/pprof/trace", httppprof.Trace)

	// ── Background workers ────────────────────────────────────────────────────
	// Provider so the partition worker reads the live System ▸ Watch
	// History Retention setting on each monthly tick — admin-UI changes
	// take effect without a server restart. See
	// worker.RetainMonthsProvider for the contract.
	partitionWorker := worker.NewPartitionWorker(rwPool, &hotRetainMonths{cfg: cfg}, logger)
	hubRefreshWorker := worker.NewHubRefreshWorker(rwPool, 5*time.Minute, logger).WithMetrics(metrics)
	periodicScanWorker := newPeriodicScanWorker(libSvc, libEnqueuer, logger)
	// masterLock ensures only one instance runs singleton workers (hub refresh,
	// partition maintenance, periodic scans). Any instance can take over if the
	// current master crashes — the lock TTL (15 s) bounds the failover window.
	masterLock := worker.NewMasterLock(valkeyClient, uuid.New().String(), logger)

	// ── Servers ───────────────────────────────────────────────────────────────
	apiServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mainMux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if dbTLSConfig != nil {
		apiServer.TLSConfig = dbTLSConfig
	}
	metricsServer := &http.Server{
		Addr:         cfg.MetricsAddr,
		Handler:      metricsMux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// ── Start everything ──────────────────────────────────────────────────────
	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		switch {
		case cfg.TLSEnabled():
			// Env file paths win (see loadDBTLSConfig).
			logger.Info("api server listening", "addr", cfg.ListenAddr, "tls", true, "cert", cfg.TLSCertFile)
			if err := apiServer.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("api server (tls): %w", err)
			}
		case apiServer.TLSConfig != nil:
			// UI-managed cert: empty strings make ListenAndServeTLS use the
			// in-memory certificate already on apiServer.TLSConfig.
			logger.Info("api server listening", "addr", cfg.ListenAddr, "tls", true, "cert", "admin-uploaded")
			if err := apiServer.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("api server (tls): %w", err)
			}
		default:
			logger.Info("api server listening", "addr", cfg.ListenAddr, "tls", false)
			if err := apiServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("api server: %w", err)
			}
		}
		return nil
	})

	g.Go(func() error {
		logger.Info("metrics server listening", "addr", cfg.MetricsAddr)
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("metrics server: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		masterLock.Run(gCtx)
		return nil
	})

	g.Go(func() error {
		masterLock.RunIfMaster(gCtx, partitionWorker.Run)
		return nil
	})

	g.Go(func() error {
		masterLock.RunIfMaster(gCtx, hubRefreshWorker.Run)
		return nil
	})

	g.Go(func() error {
		masterLock.RunIfMaster(gCtx, periodicScanWorker.Run)
		return nil
	})

	g.Go(func() error {
		masterLock.RunIfMaster(gCtx, sched.Run)
		return nil
	})

	if embeddedWorker != nil {
		g.Go(func() error {
			if err := embeddedWorker.Start(gCtx); err != nil {
				logger.Warn("transcode worker exited", "err", err)
			}
			return nil
		})
	}

	// LAN discovery — UDP listener so native clients (TVs, phones) can
	// auto-discover this server on the local network. Best-effort: if the
	// port is already in use we log and move on rather than failing startup.
	if cfg.DiscoveryEnabled {
		discInfo := func() discovery.ServerInfo {
			return discovery.ServerInfo{
				ServerName: cfg.ServerName,
				MachineID:  machineID,
				Version:    version,
				HTTPURL:    baseURL,
			}
		}
		discListener := discovery.NewListener(cfg.DiscoveryPort, discInfo, logger)
		g.Go(func() error {
			if err := discListener.Run(gCtx); err != nil {
				logger.Warn("discovery listener exited", "err", err)
			}
			return nil
		})
	}

	// Graceful shutdown on context cancellation (SIGTERM/SIGINT).
	g.Go(func() error {
		<-gCtx.Done()
		logger.Info("shutdown signal received")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := apiServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("api server shutdown error", "err", err)
		}
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("metrics server shutdown error", "err", err)
		}
		// Stop plugin workers after the HTTP servers so we don't drop
		// in-flight notifications triggered by requests already in progress.
		pluginDispatcher.Close()
		// Release the subtitle handler's background OCR-sweep goroutine.
		subtitleHandler.Close()
		return nil
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("server stopped")
	return nil
}

// resolveMachineID returns the server's stable public identifier, persisting it
// in settings on first boot so it is decoupled from the master SECRET_KEY.
//
// The old code derived it as uuid.NewSHA1(NameSpaceDNS, SECRET_KEY) on every
// boot, publishing a bare hash of the secret on the (unauthenticated)
// capabilities endpoint, UDP discovery, and webhook payloads. To avoid breaking
// already-deployed servers (clients, webhook consumers and discovery key off
// the identifier), an EXISTING install — one that already has users — keeps its
// historical derived value; a brand-new install (no users yet) gets a fresh
// random id with no relationship to the secret. After this one-time seed the id
// is read from storage, so a later key rotation no longer changes it.
func resolveMachineID(ctx context.Context, settingsSvc *settings.Service, q *gen.Queries, secretKey string, logger *slog.Logger) string {
	if id := settingsSvc.MachineID(ctx); id != "" {
		return id
	}
	n, err := q.CountUsers(ctx)
	if err != nil {
		// Can't tell fresh-vs-existing. Do NOT guess-and-persist: minting a fresh
		// random id for what is actually an existing install would permanently
		// change the machine identity and break paired clients, webhooks and
		// discovery. Fall back to the legacy derived id for THIS run only
		// (preserves an existing install's identity) and leave settings unwritten
		// so the next healthy boot seeds the correct value.
		logger.Warn("machine_id: count users failed; using legacy derived id for this run without persisting", "err", err)
		return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(secretKey)).String()
	}
	var id string
	if n > 0 {
		// Existing install: preserve identity (paired clients / webhooks / discovery).
		id = uuid.NewSHA1(uuid.NameSpaceDNS, []byte(secretKey)).String()
		logger.Info("machine_id: seeding persisted value from legacy derived id (existing install)")
	} else {
		// Fresh install: random id, no coupling to the secret key.
		id = uuid.NewString()
		logger.Info("machine_id: generated fresh random id (new install)")
	}
	if err := settingsSvc.SetMachineID(ctx, id); err != nil {
		logger.Warn("machine_id: persist failed; using value in-memory for this run", "err", err)
	}
	return id
}

// scanEnqueuer implements library.ScanEnqueuer and scanner.WatchTrigger.
// It manages per-library filesystem watchers and drives the real scanner.
// jobsLibNamer adapts library.Service to the v1.JobsLibraryNamer
// interface so the JobsHandler can decorate in-flight scan UUIDs with
// human-readable library names ("scanning Movies…" beats "scanning
// 5a8ac716-…"). NameOf returns ("", false) when the library can't be
// resolved — JobsHandler treats that as "fall through to the UUID",
// so a transient lookup failure doesn't black out the response.
// hotRetainMonths adapts *config.Config to worker.RetainMonthsProvider so the
// partition worker reads the live System ▸ Watch History Retention value on
// each monthly tick. Mirrors cmd/worker's adapter of the same name — each
// `main` package is separate so the type can't be shared without lifting it
// to a public package, which isn't worth a new internal package for two
// three-line types.
type hotRetainMonths struct {
	cfg *config.Config
}

func (h *hotRetainMonths) RetainMonths() int { return h.cfg.RetainMonths }

type jobsLibNamer struct {
	libSvc *library.Service
}

func (j *jobsLibNamer) NameOf(ctx context.Context, id uuid.UUID) (string, bool) {
	lib, err := j.libSvc.Get(ctx, id)
	if err != nil {
		return "", false
	}
	return lib.Name, true
}

type scanEnqueuer struct {
	scanner           *scanner.Scanner
	libSvc            *library.Service
	db                *gen.Queries // for hub refresh after scan
	logger            *slog.Logger
	serverCtx         context.Context // outlives individual HTTP requests
	webhookDispatcher *worker.WebhookDispatcher
	notifService      *notification.Service
	settingsSvc       *settings.Service
	introDetector     *intromarker.Detector
	// requestsSvc is consulted after every scan to flip approved/downloading
	// requests to available when the matching media_item lands. Optional —
	// nil disables fulfillment polling, matching the pre-Requests behaviour.
	requestsSvc *requests.Service

	watchMu      sync.Mutex
	watchers     map[uuid.UUID]*scanner.Watcher // one watcher per library
	scanInFlight sync.Map                       // uuid.UUID → struct{} — libraries currently scanning
	// detectInFlight collapses concurrent intro-detection runs for the
	// same library. A flapping fsnotify event over a large library
	// can trigger overlapping scan-completes; without this dedup,
	// each one spawns runIntroDetection in a fresh goroutine and the
	// detector ends up fingerprinting the same episodes N times in
	// parallel — burning CPU and writing competing intro_markers
	// rows for the same episode.
	detectInFlight sync.Map // uuid.UUID → struct{}
	// dirScanInFlight collapses concurrent watcher-triggered scans of the
	// same directory. A file being copied in (sonarr import) re-fires the
	// debounced fsnotify trigger every window for the duration of the copy;
	// without this dedup each trigger spawns its own ScanDirectory goroutine
	// and the same season folder gets parsed by several scans at once —
	// wasted I/O and a duplicate-row race on item attach. Skipping is safe:
	// writes still in progress fire the watcher again after the running scan
	// ends, and the periodic full scan backstops anything missed.
	dirScanInFlight sync.Map // "libraryID|dir" → struct{}
}

// InFlightScans returns the set of library IDs currently being scanned.
// Read-side surface for the /api/v1/jobs endpoint so the frontend can
// show a "scanning Movies…" banner without polling any heavier
// progress source. Snapshot semantics: a scan that completes between
// the InFlightScans call and the response is fine, the next poll
// will catch up.
func (e *scanEnqueuer) InFlightScans() []uuid.UUID {
	out := make([]uuid.UUID, 0)
	e.scanInFlight.Range(func(k, _ any) bool {
		if id, ok := k.(uuid.UUID); ok {
			out = append(out, id)
		}
		return true
	})
	return out
}

func (e *scanEnqueuer) EnqueueScan(ctx context.Context, libraryID uuid.UUID) error {
	// Deduplicate: skip if a scan is already running for this library.
	if _, loaded := e.scanInFlight.LoadOrStore(libraryID, struct{}{}); loaded {
		e.logger.Info("scan already in flight, skipping", "library_id", libraryID)
		return nil
	}
	lib, err := e.libSvc.Get(ctx, libraryID)
	if err != nil {
		e.scanInFlight.Delete(libraryID)
		return fmt.Errorf("get library for scan: %w", err)
	}
	e.logger.Info("scan enqueued", "library_id", libraryID, "paths", lib.Paths)
	go func() {
		defer e.scanInFlight.Delete(libraryID)
		scanCtx := context.WithoutCancel(e.serverCtx)
		result, err := e.scanner.ScanLibrary(scanCtx, libraryID, lib.Type, lib.Paths)
		if err != nil {
			e.logger.Error("scan failed", "library_id", libraryID, "err", err)
			return
		}
		e.logger.Info("scan finished",
			"library_id", libraryID,
			"found", result.Found,
			"new", result.New,
			"duration_ms", result.Duration.Milliseconds(),
		)
		// Reset the scan interval timer so periodic scans don't re-trigger immediately.
		if err := e.libSvc.MarkScanCompleted(context.WithoutCancel(e.serverCtx), libraryID); err != nil {
			e.logger.Warn("mark scan completed", "library_id", libraryID, "err", err)
		}
		// Refresh hub views so recently-added reflects the scan results immediately.
		if err := e.db.RefreshHubRecentlyAdded(context.WithoutCancel(e.serverCtx)); err != nil {
			e.logger.Warn("refresh hub after scan", "library_id", libraryID, "err", err)
		}
		// Ensure the library is being watched after its first scan.
		e.watchLibrary(libraryID, lib.Paths)
		// Dispatch library.scan.complete webhook event.
		if e.webhookDispatcher != nil {
			e.webhookDispatcher.Dispatch("library.scan.complete", uuid.Nil, uuid.Nil)
		}
		// Send in-app notification if new items were found.
		if e.notifService != nil && result.New > 0 {
			e.notifService.NotifyScanComplete(context.WithoutCancel(e.serverCtx), lib.Name, result.New)
		}
		// Intro/credits detection runs on show + anime libraries, and only
		// when the admin has left detection on auto. Movies are excluded —
		// users typically mark them manually if at all. Anime libraries
		// share the show → season → episode hierarchy and benefit even
		// more from auto-detection (every cour has its own OP/ED, and
		// fansub releases routinely vary intro length by ±2 s).
		if (lib.Type == "show" || lib.Type == "anime") && e.introDetector != nil && e.settingsSvc != nil {
			mode := e.settingsSvc.IntroDetectionMode(context.WithoutCancel(e.serverCtx))
			if mode == settings.IntroDetectionOnScan {
				go e.runIntroDetection(libraryID)
			}
		}
		// Settle any media-requests whose download just landed. Cheap — the
		// reconciler is bounded by active-request count, not library size.
		if e.requestsSvc != nil && result.New > 0 {
			e.requestsSvc.ReconcileFulfillments(context.WithoutCancel(e.serverCtx))
		}
	}()
	return nil
}

// runIntroDetection walks every season in a show library and kicks off
// intro + credits detection. Fire-and-forget: errors are logged per-season
// and never block or retry. Called only after a successful scan.
//
// Dedups concurrent calls per library via detectInFlight so a flapping
// scan-complete sequence (fsnotify storm during mass copy, manual
// rescan triggered while auto-scan is still finishing) doesn't stack
// overlapping detection runs on the same library.
func (e *scanEnqueuer) runIntroDetection(libraryID uuid.UUID) {
	if _, loaded := e.detectInFlight.LoadOrStore(libraryID, struct{}{}); loaded {
		e.logger.Info("intro detection already in flight; skipping duplicate run",
			"library_id", libraryID)
		return
	}
	defer e.detectInFlight.Delete(libraryID)

	detectCtx := context.WithoutCancel(e.serverCtx)
	if err := e.introDetector.DetectLibrary(detectCtx, libraryID); err != nil {
		e.logger.Warn("intro detection library",
			"library_id", libraryID, "err", err)
	}
}

// TriggerDirectoryScan implements scanner.WatchTrigger.
// Called by the per-library Watcher when fsnotify detects a change.
func (e *scanEnqueuer) TriggerDirectoryScan(_ context.Context, libraryID uuid.UUID, dirPath string) error {
	lib, err := e.libSvc.Get(e.serverCtx, libraryID)
	if err != nil {
		return fmt.Errorf("get library: %w", err)
	}
	inFlightKey := libraryID.String() + "|" + dirPath
	if _, loaded := e.dirScanInFlight.LoadOrStore(inFlightKey, struct{}{}); loaded {
		return nil // this directory is already being scanned; watcher re-fires on further writes
	}
	e.logger.Info("fs change detected, scanning directory",
		"library_id", libraryID, "dir", dirPath)
	go func() {
		defer e.dirScanInFlight.Delete(inFlightKey)
		scanCtx := context.WithoutCancel(e.serverCtx)
		// ScanDirectory walks just dirPath but parses each file against
		// the library's configured scan_paths. Calling ScanLibrary with
		// [dirPath] as paths confuses parseAudiobookPath /
		// parseMusicPath: they decide which ancestor is the library
		// root from the slice we pass in, so a watcher event on
		// `<lib>/<author>/<book>/` re-classified each file as a
		// "loose at root" file with no author folder above it. Result:
		// a totally different hierarchy than the manual scan, racing
		// to attach files to fresh audiobook rows.
		result, err := e.scanner.ScanDirectory(scanCtx, libraryID, lib.Type, dirPath, lib.Paths)
		if err != nil {
			e.logger.Error("directory scan failed",
				"library_id", libraryID, "dir", dirPath, "err", err)
			return
		}
		if result.New > 0 {
			e.logger.Info("directory scan found new files",
				"library_id", libraryID, "dir", dirPath,
				"new", result.New, "duration_ms", result.Duration.Milliseconds())
			// Refresh hub so new items appear in recently added.
			if err := e.db.RefreshHubRecentlyAdded(context.WithoutCancel(e.serverCtx)); err != nil {
				e.logger.Warn("refresh hub after dir scan", "err", err)
			}
			// Settle any media-requests whose download just landed. The arr
			// webhook also fires this directly, but a watcher-driven scan
			// (no webhook) needs its own trigger.
			if e.requestsSvc != nil {
				e.requestsSvc.ReconcileFulfillments(context.WithoutCancel(e.serverCtx))
			}
		}
	}()
	return nil
}

// watchLibrary starts a watcher for a library if one isn't already running.
// Safe to call multiple times for the same library.
func (e *scanEnqueuer) watchLibrary(libraryID uuid.UUID, paths []string) {
	e.watchMu.Lock()
	defer e.watchMu.Unlock()

	if _, exists := e.watchers[libraryID]; exists {
		return // already watching
	}

	w, err := scanner.NewWatcher(e, e.logger)
	if err != nil {
		e.logger.Warn("failed to create fs watcher", "library_id", libraryID, "err", err)
		return
	}
	if err := w.WatchLibrary(libraryID, paths); err != nil {
		e.logger.Warn("failed to watch library paths", "library_id", libraryID, "err", err)
		w.Close()
		return
	}
	e.watchers[libraryID] = w

	go w.Run(e.serverCtx, libraryID)
	e.logger.Info("watching library for changes", "library_id", libraryID, "paths", paths)
}

// periodicScanWorker checks every minute for libraries whose scan_interval has
// elapsed and enqueues a fresh scan. This is the fallback for environments
// (e.g. WSL watching a Windows-side drive) where fsnotify events are not
// delivered for changes made outside the Linux filesystem.
type periodicScanWorker struct {
	libSvc *library.Service
	enq    *scanEnqueuer
	logger *slog.Logger
}

func newPeriodicScanWorker(libSvc *library.Service, enq *scanEnqueuer, logger *slog.Logger) *periodicScanWorker {
	return &periodicScanWorker{libSvc: libSvc, enq: enq, logger: logger}
}

func (w *periodicScanWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

func (w *periodicScanWorker) tick(ctx context.Context) {
	libs, err := w.libSvc.ListDueForScan(ctx)
	if err != nil {
		w.logger.Warn("periodic scan: list due libraries", "err", err)
		return
	}
	for _, lib := range libs {
		lib := lib
		w.logger.Info("periodic scan: enqueueing", "library_id", lib.ID, "name", lib.Name)
		if err := w.enq.EnqueueScan(ctx, lib.ID); err != nil {
			w.logger.Warn("periodic scan: enqueue failed", "library_id", lib.ID, "err", err)
		}
	}
}
