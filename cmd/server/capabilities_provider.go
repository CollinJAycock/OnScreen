package main

import (
	"context"
	"os/exec"
	"sync/atomic"
	"time"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/dbtools"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/observability"
)

// capabilitiesProvider builds a CapabilitiesResponse on demand. We rebuild
// per-request rather than caching so admins see toggles (OIDC enable, OS
// configuration) reflected immediately.
type capabilitiesProvider struct {
	cfg       *config.Config
	version   string
	machineID string
	settings  *settings.Service

	// Runtime-detected fields populated at wiring time. Taken as
	// snapshots so Capabilities() stays O(1); values that can change
	// dynamically (DVR enabled after live TV is disabled) require a
	// server restart, which is consistent with how settings-layer
	// toggles already behave.
	liveTVAvailable         bool
	liveTVTuneCount         int
	activeEncoders          []string
	maxConcurrentTranscodes int

	// Tool-dependency probes. The four features below shell out to
	// external binaries that may or may not be on PATH depending on
	// the operator's Docker image / host setup. Probed once at boot
	// in setRuntimeDetected — PATH doesn't change at runtime, so a
	// snapshot is correct. Capability flags reflect what actually
	// works, not what the codebase intends to support, so a client
	// reading `features.trickplay = false` knows to hide the
	// scrub-preview UI instead of getting 5xx mid-render.
	hasFFmpeg    bool // trickplay generation, transcode, OCR pre-roll
	hasTesseract bool // OCR subtitle path
	hasFPCalc    bool // intro-marker AcoustID detector
	hasPGDump    bool // backup endpoint + scheduled backup task

	// schemaVersion is the highest applied migration, as last read by the
	// boot migration check, followSchemaVersion or a /health/ready probe
	// (setSchemaVersion). Atomic because those store it while
	// Capabilities() reads it. 0 until the first successful read, which
	// keeps the schema-gated flags off rather than guessing.
	schemaVersion atomic.Int64
}

// progressKeepsDurationVersion is migration 00035_watch_progress_keep_duration:
// from it on, a watch event without a duration keeps the stored one. The
// rollup before it clears the stored duration on such an event, so
// features.progress_without_duration stays false until it is applied —
// AUTO_MIGRATE=false and a binary started before `goose up` would otherwise
// invite duration-less reports into the very wipe the flag exists to avoid.
const progressKeepsDurationVersion = 35

// setSchemaVersion records the applied schema version. main.go feeds it the
// boot migration check and, through its migrationStatusFn, every readiness
// probe and followSchemaVersion tick, so a `goose up` against a running
// server lifts the schema-gated flags within a minute without a restart,
// and a capabilities request never pays for a query of its own.
func (p *capabilitiesProvider) setSchemaVersion(applied int64) {
	p.schemaVersion.Store(applied)
}

// schemaVersionRefreshInterval is how often followSchemaVersion re-reads the
// applied schema version.
const schemaVersionRefreshInterval = time.Minute

// followSchemaVersion runs status — main.go's migrationStatusFn, which hands
// the applied version to setSchemaVersion — every interval until ctx is done.
// The readiness probe runs the same check, but only when something polls
// /health/ready, and the shipped compose healthchecks and the documented
// load-balancer probe poll /health/live instead; without this tick a
// `goose up` against a running server would leave the schema-gated flags
// off until a restart. One MAX over goose_db_version a minute.
func followSchemaVersion(ctx context.Context, interval time.Duration, status observability.MigrationStatusFn) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status()
		}
	}
}

// progressWithoutDuration is features.progress_without_duration; see
// progressKeepsDurationVersion.
func (p *capabilitiesProvider) progressWithoutDuration() bool {
	return p.schemaVersion.Load() >= progressKeepsDurationVersion
}

// setRuntimeDetected populates the runtime-sensed fields. Called from
// main.go after encoder detection and Live TV wiring complete. Safe
// because the HTTP server hasn't started listening yet — no concurrent
// Capabilities() readers are possible.
func (p *capabilitiesProvider) setRuntimeDetected(
	liveTV bool, tuneCount int,
	encoders []string, maxTranscodes int,
) {
	p.liveTVAvailable = liveTV
	p.liveTVTuneCount = tuneCount
	p.activeEncoders = encoders
	p.maxConcurrentTranscodes = maxTranscodes
	// Tool-dep probes — see capabilitiesProvider field comments.
	// LookPath errors are equivalent to "not on PATH"; we don't
	// distinguish reasons (missing vs. unreadable) because the
	// remediation is the same: install the tool.
	p.hasFFmpeg = lookPathOK("ffmpeg")
	p.hasTesseract = lookPathOK("tesseract")
	p.hasFPCalc = lookPathOK("fpcalc")
	// pg_dump may be bundled next to the server binary (Windows installer
	// stages it under pgsql/bin); dbtools.Available checks that path before
	// falling back to PATH, matching what the backup handler actually uses.
	p.hasPGDump = dbtools.Available("pg_dump")
}

