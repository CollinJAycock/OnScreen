// cmd/worker is the OnScreen transcode and maintenance worker entrypoint.
// It handles: partition management, missing file cleanup, metadata refresh,
// session cleanup, and (Phase 2) FFmpeg transcode jobs.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/db"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/transcode"
	"github.com/onscreen/onscreen/internal/valkey"
	"github.com/onscreen/onscreen/internal/worker"
)

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
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logLevel, err := cfg.LogLevelVar()
	if err != nil {
		return fmt.Errorf("log level: %w", err)
	}
	logger := observability.NewLogger(logLevel)
	slog.SetDefault(logger)

	logger.Info("starting onscreen worker", "version", version, "build_time", buildTime)

	hot := config.NewHotReloadable(cfg)
	config.WatchSIGHUP(logger, hot, cfg, logLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── Database ──────────────────────────────────────────────────────────────
	rwPool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db pool: %w", err)
	}
	defer rwPool.Close()

	// ── Valkey ────────────────────────────────────────────────────────────────
	valkeyClient, err := valkey.New(ctx, cfg.ValkeyURL)
	if err != nil {
		return fmt.Errorf("valkey: %w", err)
	}
	defer valkeyClient.Close()

	// ── Domain services ──────────────────────────────────────────────────────
	queries := gen.New(rwPool)
	rwMQ := &mediaAdapter{q: queries}
	mediaSvc := media.NewService(rwMQ, rwMQ, logger)
	sessionSvc := &sessionCleanupAdapter{q: queries}

	// ── Workers ───────────────────────────────────────────────────────────────
	partitionWorker := worker.NewPartitionWorker(rwPool, cfg.RetainMonths, logger)

	gracePeriodProvider := &hotGracePeriod{cfg: cfg}

	missingWorker := worker.NewMissingFilesWorker(
		mediaSvc,
		gracePeriodProvider,
		5*time.Minute,
		logger,
	)

	sessionWorker := worker.NewSessionCleanupWorker(
		sessionSvc,
		1*time.Hour,
		logger,
	)

	// ── Transcode worker (Phase 2) ─────────────────────────────────────────────
	workerAddr := os.Getenv("WORKER_ADDR")
	if workerAddr == "" {
		workerAddr = ":7073"
	}

	// Check fleet config for an encoder assignment matching this worker's address.
	settingsSvc := settings.New(rwPool, logger)
	fleetCfg := settingsSvc.WorkerFleet(ctx)
	encoderOverride := cfg.TranscodeEncoders
	maxSessions := cfg.TranscodeMaxSessions
	for _, slot := range fleetCfg.Workers {
		if slot.Addr == workerAddr {
			if slot.Encoder != "" {
				encoderOverride = slot.Encoder
				logger.Info("fleet config encoder override", "addr", workerAddr, "encoder", slot.Encoder)
			}
			if slot.MaxSessions > 0 {
				maxSessions = slot.MaxSessions
				logger.Info("fleet config max_sessions override", "addr", workerAddr, "max_sessions", slot.MaxSessions)
			}
			break
		}
	}

	encoders, err := transcode.DetectEncoders(ctx, encoderOverride)
	if err != nil {
		logger.Warn("encoder detection failed, defaulting to software", "err", err)
		encoders = []transcode.Encoder{transcode.EncoderSoftware}
	}
	logger.Info("encoders available", "encoders", transcode.EncoderNames(encoders))
	sessionStore := transcode.NewSessionStore(valkeyClient)
	transcodeWorker := transcode.NewWorker(
		transcode.WorkerID(),
		workerAddr,
		sessionStore,
		encoders,
		maxSessions,
		logger,
	)

	// ── Health server ─────────────────────────────────────────────────────────
	liveH, readyH := observability.HealthHandler(
		&db.PingablePool{Pool: rwPool},
		valkeyClient,
		logger,
	)
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/health/live", liveH)
	healthMux.HandleFunc("/health/ready", readyH)
	healthSrv := &http.Server{Addr: cfg.WorkerHealthAddr, Handler: healthMux}

	// ── Run all workers ───────────────────────────────────────────────────────
	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		logger.Info("worker health server listening", "addr", cfg.WorkerHealthAddr)
		if err := healthSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("health server: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		<-gCtx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return healthSrv.Shutdown(shutCtx)
	})
	g.Go(func() error {
		partitionWorker.Run(gCtx)
		return nil
	})
	g.Go(func() error {
		missingWorker.Run(gCtx)
		return nil
	})
	g.Go(func() error {
		sessionWorker.Run(gCtx)
		return nil
	})
	g.Go(func() error {
		return transcodeWorker.Start(gCtx)
	})

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("worker stopped")
	return nil
}

// hotGracePeriod adapts config to the GracePeriodProvider interface.
type hotGracePeriod struct {
	cfg *config.Config
}

func (h *hotGracePeriod) MissingFileGracePeriod() time.Duration {
	return h.cfg.MissingFileGracePeriod
}
