package main

import (
	"context"
	"crypto/tls"
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
	if sys.DiscoveryEnabled != nil {
		cfg.DiscoveryEnabled = *sys.DiscoveryEnabled
		changed++
	}
	if sys.DiscoveryPort != nil && *sys.DiscoveryPort > 0 {
		cfg.DiscoveryPort = *sys.DiscoveryPort
		changed++
	}
	if changed > 0 {
		logger.Info("applied System settings over env config", "overrides", changed)
	}
}

// applyNodeSettings overrides node/site-specific config with this node's row in
// the node_settings table (Settings ▸ Nodes). Unlike applySystemSettings, these
// values are per-node — each node reads only its own row, so they're safe to
// edit without affecting the rest of a multi-site fleet. The per-node DB value
// wins over env; set IGNORE_NODE_DB_CONFIG=true on a node to skip this entirely
// (break-glass for a node locked out by a bad bind address). A nil field keeps
// the env value. Mutates cfg in place; call before any consumer reads it.
func applyNodeSettings(cfg *config.Config, ns settings.NodeSettings, logger *slog.Logger) {
	changed := 0
	if ns.ListenAddr != nil && *ns.ListenAddr != "" {
		cfg.ListenAddr = *ns.ListenAddr
		changed++
	}
	if ns.MetricsAddr != nil && *ns.MetricsAddr != "" {
		cfg.MetricsAddr = *ns.MetricsAddr
		changed++
	}
	if ns.WorkerHealthAddr != nil && *ns.WorkerHealthAddr != "" {
		cfg.WorkerHealthAddr = *ns.WorkerHealthAddr
		changed++
	}
	if ns.CachePath != nil && *ns.CachePath != "" {
		cfg.CachePath = *ns.CachePath
		changed++
	}
	if ns.StaticABRRoot != nil {
		cfg.StaticABRRoot = *ns.StaticABRRoot
		changed++
	}
	if ns.SiteID != nil {
		cfg.SiteID = *ns.SiteID
		changed++
	}
	if ns.TranscodeQSVDecode != nil {
		cfg.TranscodeQSVDecode = *ns.TranscodeQSVDecode
		changed++
	}
	if ns.DisableEmbeddedWorker != nil {
		cfg.DisableEmbeddedWorker = *ns.DisableEmbeddedWorker
		changed++
	}
	if changed > 0 {
		logger.Info("applied per-node settings over env config", "node_id", cfg.NodeID, "overrides", changed)
	}
}

// nodeEffective snapshots the per-node knobs from the merged config so the API
// can show running values as defaults where this node has no stored override.
func nodeEffective(cfg *config.Config) settings.NodeSettings {
	return settings.NodeSettings{
		ListenAddr:            strPtr(cfg.ListenAddr),
		MetricsAddr:           strPtr(cfg.MetricsAddr),
		WorkerHealthAddr:      strPtr(cfg.WorkerHealthAddr),
		CachePath:             strPtr(cfg.CachePath),
		StaticABRRoot:         strPtr(cfg.StaticABRRoot),
		SiteID:                strPtr(cfg.SiteID),
		TranscodeQSVDecode:    boolPtr(cfg.TranscodeQSVDecode),
		DisableEmbeddedWorker: boolPtr(cfg.DisableEmbeddedWorker),
	}
}

// applyTranscodeConfig overrides the env-derived transcoder knobs with any
// values set in Settings ▸ Transcode (stored in TranscodeConfig): output
// ceilings (0 = "unset, keep env default") and the ABR ladder (pointer fields,
// nil = "unset"). The server reads cfg.TranscodeMax* / TranscodeABR* directly
// when starting a session, so merging here (before any session) is all that's
// required. NVENC/maxrate tuning is consumed elsewhere and left untouched.
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
	if tc.ABR != nil {
		cfg.TranscodeABR = *tc.ABR
		changed++
	}
	if tc.ABRMaxHeight != nil {
		cfg.TranscodeABRMaxHeight = *tc.ABRMaxHeight
		changed++
	}
	if tc.ABRAutoMaxHeight != nil {
		cfg.TranscodeABRAutoMaxHeight = *tc.ABRAutoMaxHeight
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
		ServerName:              strPtr(cfg.ServerName),
		RetainMonths:            intPtr(cfg.RetainMonths),
		TMDBRateLimit:           intPtr(cfg.TMDBRateLimit),
		PublicAssetCache:        boolPtr(cfg.PublicAssetCache),
		StaticABREnabled:        boolPtr(cfg.StaticABREnabled),
		MissingFileGraceMinutes: intPtr(int(cfg.MissingFileGracePeriod / time.Minute)),
		ScanFileConcurrency:     intPtr(cfg.ScanFileConcurrency),
		ScanLibraryConcurrency:  intPtr(cfg.ScanLibraryConcurrency),
		DiscoveryEnabled:        boolPtr(cfg.DiscoveryEnabled),
		DiscoveryPort:           intPtr(cfg.DiscoveryPort),
	}
}

// transcodeEffective snapshots the transcoder knobs the UI fills as defaults
// (output ceilings + ABR ladder) from the merged config, so a blank stored row
// shows the running values rather than zeros / false.
func transcodeEffective(cfg *config.Config) settings.TranscodeConfig {
	return settings.TranscodeConfig{
		MaxBitrateKbps:   cfg.TranscodeMaxBitrate,
		MaxWidth:         cfg.TranscodeMaxWidth,
		MaxHeight:        cfg.TranscodeMaxHeight,
		ABR:              boolPtr(cfg.TranscodeABR),
		ABRMaxHeight:     intPtr(cfg.TranscodeABRMaxHeight),
		ABRAutoMaxHeight: intPtr(cfg.TranscodeABRAutoMaxHeight),
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func boolPtr(b bool) *bool    { return &b }

// loadDBTLSConfig builds an in-memory tls.Config from an admin-uploaded
// certificate (Settings ▸ Security) so the server can serve HTTPS without a
// cert file on disk. Returns nil — serve plain HTTP / use env files — when the
// env TLS file paths are set (they win), nothing is stored, or the stored pair
// fails to parse (logged, not fatal: a bad upload shouldn't take the server
// down, it falls back to HTTP).
func loadDBTLSConfig(ctx context.Context, svc *settings.Service, cfg *config.Config, logger *slog.Logger) *tls.Config {
	if cfg.TLSEnabled() {
		return nil // env TLS_CERT_FILE/TLS_KEY_FILE take precedence
	}
	tc := svc.TLS(ctx)
	if tc.CertPEM == "" || tc.KeyPEM == "" {
		return nil
	}
	cert, err := tls.X509KeyPair([]byte(tc.CertPEM), []byte(tc.KeyPEM))
	if err != nil {
		logger.Error("stored TLS certificate invalid — falling back to plain HTTP", "err", err)
		return nil
	}
	logger.Info("UI-managed TLS certificate loaded — serving HTTPS")
	return &tls.Config{Certificates: []tls.Certificate{cert}}
}
