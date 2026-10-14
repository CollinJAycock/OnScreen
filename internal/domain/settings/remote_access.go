package settings

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const keyRemoteAccess = "remote_access_config"

// RemoteAccessConfig is Settings ▸ Remote access: whether the server asks the
// home router to forward a port to it (UPnP IGD / PCP / NAT-PMP, see
// internal/portmap). Off by default — a mapping exposes the server to the
// whole internet, so it is only ever made when an admin turns this on.
type RemoteAccessConfig struct {
	// MapPort: "Map a port on my router automatically".
	MapPort bool `json:"map_port"`
	// ExternalPort is the public port to ask for; 0 = the same as the
	// server's listen port.
	ExternalPort int `json:"external_port"`
	// Protocol is the transport mapped. Only "TCP" — the API is HTTP(S).
	Protocol string `json:"protocol"`
}

// ErrInvalidRemoteAccessConfig is returned by RemoteAccessConfig.Validate.
var ErrInvalidRemoteAccessConfig = errors.New("settings: invalid remote access config")

// WithDefaults fills the protocol.
func (c RemoteAccessConfig) WithDefaults() RemoteAccessConfig {
	if c.Protocol == "" {
		c.Protocol = "TCP"
	}
	c.Protocol = strings.ToUpper(c.Protocol)
	return c
}

// Validate checks the port range and protocol. Call on a config that has
// been through WithDefaults.
func (c RemoteAccessConfig) Validate() error {
	if c.ExternalPort < 0 || c.ExternalPort > 65535 {
		return fmt.Errorf("%w: external_port must be 1-65535, or 0 for the listen port", ErrInvalidRemoteAccessConfig)
	}
	if c.Protocol != "TCP" {
		return fmt.Errorf("%w: protocol must be TCP", ErrInvalidRemoteAccessConfig)
	}
	return nil
}

// RemoteAccess returns the stored config; mapping off when nothing is
// stored.
func (s *Service) RemoteAccess(ctx context.Context) RemoteAccessConfig {
	var cfg RemoteAccessConfig
	s.getJSON(ctx, keyRemoteAccess, &cfg)
	return cfg.WithDefaults()
}

// SetRemoteAccess validates and persists the config.
func (s *Service) SetRemoteAccess(ctx context.Context, cfg RemoteAccessConfig) error {
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	return s.setJSON(ctx, keyRemoteAccess, cfg)
}
