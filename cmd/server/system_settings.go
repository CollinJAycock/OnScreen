package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// applySystemSettings overrides the env-derived config with any cluster-wide
// System settings configured in the admin UI. A nil field falls back to the env
// value already in cfg, so env-configured installs are unaffected and the UI
// takes precedence once an admin sets a value. Mutates cfg in place; call before
// any consumer reads it. Restart-required, matching the other startup-read
// settings.
func applySystemSettings(_ context.Context, cfg *config.Config, sys settings.SystemConfig, logger *slog.Logger) {
	changed := 0
	if sys.ServerName != nil {
		cfg.ServerName = *sys.ServerName
		changed++
	}
	if sys.RetainMonths != nil && *sys.RetainMonths > 0 {
		cfg.RetainMonths = *sys.RetainMonths
		changed++
	}
	if sys.TMDBRateLimit != nil && *sys.TMDBRateLimit > 0 {
		cfg.TMDBRateLimit = *sys.TMDBRateLimit
		changed++
	}
	if sys.TranscodeABR != nil {
		cfg.TranscodeABR = *sys.TranscodeABR
		changed++
	}
	if sys.TranscodeABRMaxHeight != nil {
		cfg.TranscodeABRMaxHeight = *sys.TranscodeABRMaxHeight
		changed++
	}
	if sys.TranscodeABRAutoMaxHeight != nil {
		cfg.TranscodeABRAutoMaxHeight = *sys.TranscodeABRAutoMaxHeight
		changed++
	}
	if sys.PublicAssetCache != nil {
		cfg.PublicAssetCache = *sys.PublicAssetCache
		changed++
	}
	if sys.StaticABREnabled != nil {
		cfg.StaticABREnabled = *sys.StaticABREnabled
		changed++
	}
	if sys.MissingFileGraceMinutes != nil && *sys.MissingFileGraceMinutes > 0 {
		cfg.MissingFileGracePeriod = time.Duration(*sys.MissingFileGraceMinutes) * time.Minute
		changed++
	}
	if sys.ScanFileConcurrency != nil && *sys.ScanFileConcurrency > 0 {
		cfg.ScanFileConcurrency = *sys.ScanFileConcurrency
		changed++
	}
	if sys.ScanLibraryConcurrency != nil && *sys.ScanLibraryConcurrency > 0 {
		cfg.ScanLibraryConcurrency = *sys.ScanLibraryConcurrency
		changed++
	}
	if changed > 0 {
		logger.Info("applied System settings over env config", "overrides", changed)
	}
}

// applyTranscodeConfig overrides the env-derived transcode output ceilings with
// any values set in Settings ▸ Transcode (stored in TranscodeConfig). 0 means
// "unset — keep the env/built-in default". The server reads cfg.TranscodeMax*
// directly when starting a session, so merging here (before any session) is all
// that's required. NVENC/maxrate tuning is consumed elsewhere and left untouched.
func applyTranscodeConfig(cfg *config.Config, tc settings.TranscodeConfig, logger *slog.Logger) {
	changed := 0
	if tc.MaxBitrateKbps > 0 {
		cfg.TranscodeMaxBitrate = tc.MaxBitrateKbps
		changed++
	}
	if tc.MaxWidth > 0 {
		cfg.TranscodeMaxWidth = tc.MaxWidth
		changed++
	}
	if tc.MaxHeight > 0 {
		cfg.TranscodeMaxHeight = tc.MaxHeight
		changed++
	}
	if changed > 0 {
		logger.Info("applied Transcode settings over env config", "overrides", changed)
	}
}

// systemEffective snapshots the cluster-wide System knobs from the (already
// merged) config, so the settings API can show the running values as defaults.
func systemEffective(cfg *config.Config) settings.SystemConfig {
	return settings.SystemConfig{
		ServerName:                strPtr(cfg.ServerName),
		RetainMonths:              intPtr(cfg.RetainMonths),
		TMDBRateLimit:             intPtr(cfg.TMDBRateLimit),
		TranscodeABR:              boolPtr(cfg.TranscodeABR),
		TranscodeABRMaxHeight:     intPtr(cfg.TranscodeABRMaxHeight),
		TranscodeABRAutoMaxHeight: intPtr(cfg.TranscodeABRAutoMaxHeight),
		PublicAssetCache:          boolPtr(cfg.PublicAssetCache),
		StaticABREnabled:          boolPtr(cfg.StaticABREnabled),
		MissingFileGraceMinutes:   intPtr(int(cfg.MissingFileGracePeriod / time.Minute)),
		ScanFileConcurrency:       intPtr(cfg.ScanFileConcurrency),
		ScanLibraryConcurrency:    intPtr(cfg.ScanLibraryConcurrency),
	}
}

// transcodeEffective snapshots the transcode output ceilings from the merged
// config so the settings API can show running values as defaults when the
// TranscodeConfig fields are unset (0).
func transcodeEffective(cfg *config.Config) settings.TranscodeConfig {
	return settings.TranscodeConfig{
		MaxBitrateKbps: cfg.TranscodeMaxBitrate,
		MaxWidth:       cfg.TranscodeMaxWidth,
		MaxHeight:      cfg.TranscodeMaxHeight,
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func boolPtr(b bool) *bool    { return &b }
