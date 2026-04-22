package plugin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// allowedHostSet is the case-folded set of hostnames a plugin is permitted
// to dial. The plugin's own endpoint host is always implicitly allowed.
type allowedHostSet map[string]struct{}

func newAllowedHostSet(endpoint string, hosts []string) (allowedHostSet, error) {
	set := allowedHostSet{}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	if h := u.Hostname(); h != "" {
		set[strings.ToLower(h)] = struct{}{}
	}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		set[strings.ToLower(h)] = struct{}{}
	}
	return set, nil
}

func (s allowedHostSet) allows(host string) bool {
	_, ok := s[strings.ToLower(host)]
	return ok
}

// isNonPublicIP reports whether ip is on the egress denylist (loopback,
// RFC1918, link-local, unspecified). Exposed as a package var so tests can
// swap it — production callers should never reassign it.
var isNonPublicIP = func(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// clientHardTimeout is an outer safety net on top of per-call context
// deadlines. Context is the primary mechanism; this catches any caller
// that forgets to set one.
const clientHardTimeout = 30 * time.Second

// httpClientForPlugin returns an *http.Client whose dialer:
//   - rejects any host not in the plugin's allowlist (which always includes
//     the plugin's own endpoint host);
//   - resolves DNS itself and validates the resolved IPs against the
//     loopback/private/link-local set, then dials the validated IP directly
//     (not the hostname) so DNS rebinding between validation and dial is
//     impossible — the second lookup can't happen because the dialer never
//     does one;
//   - rejects all HTTP redirects (MCP over Streamable HTTP never legitimately
//     redirects, and a 302 to an allowlisted host with a TTL-0 record would
//     otherwise be a second TOCTOU window);
//   - caps total request time at clientHardTimeout as belt-and-suspenders
//     alongside per-call context deadlines.
//
// Implementation note: this mirrors webhook.SafeTransport but adds the
// per-plugin hostname allowlist, IP-pinned dialing, and redirect rejection.
// Kept separate so policy changes to one surface don't silently affect the other.
func httpClientForPlugin(p Plugin) (*http.Client, error) {
	allowed, err := newAllowedHostSet(p.EndpointURL, p.AllowedHosts)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("egress: split host port: %w", err)
			}
			if !allowed.allows(host) {
				return nil, fmt.Errorf("egress: host %q is not in plugin %q allowlist", host, p.Name)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("egress: resolve %s: %w", host, err)
			}
			// Reject-if-ANY-non-public: split-horizon DNS that returns a
			// mix of public and private addresses is treated as hostile.
			for _, ip := range ips {
				if isNonPublicIP(ip.IP) {
					return nil, fmt.Errorf("egress: %s resolves to non-public address %s", host, ip.IP)
				}
			}
			// Dial the validated IP directly, not the hostname. This closes
			// the TOCTOU window: dialer.DialContext won't re-resolve because
			// the address is already a literal IP. TLS SNI / cert verification
			// still use the URL host via http.Transport's TLSClientConfig,
			// which is derived from the request URL rather than the dial addr.
			var lastErr error
			for _, ip := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = fmt.Errorf("egress: no usable IPs for %s", host)
			}
			return nil, lastErr
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   clientHardTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("egress: redirects are not followed (plugin %q tried to redirect to %s)", p.Name, req.URL)
		},
	}, nil
}
