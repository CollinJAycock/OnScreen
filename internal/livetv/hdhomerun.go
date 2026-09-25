package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/onscreen/onscreen/internal/safehttp"
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
	// stream is the timeout-free client used for tune sessions, built once.
	// OpenStream used to construct one per call, and an http.Client owns an
	// http.Transport with its own connection pool that nothing here ever
	// closes — so every channel change leaked a pool holding idle sockets for
	// its 90s IdleConnTimeout. Channel surfing on a TV remote is a burst of
	// tunes, which is exactly the pattern that piled them up.
	stream *http.Client

	// Per-channel stream URLs, populated by Discover from each lineup
	// entry's URL field. Constructing /auto/v{number} works on most
	// firmwares but some variants (CableCARD-era HDHR PRIME, certain
	// HDHR5 builds) only honor the lineup's pre-computed URL. Storing
	// what the device tells us is cheaper than guessing.
	mu         sync.RWMutex
	streamURLs map[string]string // GuideNumber -> stream URL
}

// NewHDHomeRunDriver constructs a driver from a stored device row's config.
// It does not probe the device — that happens lazily on the first Discover
// or Probe call so a single dead box doesn't block server startup.
func NewHDHomeRunDriver(name string, cfg HDHomeRunConfig) *HDHomeRunDriver {
	// HTTP timeout has to be longer than the streaming endpoint's first
	// byte (some HDHRs take ~10s to lock onto a channel) but the streaming
	// endpoint uses a separate stream-aware request, so this is fine for
	// the small JSON endpoints. LocalDevice policy allows RFC1918 —
	// HDHomeRuns live on the LAN.
	jsonClient := safehttp.LocalDevice()
	// No timeout: the body IS the tune session, so a Client.Timeout would
	// cut the stream off mid-watch. Cancellation comes from the request
	// context instead.
	streamClient := safehttp.NewClient(safehttp.DialPolicy{
		AllowPrivate: true, AllowLoopback: true, AllowLinkLocal: true,
	}, 0)
	// Both clients may dial loopback + link-local (tuner auto-config), so a
	// redirect is as dangerous as a lineup URL: a spoofed tuner answering
	// /auto/v5.1 or /lineup.json with a 302 to http://169.254.169.254/ or a
	// 127.0.0.1 admin port would make the server fetch it — and for a tune,
	// stream the body back to the viewer. Only follow redirects that stay on
	// the tuner's own host, the same rule sameHostname applies to lineup URLs.
	jsonClient.CheckRedirect = hdhomerunRedirectPolicy(cfg.HostURL)
	streamClient.CheckRedirect = hdhomerunRedirectPolicy(cfg.HostURL)
	return &HDHomeRunDriver{
		name:       name,
		cfg:        cfg,
		http:       jsonClient,
		stream:     streamClient,
		streamURLs: make(map[string]string),
	}
}

// errHDHomeRunRedirect is returned (wrapped in the client's *url.Error) when a
// tuner response redirects off the tuner's own host or chains too many hops.
var errHDHomeRunRedirect = errors.New("hdhomerun: refusing redirect")

// maxHDHomeRunRedirects caps redirect hops from a tuner. Real devices don't
// redirect at all; a small allowance keeps a same-host path rewrite working.
const maxHDHomeRunRedirects = 3