// lookPathOK is a thin LookPath wrapper that swallows the *exec.Error
// detail — callers only care about presence/absence. Kept package-
// local because no other startup code probes binaries this way.
func lookPathOK(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// Capabilities returns the current snapshot. Background context is fine —
// the settings reads are cached in-memory and don't take long enough to
// warrant plumbing the request context all the way through.
func (p *capabilitiesProvider) Capabilities() v1.CapabilitiesResponse {
	ctx := context.Background()

	oidcCfg := p.settings.OIDC(ctx)
	ldapCfg := p.settings.LDAP(ctx)
	osCfg := p.settings.OpenSubtitles(ctx)

	resp := v1.CapabilitiesResponse{
		Server: v1.CapabilitiesServer{
			Name:       p.cfg.ServerName,
			MachineID:  p.machineID,
			Version:    p.version,
			APIVersion: "v1",
		},
		Features: v1.CapabilitiesFeatures{
			// Transcode rides on ffmpeg. If it's missing, transcode
			// requests will 5xx, so the flag should agree with the
			// runtime probe even though "no ffmpeg = barely-functional
			// server" is itself an unusual deploy.
			Transcode: p.hasFFmpeg,
			// ABR needs ffmpeg AND the operator opt-in (multi-rendition
			// fan-out costs more encode capacity than single-rendition).
			ABRLadder:         p.hasFFmpeg && p.cfg.TranscodeABR,
			Trickplay:         p.hasFFmpeg,
			SubtitlesExternal: osCfg.APIKey != "",
			SubtitlesOCR:      p.hasFFmpeg && p.hasTesseract,
			OIDC:              oidcCfg.Enabled && oidcCfg.IssuerURL != "" && oidcCfg.ClientID != "",
			LDAP:              ldapCfg.Enabled && ldapCfg.Host != "",
			DevicePairing:     true,
			Plugins:           true,
			Backup:            p.hasPGDump,
			PeopleCredits:     p.cfg.TMDBAPIKey != "",
			Photos:            true,
			Music:             true,
			Webhooks:          true,
			Notifications:     true,
			// Requests gates on TMDB only — Discover and the metadata
			// snapshot at create time both need it. Admins still need to
			// configure at least one arr_service before approvals can
			// dispatch downstream, but the user-facing surface is live
			// as soon as TMDB is wired.
			Requests: p.cfg.TMDBAPIKey != "",
			// Always-on features that became first-class post-Phase-A.
			LiveTV: p.liveTVAvailable,
			DVR:    p.liveTVAvailable, // share one flag; DVR rides Live TV
			Lyrics: true,
			// IntroMarkers needs both fpcalc (fingerprint) and ffmpeg
			// (audio decode pre-roll) — either missing means the
			// detector silently no-ops, so reflect honestly.
			IntroMarkers: p.hasFPCalc && p.hasFFmpeg,
			Chapters:     true,
			// Built-in local-account 2FA — no external tool to probe.
			TOTP:         true,
			WebDownloads: p.settings.WebDownloadsEnabled(ctx),
			// Duration-less progress reports keep the stored duration
			// (Progress handler + migration 00035) — once that migration
			// is applied.
			ProgressWithoutDuration: p.progressWithoutDuration(),
		},
		Codecs: v1.CapabilitiesCodecs{
			Video:      []string{"h264", "hevc"},
			Audio:      []string{"aac", "ac3", "eac3", "mp3", "opus", "flac"},
			Containers: []string{"mp4", "mkv", "ts", "webm"},
			Hardware:   p.activeEncoders,
			HDRToneMap: true,
		},
		Limits: v1.CapabilitiesLimits{
			MaxUploadBytes:          1 << 20, // matches MaxBytesBody middleware
			MaxTranscodeBitrateKbps: p.cfg.TranscodeMaxBitrate,
			MaxTranscodeWidth:       p.cfg.TranscodeMaxWidth,
			MaxTranscodeHeight:      p.cfg.TranscodeMaxHeight,
			MaxConcurrentTranscodes: p.maxConcurrentTranscodes,
			LiveTVTuneCount:         p.liveTVTuneCount,
		},
	}
	if p.cfg.DiscoveryEnabled {
		resp.Discovery.UDPPort = p.cfg.DiscoveryPort
	}
	return resp
}
