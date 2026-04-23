// cmd/server is the OnScreen API server entrypoint.
// It wires all dependencies, starts the HTTP server, and handles graceful shutdown.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/onscreen/onscreen/internal/api"
	"github.com/onscreen/onscreen/internal/api/middleware"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/artwork"
	"github.com/onscreen/onscreen/internal/photoimage"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/db"
	"github.com/onscreen/onscreen/internal/db/gen"
	dbmigrations "github.com/onscreen/onscreen/internal/db/migrations"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/people"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/domain/watchevent"
	"github.com/onscreen/onscreen/internal/email"
	"github.com/onscreen/onscreen/internal/intromarker"
	"github.com/onscreen/onscreen/internal/livetv"
	"github.com/onscreen/onscreen/internal/lyrics"
	"github.com/onscreen/onscreen/internal/discovery"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/audiodb"
	"github.com/onscreen/onscreen/internal/metadata/tmdb"
	"github.com/onscreen/onscreen/internal/metadata/tvdb"
	"github.com/onscreen/onscreen/internal/notification"
	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/plugin"
	"github.com/onscreen/onscreen/internal/requests"
	"github.com/onscreen/onscreen/internal/scanner"
	"github.com/onscreen/onscreen/internal/scheduler"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/transcode"
	"github.com/onscreen/onscreen/internal/subtitles"
	"github.com/onscreen/onscreen/internal/subtitles/ocr"
	"github.com/onscreen/onscreen/internal/trickplay"
	"github.com/onscreen/onscreen/internal/valkey"
	"github.com/onscreen/onscreen/internal/worker"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// version and buildTime are injected by the Makefile via ldflags.