// hdhomerunRedirectPolicy returns an http.Client CheckRedirect that allows a
// redirect only when its target hostname equals hostURL's (port may differ, as
// with sameHostname) and the chain is short. Every hop is re-validated, since
// CheckRedirect runs before each follow.
func hdhomerunRedirectPolicy(hostURL string) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) > maxHDHomeRunRedirects {
			return fmt.Errorf("%w: more than %d hops", errHDHomeRunRedirect, maxHDHomeRunRedirects)
		}
		if !sameHostname(req.URL.String(), hostURL) {
			return fmt.Errorf("%w: target host %q is not the tuner", errHDHomeRunRedirect, req.URL.Host)
		}
		return nil
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
	streams := make(map[string]string, len(lineup))
	for _, e := range lineup {
		if e.DRM == 1 {
			// Encrypted channels (CableCARD) — we can't stream them, hide them.
			continue
		}
		out = append(out, DiscoveredChannel{
			Number: e.GuideNumber,
			Name:   e.GuideName,
		})
		// Use the lineup's pre-computed URL when present (canonical) and
		// fall back to /auto/v{number} for older firmwares that didn't
		// include URL in the lineup response.
		//
		// The lineup comes from whatever answered at HostURL, and this client
		// is allowed to reach loopback + link-local (for tuner auto-config). A
		// spoofed or compromised "tuner" could otherwise hand back channel URLs
		// pointing at 127.0.0.1 services or 169.254.169.254, and every user who
		// tuned that channel would make the server fetch it. Only trust a lineup
		// URL whose host is the tuner's own host (the port may differ — real
		// devices stream on :5004).
		if e.URL != "" && sameHostname(e.URL, d.cfg.HostURL) {
			streams[e.GuideNumber] = e.URL
		} else {
			streams[e.GuideNumber] = fmt.Sprintf("%s/auto/v%s", strings.TrimRight(d.cfg.HostURL, "/"), url.PathEscape(e.GuideNumber))
		}
	}
	d.mu.Lock()
	d.streamURLs = streams
	d.mu.Unlock()
	return out, nil
}

// OpenStream issues a streaming HTTP GET against the HDHomeRun's stream
// port. The body is raw MPEG-TS at the channel's native bitrate. Closing
// the body releases the tune slot — this is the contract HDHomeRun
// documents and what the device firmware actually does.
//
// Looks up the channel's stream URL from the in-memory map populated by
// Discover. If the map is empty (server restarted without re-running
// Discover) or the channel isn't in it, runs Discover lazily before
// giving up — without this, restoring channel rows from the DB without a
// rescan would fail every tune.
//
// HDHomeRun returns 503 when all tuners are in use; we map that to
// ErrAllTunersBusy so the HLS proxy can render the right UX. Any other
// non-2xx is wrapped with the body for debugging.
func (d *HDHomeRunDriver) OpenStream(ctx context.Context, channelNumber string) (Stream, error) {
	streamURL := d.lookupStreamURL(channelNumber)
	if streamURL == "" {
		// Lazy discover so a fresh server start can tune without an
		// explicit rescan. Discover repopulates the map and updates
		// tune_count as a side effect.
		if _, err := d.Discover(ctx); err != nil {
			return nil, fmt.Errorf("hdhomerun lazy discover: %w", err)
		}
		streamURL = d.lookupStreamURL(channelNumber)
		if streamURL == "" {
			return nil, ErrChannelNotFound
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, err
	}
	// d.stream is timeout-free by construction — the body is the entire tune
	// session, and the request context is what closes it.
	resp, err := d.stream.Do(req)
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

// sameHostname reports whether candidate is an http(s) URL whose hostname equals
// base's hostname (ports may differ). Used to refuse lineup URLs that point
// anywhere other than the tuner itself.
func sameHostname(candidate, base string) bool {
	c, err := url.Parse(candidate)
	if err != nil || (c.Scheme != "http" && c.Scheme != "https") || c.Hostname() == "" {
		return false
	}
	b, err := url.Parse(base)
	if err != nil || b.Hostname() == "" {
		return false
	}
	return strings.EqualFold(c.Hostname(), b.Hostname())
}

// lookupStreamURL returns the cached per-channel stream URL or "" when
// not present. Callers fall back to lazy Discover before giving up.
func (d *HDHomeRunDriver) lookupStreamURL(channelNumber string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.streamURLs[channelNumber]
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
	// Bound the response: discover.json / lineup.json come from whatever
	// answers at the tuner address (a spoofed LAN device included), and an
	// unbounded body was decoded straight into memory. Real lineups are a few
	// hundred KB even for large channel maps.
	return json.NewDecoder(io.LimitReader(resp.Body, maxHDHomeRunJSONBytes)).Decode(out)
}

// maxHDHomeRunJSONBytes caps a tuner's discover/lineup JSON response.
const maxHDHomeRunJSONBytes = 8 << 20
