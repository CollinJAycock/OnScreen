// Package safehttp supplies net.Dialer and http.Client values that
// reject connections to private / loopback / link-local / metadata-
// service addresses after DNS resolution. Used everywhere the server
// opens a connection to an operator-configured URL (XMLTV, M3U,
// HDHomeRun, LDAP, lyrics overrides) so admin session hijacking can't
// be used as an internal-network probe.
//
// Admin-initiated fetches to legitimate external services (tmdb.org,
// lrclib.net, schedules-direct.org, public IPTV providers) work
// unchanged because their DNS resolves to public IPs. Local HDHomeRuns
// are an explicit exception — they live on RFC1918 and we mark that
// dialer with AllowPrivate: true.
package safehttp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// ErrBlockedAddress is returned when a dial target resolves to a
// forbidden IP range. Kept distinct from generic network errors so
// callers can surface a clearer message ("host resolves to a private
// address — configure an allowlist if intentional") vs a real outage.
var ErrBlockedAddress = errors.New("safehttp: address blocked by allowlist policy")

// DialPolicy configures what IP ranges the dialer will connect to.
//
// Default zero value is the safest: public IPv4/IPv6 only. Set
// AllowPrivate=true on the dialer you hand to the HDHomeRun driver
// (local tuners are always RFC1918/link-local by definition).
type DialPolicy struct {
	AllowPrivate   bool // RFC1918, RFC4193 (fc00::/7), RFC6598 CGNAT (100.64.0.0/10)
	AllowLoopback  bool // 127.0.0.0/8, ::1/128
	AllowLinkLocal bool // 169.254.0.0/16 (includes AWS 169.254.169.254 metadata)
}

// NewDialer returns a net.Dialer that enforces policy on every Control
// callback. The Control callback fires after DNS resolution and before
// TCP connect, which is the correct hook for IP-level filtering.
func NewDialer(p DialPolicy) *net.Dialer {
	return &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   func(network, address string, _ syscall.RawConn) error { return checkAddress(p, address) },
	}
}

// NewClient returns an *http.Client whose Transport rejects forbidden
// addresses. Timeout applies to the whole request-response cycle; set
// 0 for streaming responses (HLS segment fetches, long-lived SSE).
func NewClient(p DialPolicy, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           NewDialer(p).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

// checkAddress parses "host:port" and enforces the policy on the IP.
// Returns ErrBlockedAddress wrapping a human-readable reason on miss.
//
// We check at the post-resolution layer (Control callback receives the
// already-resolved address) so a DNS rebinding attack can't slip
// through: the IP that Go connects to is exactly the IP we check here,
// not the one resolved separately by the calling code.
func checkAddress(p DialPolicy, address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		// Some callers pass bare host; treat as unresolved and block.
		return fmt.Errorf("%w: malformed address %q", ErrBlockedAddress, address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: unresolved host %q", ErrBlockedAddress, host)
	}
	return checkIP(p, ip)
}

// checkIP enforces the policy on a parsed IP. Split out from checkAddress so
// it can recurse on the IPv4 embedded in a NAT64 / 6to4 IPv6 address.
func checkIP(p DialPolicy, ip net.IP) error {
	switch {
	case ip.IsLoopback():
		if p.AllowLoopback {
			return nil
		}
		return fmt.Errorf("%w: loopback address %s", ErrBlockedAddress, ip)
	case ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast():
		if p.AllowLinkLocal {
			return nil
		}
		return fmt.Errorf("%w: link-local address %s", ErrBlockedAddress, ip)
	case ip.IsPrivate():
		if p.AllowPrivate {
			return nil
		}
		return fmt.Errorf("%w: private address %s", ErrBlockedAddress, ip)
	case isCGNAT(ip):
		// RFC 6598 shared address space (100.64.0.0/10): carrier-grade NAT
		// and overlay networks (Tailscale) live here. Go's IsPrivate()
		// doesn't cover it, so without this case it falls through to
		// "allowed". Treat it like RFC1918 — permitted only when the policy
		// opts into private ranges.
		if p.AllowPrivate {
			return nil
		}
		return fmt.Errorf("%w: shared/CGNAT address %s", ErrBlockedAddress, ip)
	case ip.IsUnspecified():
		// 0.0.0.0 / ::
		return fmt.Errorf("%w: unspecified address %s", ErrBlockedAddress, ip)
	case ip.IsMulticast():
		return fmt.Errorf("%w: multicast address %s", ErrBlockedAddress, ip)
	}
	// NAT64 (64:ff9b::/96) and 6to4 (2002::/16) wrap an IPv4 address inside an
	// IPv6 one. On a host behind such a gateway a "public-looking" IPv6 target
	// can forward to a private/metadata IPv4 (e.g. 169.254.169.254), slipping
	// past the checks above which only see the outer v6. Re-check the inner v4.
	if inner := embeddedV4(ip); inner != nil {
		return checkIP(p, inner)
	}
	return nil
}

// cgnatNet is RFC 6598 shared address space (100.64.0.0/10) — carrier-grade
// NAT and overlay networks (Tailscale). Go's net.IP.IsPrivate doesn't include
// it, so checkAddress tests it explicitly.
var cgnatNet = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n
}()

// isCGNAT reports whether ip falls in the RFC 6598 shared range.
func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && cgnatNet.Contains(v4)
}

// mustCIDR parses a CIDR at init time, panicking on a bad literal.
func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic("safehttp: bad CIDR " + s)
	}
	return n
}

var (
	// nat64Net is the well-known NAT64 prefix (RFC 6052): a target here is
	// forwarded to the IPv4 embedded in its last 32 bits.
	nat64Net = mustCIDR("64:ff9b::/96")
	// sixToFour is the 6to4 prefix (RFC 3056); bytes 2..5 embed the v4.
	sixToFour = mustCIDR("2002::/16")
)

// embeddedV4 extracts the IPv4 tunneled inside a NAT64 / 6to4 IPv6 address,
// or nil when ip carries none. IPv4 and IPv4-mapped inputs return nil — Go's
// IsPrivate/IsLoopback already unwrap those. (Teredo 2001::/32 is not handled;
// it's effectively dead and its embedding is obfuscated.)
func embeddedV4(ip net.IP) net.IP {
	if ip.To4() != nil {
		return nil
	}
	v16 := ip.To16()
	if v16 == nil {
		return nil
	}
	switch {
	case nat64Net.Contains(ip):
		return net.IPv4(v16[12], v16[13], v16[14], v16[15])
	case sixToFour.Contains(ip):
		return net.IPv4(v16[2], v16[3], v16[4], v16[5])
	}
	return nil
}

// IsBlocked reports whether ip is denied under the default public-only policy
// (loopback, private, CGNAT, link-local, unspecified, multicast, and IPv4
// tunneled in NAT64/6to4). Exported so other denylists — e.g. the plugin
// egress path — can share it instead of maintaining a parallel, drift-prone
// copy.
func IsBlocked(ip net.IP) bool {
	return checkIP(DialPolicy{}, ip) != nil
}

// Default returns a client+policy appropriate for public outbound
// fetches (TMDB, LRCLIB, public XMLTV/M3U providers). No private
// ranges allowed.
func Default() *http.Client { return NewClient(DialPolicy{}, 30*time.Second) }

// LocalDevice returns a client+policy appropriate for operator-
// configured local network devices (HDHomeRun). Allows RFC1918 +
// loopback + link-local.
func LocalDevice() *http.Client {
	return NewClient(DialPolicy{
		AllowPrivate: true, AllowLoopback: true, AllowLinkLocal: true,
	}, 30*time.Second)
}

// ResolveContextKey ensures the package exports something that takes a
