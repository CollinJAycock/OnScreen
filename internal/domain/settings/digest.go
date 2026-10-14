package settings

import (
	"context"
	"errors"
	"fmt"
)

const keyDigestConfig = "digest_config"

// DigestConfig is the server-wide digest email setting (Settings ▸ Email).
// Users opt in themselves on their Account page; nothing is sent unless the
// admin has also turned the digest on here and SMTP is configured.
//
// Weekday and Hour are in the server's local time zone, like the scheduled
// tasks' cron expressions. Weekly digests go out on Weekday at Hour; monthly
// digests on the 1st of the month at Hour. Links in the email use the general
// settings' Base URL.
type DigestConfig struct {
	Enabled bool `json:"enabled"`
	Weekday int  `json:"weekday"` // 0 = Sunday .. 6 = Saturday
	Hour    int  `json:"hour"`    // 0-23
	// IncludeContinueWatching adds the recipient's Continue Watching titles.
	IncludeContinueWatching bool `json:"include_continue_watching"`
	// IncludeStats adds the recipient's own numbers for the period.
	IncludeStats bool `json:"include_stats"`
}

// DefaultDigestConfig is the config before an admin saves one: off, Monday
// 8am, both optional sections included once it's turned on.
func DefaultDigestConfig() DigestConfig {
	return DigestConfig{Weekday: 1, Hour: 8, IncludeContinueWatching: true, IncludeStats: true}
}

// ErrInvalidDigestConfig is returned by DigestConfig.Validate.
var ErrInvalidDigestConfig = errors.New("settings: invalid digest config")

// Validate checks the day and hour ranges.
func (c DigestConfig) Validate() error {
	if c.Weekday < 0 || c.Weekday > 6 {
		return fmt.Errorf("%w: weekday must be 0 (Sunday) to 6 (Saturday)", ErrInvalidDigestConfig)
	}
	if c.Hour < 0 || c.Hour > 23 {
		return fmt.Errorf("%w: hour must be 0 to 23", ErrInvalidDigestConfig)
	}
	return nil
}

// Digest returns the stored digest config; DefaultDigestConfig when nothing
// is stored (and for any field a stored value leaves out).
func (s *Service) Digest(ctx context.Context) DigestConfig {
	cfg := DefaultDigestConfig()
	s.getJSON(ctx, keyDigestConfig, &cfg)
	if cfg.Validate() != nil {
		return DefaultDigestConfig()
	}
	return cfg
}

// SetDigest validates and persists the digest config.
func (s *Service) SetDigest(ctx context.Context, cfg DigestConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return s.setJSON(ctx, keyDigestConfig, cfg)
}
