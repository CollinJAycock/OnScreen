package livetv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
)

// M3UConfig is the per-source JSON blob stored in `tuner_devices.config` for
// type='m3u' rows.
//
// Source is a URL ("http://provider/playlist.m3u") or a local file path
// ("/var/lib/onscreen/iptv.m3u"). UserAgent is sent on both the playlist
// fetch and the stream open — many IPTV providers gate on it.
//
// MaxConcurrentStreams is the soft tune ceiling reported back to the DVR
// scheduler. IPTV servers don't expose a real number, so this is operator-
// configured. Default is high enough that a small household won't hit it.
type M3UConfig struct {
	Source               string `json:"source"`
	UserAgent            string `json:"user_agent,omitempty"`
	MaxConcurrentStreams int    `json:"max_concurrent_streams,omitempty"`
}

// m3uEntry is a parsed playlist row — one channel + its stream URL.
type m3uEntry struct {
	Number   string // tvg-chno or sequence number
	Callsign string // tvg-id
	Name     string // EXTINF display name
	LogoURL  string // tvg-logo
	StreamURL string
}

// M3UDriver serves channels from an M3U/M3U8 playlist (the de-facto IPTV
// distribution format). The playlist is fetched on Discover, parsed, and
// stream URLs are stashed in memory so OpenStream is a direct HTTP GET to
// the upstream — no second playlist fetch per tune.
type M3UDriver struct {
	name string
	cfg  M3UConfig

	mu      sync.RWMutex
	streams map[string]string // channel number -> stream URL
}

// NewM3UDriver constructs a driver. Doesn't fetch the playlist — Discover
// does that lazily, same model as HDHomeRun, so a flaky upstream doesn't
// block server start.
func NewM3UDriver(name string, cfg M3UConfig) *M3UDriver {
	return &M3UDriver{name: name, cfg: cfg, streams: make(map[string]string)}
}

// M3UFactory plugs into the Registry.
func M3UFactory(name string, configJSON []byte) (Driver, error) {
	var cfg M3UConfig
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &cfg); err != nil {
			return nil, fmt.Errorf("m3u config parse: %w", err)
		}
	}
	if cfg.Source == "" {
		return nil, errors.New("m3u: source required")
	}
	return NewM3UDriver(name, cfg), nil
}

func (d *M3UDriver) Type() TunerType { return TunerTypeM3U }

// TuneCount reports the operator-configured ceiling. Defaults to 100 when
// unset because IPTV servers usually allow far more concurrent connections
// than the hardware tuners we model with HDHomeRun.
func (d *M3UDriver) TuneCount() int {
	if d.cfg.MaxConcurrentStreams > 0 {
		return d.cfg.MaxConcurrentStreams
	}
	return 100
}

// Discover fetches and parses the M3U playlist, populating the in-memory
// stream-URL map. Idempotent: re-running it just refreshes the map.
func (d *M3UDriver) Discover(ctx context.Context) ([]DiscoveredChannel, error) {
	body, err := d.fetchPlaylist(ctx)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	entries, err := parseM3U(body)
	if err != nil {
		return nil, fmt.Errorf("m3u parse: %w", err)
	}

	out := make([]DiscoveredChannel, 0, len(entries))
	streams := make(map[string]string, len(entries))
	for _, e := range entries {
		out = append(out, DiscoveredChannel{
			Number:   e.Number,
			Callsign: e.Callsign,
			Name:     e.Name,
			LogoURL:  e.LogoURL,
		})
		streams[e.Number] = e.StreamURL
	}
	d.mu.Lock()
	d.streams = streams
	d.mu.Unlock()
	return out, nil
}

