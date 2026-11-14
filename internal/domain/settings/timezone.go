package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// ValidateTimeZone checks a System ▸ Time zone value: an IANA zone name that
// this build can load ("UTC", "America/Chicago"). "Local" is refused — it
// names whatever zone each process happens to run in, which is the default
// the setting exists to override, so it is stored as unset instead.
func ValidateTimeZone(name string) error {
	if name == "" || name == "Local" {
		return fmt.Errorf("%w: time zone %q", ErrInvalidSetting, name)
	}
	if _, err := time.LoadLocation(name); err != nil {
		return fmt.Errorf("%w: time zone %q: %w", ErrInvalidSetting, name, err)
	}
	return nil
}

// ProcessTimeZoneName names the zone the server falls back to with no time
// zone set: the TZ environment variable when it names a loadable zone, else
// the host's local zone (the /etc/localtime link target), else "" when the
// zone has no name to show ("Local" read from a copied zoneinfo file).
func ProcessTimeZoneName() string {
	if tz := strings.TrimPrefix(os.Getenv("TZ"), ":"); tz != "" {
		if _, err := time.LoadLocation(tz); err == nil {
			return tz
		}
	}
	if name := time.Local.String(); name != "Local" {
		return name // "UTC" when the host has no zone configured, as in the images
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		// …/zoneinfo/Europe/Berlin, however the distro lays the path out.
		target = filepath.ToSlash(target)
		if i := strings.Index(target, "zoneinfo/"); i >= 0 {
			if name := target[i+len("zoneinfo/"):]; ValidateTimeZone(name) == nil {
				return name
			}
		}
	}
	return ""
}

// TimeZone reads the stored household zone name ("" = unset). Unlike System
// it reports a failed read rather than answering "unset", so ZoneSource can
// keep the zone it has through a database blip instead of jumping to the
// process zone.
func (s *Service) TimeZone(ctx context.Context) (string, error) {
	var raw string
	err := s.db.QueryRow(ctx, `SELECT value FROM server_settings WHERE key = $1`, keySystemConfig).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("settings: read time zone: %w", err)
	}
	var cfg SystemConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return "", fmt.Errorf("settings: parse system_config: %w", err)
	}
	if cfg.TimeZone == nil {
		return "", nil
	}
	return *cfg.TimeZone, nil
}

// zoneCacheTTL is how long ZoneSource reuses a read of the setting, so a
// change reaches every instance within it without a restart.
const zoneCacheTTL = 30 * time.Second

// TimeZoneReader is what ZoneSource reads the setting through (*Service).
type TimeZoneReader interface {
	TimeZone(ctx context.Context) (string, error)
}

// ZoneSource serves the household time zone (SystemConfig.TimeZone) to the
// paths that evaluate in it — the parental gates, which run on every playback
// request — rereading the setting at most once per zoneCacheTTL.
type ZoneSource struct {
	src      TimeZoneReader
	fallback *time.Location
	now      func() time.Time // nil = time.Now (tests)

	mu  sync.Mutex
	loc *time.Location
	at  time.Time
}

// NewZoneSource returns a ZoneSource over src. With no zone set, or a stored
// name this build can't load, the zone is the process's (TZ env, else the
// host's), which is what every evaluation used before the setting existed.
func NewZoneSource(src TimeZoneReader) *ZoneSource {
	return &ZoneSource{src: src, fallback: time.Local}
}

// Location is the household zone.
func (z *ZoneSource) Location(ctx context.Context) *time.Location {
	now := time.Now()
	if z.now != nil {
		now = z.now()
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if z.loc != nil && now.Sub(z.at) < zoneCacheTTL {
		return z.loc
	}
	name, err := z.src.TimeZone(ctx)
	switch {
	case err != nil && z.loc != nil:
		// Keep the zone we have; the clock must not move on a read failure.
	case err != nil || name == "":
		z.loc = z.fallback
	default:
		if l, lerr := time.LoadLocation(name); lerr == nil {
			z.loc = l
		} else {
			z.loc = z.fallback
		}
	}
	z.at = now
	return z.loc
}
