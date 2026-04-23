package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// HDHomeRunConfig is the per-device JSON blob stored in
// `tuner_devices.config` for type='hdhomerun' rows.
//
// HostURL is the device's HTTP base ("http://10.0.0.50") — discovery fills
// it in automatically, but the user can override for fixed-IP setups.
// TuneCountOverride lets the operator force a value when the device's
// /discover.json lies (some HDHR PRIMEs return wrong counts).
type HDHomeRunConfig struct {
	HostURL           string `json:"host_url"`
	TuneCountOverride int    `json:"tune_count_override,omitempty"`
}

// hdhomerunDiscover is the response shape from `/discover.json`.
type hdhomerunDiscover struct {
	FriendlyName    string `json:"FriendlyName"`
	ModelNumber     string `json:"ModelNumber"`
	DeviceID        string `json:"DeviceID"`
	TunerCount      int    `json:"TunerCount"`
	BaseURL         string `json:"BaseURL"`
	LineupURL       string `json:"LineupURL"`
	FirmwareVersion string `json:"FirmwareVersion"`
}

// hdhomerunLineupEntry is one row in `/lineup.json`. HDHomeRun returns
// either a virtual or physical channel number per row depending on the
// scan; we always prefer GuideNumber since that's what users dial in.
type hdhomerunLineupEntry struct {
	GuideNumber string `json:"GuideNumber"` // "5.1"
	GuideName   string `json:"GuideName"`   // "WCBS-DT"
	URL         string `json:"URL"`         // direct stream URL
	HD          int    `json:"HD,omitempty"`
	Favorite    int    `json:"Favorite,omitempty"`
	DRM         int    `json:"DRM,omitempty"` // 1 = encrypted; we skip these
}

// HDHomeRunDriver talks to a single Silicondust HDHomeRun device over its
// HTTP API. SSDP discovery (finding new boxes on the LAN) is a separate
// concern handled by the discovery package — this driver assumes the box's
// base URL is already known.
type HDHomeRunDriver struct {
	name      string
	cfg       HDHomeRunConfig
	tuneCount int // resolved at construction time from /discover.json or override
	http      *http.Client
}

// NewHDHomeRunDriver constructs a driver from a stored device row's config.
// It does not probe the device — that happens lazily on the first Discover
// or Probe call so a single dead box doesn't block server startup.
func NewHDHomeRunDriver(name string, cfg HDHomeRunConfig) *HDHomeRunDriver {
	return &HDHomeRunDriver{
		name: name,
		cfg:  cfg,
		// HTTP timeout has to be longer than the streaming endpoint's first
		// byte (some HDHRs take ~10s to lock onto a channel) but the streaming
		// endpoint uses a separate stream-aware request, so this is fine for
		// the small JSON endpoints.
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

// HDHomeRunFactory plugs into the Registry. Parses the config blob and
// returns a Driver — used in cmd/server wiring.
func HDHomeRunFactory(name string, configJSON []byte) (Driver, error) {
	var cfg HDHomeRunConfig
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return nil, fmt.Errorf("hdhomerun config parse: %w", err)
		}
	}
	if cfg.HostURL == "" {
		return nil, errors.New("hdhomerun: host_url required")
	}
	return NewHDHomeRunDriver(name, cfg), nil
}

func (d *HDHomeRunDriver) Type() TunerType { return TunerTypeHDHomeRun }

func (d *HDHomeRunDriver) TuneCount() int {
	if d.cfg.TuneCountOverride > 0 {
		return d.cfg.TuneCountOverride
	}
	return d.tuneCount
}

// Discover hits /discover.json (to refresh tune count) and /lineup.json
// (the channel list). DRM=1 channels are filtered out at this layer
// because we can't stream them and don't want them showing in the UI.
func (d *HDHomeRunDriver) Discover(ctx context.Context) ([]DiscoveredChannel, error) {
	// /discover.json — refresh tune count and verify reachability.
	var disc hdhomerunDiscover
	if err := d.fetchJSON(ctx, "/discover.json", &disc); err != nil {
		return nil, fmt.Errorf("hdhomerun discover: %w", err)
	}
	if d.cfg.TuneCountOverride == 0 {
		d.tuneCount = disc.TunerCount
	}

	// /lineup.json — channel list. Some firmwares return an empty array
	// while a scan is in progress; that's not an error, just a "try again."
	var lineup []hdhomerunLineupEntry
	if err := d.fetchJSON(ctx, "/lineup.json", &lineup); err != nil {
		return nil, fmt.Errorf("hdhomerun lineup: %w", err)
	}

	out := make([]DiscoveredChannel, 0, len(lineup))
	for _, e := range lineup {
		if e.DRM == 1 {
			// Encrypted channels (CableCARD) — we can't stream them, hide them.
			continue
		}
		out = append(out, DiscoveredChannel{
			Number: e.GuideNumber,
			Name:   e.GuideName,
		})
	}
	return out, nil
}

// OpenStream issues a streaming HTTP GET against the HDHomeRun's stream
// port. The body is raw MPEG-TS at the channel's native bitrate. Closing
// the body releases the tune slot — this is the contract HDHomeRun
// documents and what the device firmware actually does.
//
// HDHomeRun returns 503 when all tuners are in use; we map that to
// ErrAllTunersBusy so the HLS proxy can render the right UX. Any other
// non-2xx is wrapped with the body for debugging.
func (d *HDHomeRunDriver) OpenStream(ctx context.Context, channelNumber string) (Stream, error) {
	streamURL := fmt.Sprintf("%s/auto/v%s", d.cfg.HostURL, channelNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, err
	}
	// IMPORTANT: do not set a timeout on the streaming client — the body is
	// the entire tune session. The upstream context cancellation is what
	// closes the stream.
	streamClient := &http.Client{}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hdhomerun open stream: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, nil
	case http.StatusServiceUnavailable:
		resp.Body.Close()
		return nil, ErrAllTunersBusy
	case http.StatusNotFound:
		resp.Body.Close()
		return nil, ErrChannelNotFound
	default:
		resp.Body.Close()
		return nil, fmt.Errorf("hdhomerun open stream: status %d", resp.StatusCode)
	}
}

// Probe is a cheap reachability check — /discover.json is small and
// non-stateful, so the health-check loop hits it every couple minutes.
func (d *HDHomeRunDriver) Probe(ctx context.Context) error {
	var disc hdhomerunDiscover
	return d.fetchJSON(ctx, "/discover.json", &disc)
}

func (d *HDHomeRunDriver) fetchJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.cfg.HostURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
