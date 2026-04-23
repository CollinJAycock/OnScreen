// Package livetv handles broadcast TV: tuner discovery, channel listing,
// EPG ingestion, live stream proxying, and (in Phase B) DVR scheduling.
//
// This file defines the abstract Tuner interface that backends implement.
// HDHomeRun and M3U are the Phase A backends; TVHeadend would slot in here
// later without touching callers.
package livetv

import (
	"context"
	"errors"
	"io"
	"time"
)

// TunerType identifies the backend that implements a tuner. Stored in the
// `tuner_devices.type` column and used by the registry to pick a Driver.
type TunerType string

const (
	TunerTypeHDHomeRun TunerType = "hdhomerun"
	TunerTypeM3U       TunerType = "m3u"
)

// DiscoveredChannel is one channel as a tuner reports it. The persistence
// layer maps these into rows in the `channels` table; identifying fields
// (Number) are upsert keys, descriptive fields (Name, Logo) get refreshed.
type DiscoveredChannel struct {
	Number   string // "5", "5.1", "ESPN" — opaque to the system
	Callsign string // optional, often blank for IPTV
	Name     string
	LogoURL  string // optional
}

// Stream is a live MPEG-TS byte stream from a tuner. Closing it must release
// the tune slot — the HLS proxy refcounts these so multiple viewers on the
// same channel share a single underlying Stream.
type Stream interface {
	io.ReadCloser
}

// Driver is the per-backend implementation. A Driver is constructed from a
// `tuner_devices` row; one Driver per row.
//
// Drivers must be safe for concurrent use — multiple OpenStream calls can
// arrive for different channels at once, and the backend has to enforce its
// own tune-count limit (HDHomeRun returns HTTP 503 when full).
type Driver interface {
	// Type identifies the backend, mirroring the row's `type` column.
	Type() TunerType

	// Discover refreshes the channel list. Called once at server start and on
	// demand from the settings UI. Idempotent — caller upserts the result.
	Discover(ctx context.Context) ([]DiscoveredChannel, error)

	// TuneCount returns the concurrent-tune ceiling reported by the device,
	// or an effectively-unlimited number for IPTV-style sources. Used by the
	// DVR conflict resolver in Phase B.
	TuneCount() int

	// OpenStream begins streaming the given channel. The returned Stream is
	// raw MPEG-TS; the HLS proxy is responsible for re-segmenting via ffmpeg.
	// Returns ErrAllTunersBusy when the backend's tune-count is exhausted so
	// the caller can return a structured 503 to the client.
	OpenStream(ctx context.Context, channelNumber string) (Stream, error)

	// Probe returns nil if the device is reachable. Drives the
	// `last_seen_at` column and the disabled-vs-down distinction in the UI.
	Probe(ctx context.Context) error
}

// ErrAllTunersBusy is returned by OpenStream when the device's concurrent
// tune ceiling is exhausted. The HLS proxy turns this into a 503 with a
// stable error code so the client can render "All tuners in use" UX.
var ErrAllTunersBusy = errors.New("livetv: all tuners busy")

// ErrChannelNotFound is returned when a tune is requested for a channel
// number the backend doesn't know about (e.g. user removed it from the
// HDHomeRun lineup since last discovery).
var ErrChannelNotFound = errors.New("livetv: channel not found")

// DriverFactory builds a Driver from a stored device row's config blob.
// Each backend registers one of these in the package-level Registry.
type DriverFactory func(name string, config []byte) (Driver, error)

// Registry maps a TunerType to its factory. Test code can inject fakes by
// calling Register with a stub factory before constructing a Service.
type Registry struct {
	factories map[TunerType]DriverFactory
}

// NewRegistry returns an empty registry. Production wiring calls Register
// for hdhomerun and m3u in cmd/server.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[TunerType]DriverFactory)}
}

// Register installs a factory for a tuner type. Overwrites silently if the
// type is already registered (tests rely on this).
func (r *Registry) Register(t TunerType, f DriverFactory) {
	r.factories[t] = f
}

// Build constructs a Driver from a device row. Returns an error if no
// factory is registered for the row's type — callers should treat this as
// "this device's backend isn't compiled in" and skip it gracefully rather
// than crash the live-TV subsystem.
func (r *Registry) Build(t TunerType, name string, config []byte) (Driver, error) {
	f, ok := r.factories[t]
	if !ok {
		return nil, errors.New("livetv: no driver registered for type " + string(t))
	}
	return f(name, config)
}

// healthCheckInterval is how often the background loop pings each enabled
// tuner. Aggressive enough to notice an unplugged HDHomeRun within a guide
// refresh, slack enough to not flood the LAN.
const healthCheckInterval = 2 * time.Minute
