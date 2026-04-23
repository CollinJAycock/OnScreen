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
	"context"
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
	AllowPrivate   bool // RFC1918, RFC4193 (fc00::/7)
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
	case ip.IsUnspecified():
		// 0.0.0.0 / ::
		return fmt.Errorf("%w: unspecified address %s", ErrBlockedAddress, ip)
	case ip.IsMulticast():
		return fmt.Errorf("%w: multicast address %s", ErrBlockedAddress, ip)
	}
	return nil
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
// context — keeps unused-import checkers happy during refactors.
var _ = context.Background
