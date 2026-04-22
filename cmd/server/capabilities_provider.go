package main

import (
	"context"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// capabilitiesProvider builds a CapabilitiesResponse on demand. We rebuild
// per-request rather than caching so admins see toggles (OIDC enable, OS
// configuration) reflected immediately.
type capabilitiesProvider struct {
	cfg       *config.Config
	version   string
	machineID string
	settings  *settings.Service
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
			Transcode:         true,
			Trickplay:         true,
			SubtitlesExternal: osCfg.APIKey != "",
			SubtitlesOCR:      true,
			OIDC:              oidcCfg.Enabled && oidcCfg.IssuerURL != "" && oidcCfg.ClientID != "",
			LDAP:              ldapCfg.Enabled && ldapCfg.Host != "",
			DevicePairing:     true,
			Plugins:           true,
			Backup:            true,
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
		},
		Limits: v1.CapabilitiesLimits{
			MaxUploadBytes:          1 << 20, // matches MaxBytesBody middleware
			MaxTranscodeBitrateKbps: p.cfg.TranscodeMaxBitrate,
			MaxTranscodeWidth:       p.cfg.TranscodeMaxWidth,
			MaxTranscodeHeight:      p.cfg.TranscodeMaxHeight,
		},
	}
	if p.cfg.DiscoveryEnabled {
		resp.Discovery.UDPPort = p.cfg.DiscoveryPort
	}
	return resp
}