var (
	version   = "dev"
	buildTime = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
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
	logger := observability.NewLogger(logLevel)
	slog.SetDefault(logger)

	logger.Info("starting onscreen server", "version", version, "build_time", buildTime)

	// Resolve BaseURL — settings value wins; fall back to localhost on the
	// configured listen addr so the discovery info and OAuth redirects have
	// a sensible default before an admin sets the public URL.
	baseURL := generalCfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost" + cfg.ListenAddr
	}
	corsAllowedOrigins := generalCfg.CORSAllowedOrigins

	// ── Hot-reloadable config (ADR-027) ───────────────────────────────────────
	hot := config.NewHotReloadable(cfg)
	config.WatchSIGHUP(logger, hot, cfg)

	// ── Prometheus ────────────────────────────────────────────────────────────
	promReg := prometheus.NewRegistry()
	promReg.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
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

	rwPool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("rw db pool: %w", err)
	}
	defer rwPool.Close()

	roPool, err := db.NewPool(ctx, cfg.DatabaseROURL)
	if err != nil {
		return fmt.Errorf("ro db pool: %w", err)
	}
	defer roPool.Close()

	// ── Valkey ────────────────────────────────────────────────────────────────
	valkeyClient, err := valkey.New(ctx, cfg.ValkeyURL)
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
	settingsSvc := settings.New(rwPool, logger)

	// Seed TMDB key from environment on first run (won't overwrite a DB value).
	if cfg.TMDBAPIKey != "" {
		if settingsSvc.TMDBAPIKey(ctx) == "" {
			if err := settingsSvc.SetTMDBAPIKey(ctx, cfg.TMDBAPIKey); err != nil {
				logger.Warn("failed to seed TMDB key from env", "err", err)
			}
		}
	}

	// ── Artwork ───────────────────────────────────────────────────────────────
	artworkMgr := artwork.New(cfg.CachePath)

	// ── Photo image server ────────────────────────────────────────────────────
	// Shares the cache root with artwork but uses a different subdirectory so
	// the two pipelines don't collide on cache key SHA prefixes.
	photoImageSrv := photoimage.New(filepath.Join(cfg.CachePath, "photos"))

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
		agentMu.Lock()
		defer agentMu.Unlock()
		if key != agentKey {
			agentKey = key
			if key == "" {
				agentCache = nil
			} else {
				agentCache = tmdb.New(key, cfg.TMDBRateLimit, "")
			}
		}
		return agentCache
	}
	// scanPathsFn returns all active library scan paths — used by the enricher
	// to convert absolute artwork paths to paths relative to the library root,
	// and by the router to serve artwork files.
	// Initially returns an empty slice; once libSvc is created we replace it.
	var scanPathsFn func() []string
	scanPathsFn = func() []string { return nil }
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

	// Wire TheAudioDB for music enrichment — no API key required.
	audiodbClient := audiodb.New()
	metaAgent.SetMusicAgentFn(func() metadata.MusicAgent { return audiodbClient })

	libScanner := scanner.New(mediaSvc, metaAgent, hot, logger)
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

	// Now that libSvc exists, wire up scanPathsFn for artwork path resolution.
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

	// Start watching all libraries that already exist (from a previous run).
	if existingLibs, err := libSvc.List(ctx); err == nil {
		for _, lib := range existingLibs {
			lib := lib
			libEnqueuer.watchLibrary(lib.ID, lib.Paths)
		}
	} else {
		logger.Warn("could not load libraries for fs watching", "err", err)
	}

	// Watch event service (Phase 2).
	rwWQ := &watchEventAdapter{q: gen.New(rwPool)}
	roWQ := &watchEventAdapter{q: gen.New(roPool)}
	watchSvc := watchevent.NewService(rwWQ, roWQ, logger)

	// ── Transcode session store + segment token (Phase 2) ─────────────────────
	sessionStore := transcode.NewSessionStore(valkeyClient)
	segTokenMgr := transcode.NewSegmentTokenManager(valkeyClient)

	// ── API handlers ──────────────────────────────────────────────────────────
	authSvc := &authService{
		db:     gen.New(rwPool),
		tokens: tokenMaker,
		logger: logger,
	}

	authMiddleware := middleware.NewAuthenticator(tokenMaker)

	libHandler := v1.NewLibraryHandler(libSvc, logger).
		WithMedia(mediaSvc).
		WithDetector(libEnqueuer.introDetector)
	webhookSvc := newWebhookService(gen.New(rwPool), encryptor, logger)
	webhookHandler := v1.NewWebhookHandler(webhookSvc, logger)
	auditLogger := audit.New(gen.New(rwPool), logger)

	authHandler := v1.NewAuthHandler(authSvc, logger).WithAudit(auditLogger)

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
		WithLibraryAccess(&userLibraryAccessAdapter{lib: libSvc, q: gen.New(roPool)})
	fsHandler := v1.NewFSHandler()
	settingsHandler := v1.NewSettingsHandler(settingsSvc, logger).WithAudit(auditLogger)
	settingsHandler.SetWorkerLister(sessionStore)
	auditHandler := v1.NewAuditHandler(gen.New(roPool), logger)
	streamTracker := streaming.NewValkeyTracker(valkeyClient)
	analyticsHandler := v1.NewAnalyticsHandler(gen.New(roPool), logger)
	hubHandler := v1.NewHubHandler(gen.New(roPool), logger).WithLibraryAccess(libSvc)
	searchHandler := v1.NewSearchHandler(gen.New(roPool), logger).WithLibraryAccess(libSvc)
	historyHandler := v1.NewHistoryHandler(gen.New(roPool), logger).WithLibraryAccess(libSvc)
	nativeSessionsHandler := v1.NewNativeSessionsHandler(sessionStore, streamTracker, gen.New(roPool), logger)
	// Derive a stable machine ID from the secret key so webhook payloads
	// identify this server consistently across restarts without a dedicated config field.
	machineID := uuid.NewSHA1(uuid.NameSpaceDNS, []byte(cfg.SecretKey)).String()

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
	).WithPluginNotifier(pluginDispatcher)
	libEnqueuer.webhookDispatcher = webhookDispatcher
	matchAdapter := &matchSearchAdapter{enricher: metaAgent}
	arrAdapter := &arrLibraryAdapter{libSvc: libSvc, scanner: libEnqueuer}
	arrHandler := v1.NewArrHandler(settingsSvc, arrAdapter, logger)
	favoritesHandler := v1.NewFavoritesHandler(gen.New(rwPool), logger).WithLibraryAccess(libSvc)
	favoritesChecker := &favoritesChecker{q: gen.New(roPool)}
	nativeTranscodeHandler := v1.NewNativeTranscodeHandler(sessionStore, segTokenMgr, mediaSvc, cfg, logger).
		WithLibraryAccess(libSvc).
		WithAudit(auditLogger)

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
	// Lives next to the trickplay cache; on-disk *.vtt files keyed by file id.
	subtitleCacheRoot := cfg.CachePath
	if subtitleCacheRoot == "" {
		subtitleCacheRoot = filepath.Join(os.TempDir(), "onscreen-subtitles")
	} else {
		subtitleCacheRoot = filepath.Join(filepath.Dir(subtitleCacheRoot), "subtitles")
	}
	// Provider is dynamic: it re-reads settings on each call and rebuilds the
	// underlying client when credentials change, so users don't need to restart
	// the server after adding or updating an OpenSubtitles key.
	subtitleProvider := subtitles.NewDynamicProvider(func(ctx context.Context) subtitles.OpenSubtitlesCreds {
		cfg := settingsSvc.OpenSubtitles(ctx)
		return subtitles.OpenSubtitlesCreds{
			Enabled:  cfg.Enabled,
			APIKey:   cfg.APIKey,
			Username: cfg.Username,
			Password: cfg.Password,
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

	itemHandler := v1.NewItemHandler(mediaSvc, watchSvc, sessionStore, metaAgent, matchAdapter, webhookDispatcher, favoritesChecker, streamTracker, logger).
		WithLibraryAccess(libSvc).
		WithMarkers(intromarker.NewStore(rwPool)).
		WithExternalSubtitles(subtitleSvc).
		WithAudit(auditLogger)

	photosHandler := v1.NewPhotosHandler(mediaSvc, photoImageSrv, logger).
		WithLibraryAccess(libSvc)

	// ── Live TV ──────────────────────────────────────────────────────────────
	// Phase A: tuner abstraction + HDHomeRun and M3U drivers; channels list +
	// now/next display + HLS proxy. DVR scheduling lives in Phase B and slots
	// in here through the same service.
	liveTVRegistry := livetv.NewRegistry()
	liveTVRegistry.Register(livetv.TunerTypeHDHomeRun, livetv.HDHomeRunFactory)
	liveTVRegistry.Register(livetv.TunerTypeM3U, livetv.M3UFactory)
	liveTVSvc := livetv.NewService(newLiveTVAdapter(gen.New(rwPool)), liveTVRegistry, logger)
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
		Dir:          filepath.Join(cfg.CachePath, "livetv"),
		VideoEncoder: liveTVEncoder,
	}, liveTVSvc, logger)
	liveTVHandler = v1.NewLiveTVHandler(liveTVSvc, logger).WithStreamProxy(liveTVProxy)

	// DVR: matcher + recording worker. Recordings land in
	// {CachePath}/dvr by default — users should point a "dvr" library
	// at that path so the scanner surfaces finalized captures.
	dvrQueries := newDVRAdapter(gen.New(rwPool), newLiveTVAdapter(gen.New(rwPool)))
	dvrSvc := livetv.NewDVRService(dvrQueries, liveTVSvc,
		filepath.Join(cfg.CachePath, "dvr"), logger)
	dvrWorker := livetv.NewDVRWorker(livetv.DVRWorkerConfig{
		RecordDir: filepath.Join(cfg.CachePath, "dvr"),
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
		embeddedWorker = transcode.NewWorker(
			transcode.WorkerID(),
			"127.0.0.1:7073",
			sessionStore,
			encoders,
			cfg.TranscodeMaxSessions,
			transcode.EncoderOpts{
				NVENCPreset:  cfg.TranscodeNVENCPreset,
				NVENCTune:    cfg.TranscodeNVENCTune,
				NVENCRC:      cfg.TranscodeNVENCRC,
				MaxrateRatio: cfg.TranscodeMaxrateRatio,
			},
			logger,
		)

		// Wire embedded worker into transcode handler so Stop can kill FFmpeg immediately.
		nativeTranscodeHandler.SetSessionKiller(embeddedWorker)
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
	passwordResetHandler := v1.NewPasswordResetHandler(passwordResetDB, emailSender, baseURL, logger)
	inviteDB := &inviteAdapter{q: gen.New(rwPool)}
	inviteHandler := v1.NewInviteHandler(inviteDB, emailSender, baseURL, logger)

	// ── OIDC + LDAP (settings-driven, always wired) ───────────────────────────
	// Both pull config from server_settings on each request, so admins enable
	// and reconfigure them through the UI without a restart.
	oidcSvc := v1.NewOIDCAuthService(gen.New(rwPool), authSvc.issueTokenPair, logger)
	oidcAuthHandler := v1.NewOIDCHandler(settingsSvc, oidcSvc, baseURL, logger)
	ldapSvc := v1.NewLDAPAuthService(settingsSvc, v1.DefaultLDAPDialer{}, gen.New(rwPool), authSvc.issueTokenPair, logger)
	ldapAuthHandler := v1.NewLDAPHandler(settingsSvc, ldapSvc, logger)

	// ── Notifications ────────────────────────────────────────────────────────
	_ = notifServiceEarly // used by scanEnqueuer above
	notifHandler := v1.NewNotificationHandler(gen.New(roPool), notifBrokerEarly, logger)

	// ── Maintenance (admin one-shot operations) ──────────────────────────────
	maintenanceHandler := v1.NewMaintenanceHandler(mediaSvc, metaAgent, logger)
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
	requestsSvc := requests.NewService(gen.New(rwPool), requestsTMDB, notifServiceEarly, logger)
	requestsHandler := v1.NewRequestHandler(requestsSvc, logger).WithAudit(auditLogger)
	arrServicesHandler := v1.NewArrServicesHandler(gen.New(rwPool), logger).WithAudit(auditLogger)
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
	sched := scheduler.New(scheduler.NewPgxQuerier(rwPool), schedRegistry, logger)
	tasksHandler := v1.NewTasksHandler(gen.New(rwPool), schedRegistry, logger)

	// ── People (cast/crew) — lazy TMDB fetch on first item-detail view ───────
	peopleQ := &peopleAdapter{q: gen.New(rwPool)}
	peopleAgentFn := func() people.Agent {
		a := agentFn()
		if a == nil {
			return nil
		}
		// *tmdb.Client implements both metadata.Agent and people.Agent.
		if pa, ok := a.(people.Agent); ok {
			return pa
		}
		return nil
	}
	peopleSvc := people.New(peopleQ, peopleAgentFn)
	peopleHandler := v1.NewPeopleHandler(peopleSvc, &peopleItemLookup{
		svc:      mediaSvc,
		agentFn:  agentFn,
		enricher: metaAgent,
		logger:   logger,
	}, logger)

	// ── Router ────────────────────────────────────────────────────────────────
	h := &api.Handlers{
		Library:            libHandler,
		Webhook:            webhookHandler,
		Auth:               authHandler,
		User:               userHandler,
		FS:                 fsHandler,
		Settings:           settingsHandler,
		Analytics:          analyticsHandler,
		NativeSessions:     nativeSessionsHandler,
		Hub:                hubHandler,
		Search:             searchHandler,
		History:            historyHandler,
		Items:              itemHandler,
		Photos:             photosHandler,
		Trickplay:          trickplayHandler,
		Subtitles:          subtitleHandler,
		NativeTranscode:    nativeTranscodeHandler,
		Collections:        v1.NewCollectionHandler(gen.New(rwPool), logger).WithLibraryAccess(libSvc),
		Playlists:          v1.NewPlaylistHandler(gen.New(rwPool), logger).WithLibraryAccess(libSvc),
		PhotoAlbums:        v1.NewPhotoAlbumHandler(gen.New(rwPool), logger).WithLibraryAccess(libSvc),
		LiveTV:             liveTVHandler,
		Lyrics:             lyricsHandler,
		Arr:                arrHandler,
		OIDCAuth:           oidcAuthHandler,
		LDAPAuth:           ldapAuthHandler,
		Audit:              auditHandler,
		Email:              emailHandler,
		PasswordReset:      passwordResetHandler,
		Invite:             inviteHandler,
		Notifications:      notifHandler,
		Maintenance:        maintenanceHandler,
		Backup:             backupHandler,
		Tasks:              tasksHandler,
		People:             peopleHandler,
		Plugins:            pluginHandler,
		Pair:               pairHandler,
		Capabilities:       capabilitiesHandler,
		ArrServices:        arrServicesHandler,
		Requests:           requestsHandler,
		Discover:           discoverHandler,
		Favorites:          favoritesHandler,
		StreamTracker:      streamTracker,
		Artwork:            artworkMgr,
		ArtworkRoots:       scanPathsFn,
		Logger:             logger,
		Metrics:            metrics,
		Auth_mw:            authMiddleware,
		RateLimiter:        rateLimiter,
		CORSAllowedOrigins: corsAllowedOrigins,
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

	// ── Metrics server (separate port, ADR) ──────────────────────────────────
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", observability.MetricsHandler(promReg))
	metricsMux.HandleFunc("/health/live", liveH)

	// ── Background workers ────────────────────────────────────────────────────
	partitionWorker := worker.NewPartitionWorker(rwPool, cfg.RetainMonths, logger)
	hubRefreshWorker := worker.NewHubRefreshWorker(rwPool, 5*time.Minute, logger)
	periodicScanWorker := newPeriodicScanWorker(libSvc, libEnqueuer, logger)
	// masterLock ensures only one instance runs singleton workers (hub refresh,
	// partition maintenance, periodic scans). Any instance can take over if the
	// current master crashes — the lock TTL (15 s) bounds the failover window.
	masterLock := worker.NewMasterLock(valkeyClient, uuid.New().String(), logger)

	// ── Servers ───────────────────────────────────────────────────────────────
	apiServer := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mainMux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
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
		logger.Info("api server listening", "addr", cfg.ListenAddr)
		if err := apiServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("api server: %w", err)
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
		return nil
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("server stopped")
	return nil
}

// scanEnqueuer implements library.ScanEnqueuer and scanner.WatchTrigger.
// It manages per-library filesystem watchers and drives the real scanner.
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
		// Intro/credits detection runs only on show libraries, and only when
		// the admin has left detection on auto. Movies are excluded — users
		// typically mark them manually if at all.
		if lib.Type == "show" && e.introDetector != nil && e.settingsSvc != nil {
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
func (e *scanEnqueuer) runIntroDetection(libraryID uuid.UUID) {
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
	e.logger.Info("fs change detected, scanning directory",
		"library_id", libraryID, "dir", dirPath)
	go func() {
		scanCtx := context.WithoutCancel(e.serverCtx)
		result, err := e.scanner.ScanLibrary(scanCtx, libraryID, lib.Type, []string{dirPath})
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