// OpenStream proxies the upstream HTTP stream URL. Closing the body
// releases the upstream connection. Some IPTV providers require a User-
// Agent header that matches the original device — pass through whatever
// the operator configured.
func (d *M3UDriver) OpenStream(ctx context.Context, channelNumber string) (Stream, error) {
	d.mu.RLock()
	streamURL, ok := d.streams[channelNumber]
	d.mu.RUnlock()
	if !ok {
		return nil, ErrChannelNotFound
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, err
	}
	if d.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", d.cfg.UserAgent)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("m3u open stream: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, nil
	case http.StatusServiceUnavailable, http.StatusTooManyRequests:
		// Some IPTV providers signal "out of slots" with 503 or 429.
		resp.Body.Close()
		return nil, ErrAllTunersBusy
	case http.StatusNotFound, http.StatusGone:
		resp.Body.Close()
		return nil, ErrChannelNotFound
	default:
		resp.Body.Close()
		return nil, fmt.Errorf("m3u open stream: status %d", resp.StatusCode)
	}
}

// Probe re-fetches the playlist as a reachability check. Heavier than the
// HDHomeRun probe but there's no separate "is this provider up" endpoint.
func (d *M3UDriver) Probe(ctx context.Context) error {
	body, err := d.fetchPlaylist(ctx)
	if err != nil {
		return err
	}
	body.Close()
	return nil
}

func (d *M3UDriver) fetchPlaylist(ctx context.Context) (io.ReadCloser, error) {
	if strings.HasPrefix(d.cfg.Source, "http://") || strings.HasPrefix(d.cfg.Source, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.cfg.Source, nil)
		if err != nil {
			return nil, err
		}
		if d.cfg.UserAgent != "" {
			req.Header.Set("User-Agent", d.cfg.UserAgent)
		}
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("playlist fetch: status %d", resp.StatusCode)
		}
		return resp.Body, nil
	}
	// File path.
	f, err := os.Open(d.cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("playlist open: %w", err)
	}
	return f, nil
}

// extInfRe matches an EXTINF line's attribute=value pairs. M3U attributes
// are quoted with double quotes and separated by spaces. The trailing
// `,Display Name` lives outside the regex because it has different
// quoting rules and may contain commas itself.
var extInfAttrRe = regexp.MustCompile(`(\w[\w-]*)="([^"]*)"`)

// parseM3U walks an Extended M3U playlist. Format:
//
//	#EXTM3U
//	#EXTINF:-1 tvg-id="abc" tvg-chno="5.1" tvg-logo="http://..." tvg-name="WCBS",WCBS-DT
//	http://stream.url/...
//
// Stream URL is the first non-comment, non-empty line after each EXTINF.
// Channel number falls back to a sequence counter when tvg-chno is absent
// because the upsert key has to be stable across re-fetches.
func parseM3U(r io.Reader) ([]m3uEntry, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // tolerate very long lines

	var entries []m3uEntry
	var pending m3uEntry
	var havePending bool
	seq := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "#EXTM3U" {
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			pending = m3uEntry{}
			havePending = true
			// Pull attributes from the part before the comma; display name
			// from after.
			body := strings.TrimPrefix(line, "#EXTINF:")
			commaIdx := strings.Index(body, ",")
			var attrs string
			if commaIdx >= 0 {
				attrs = body[:commaIdx]
				pending.Name = strings.TrimSpace(body[commaIdx+1:])
			} else {
				attrs = body
			}
			for _, m := range extInfAttrRe.FindAllStringSubmatch(attrs, -1) {
				switch m[1] {
				case "tvg-id":
					pending.Callsign = m[2]
				case "tvg-chno":
					pending.Number = m[2]
				case "tvg-name":
					if pending.Name == "" {
						pending.Name = m[2]
					}
				case "tvg-logo":
					pending.LogoURL = m[2]
				}
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			// Other directive (#EXTGRP, #EXTVLCOPT, etc.) — skip.
			continue
		}
		if !havePending {
			// Stream URL without an EXTINF: skip rather than attach to
			// previous, since we'd lose the channel name.
			continue
		}
		seq++
		pending.StreamURL = line
		if pending.Number == "" {
			pending.Number = fmt.Sprintf("%d", seq)
		}
		if pending.Name == "" {
			pending.Name = pending.Number
		}
		entries = append(entries, pending)
		havePending = false
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}
