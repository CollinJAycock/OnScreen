package middleware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type peerTrustCtxKey struct{}

// WithPeerTrust records the trust decision made against the ORIGINAL immediate
// peer, before [TrustedRealIP] overwrites r.RemoteAddr with the forwarded
// client address.
func WithPeerTrust(r *http.Request, trusted bool) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), peerTrustCtxKey{}, trusted))
}

// peerTrust returns the recorded trust decision. The bool result reports
// whether TrustedRealIP ran at all — when it hasn't (unit tests, the metrics
// mux, the worker health server), callers fall back to inspecting RemoteAddr,
// which is still the real peer in that case.
func peerTrust(r *http.Request) (trusted bool, recorded bool) {
	v, ok := r.Context().Value(peerTrustCtxKey{}).(bool)
	return v, ok
}

// IsSecure reports whether the request arrived over HTTPS.
//
// Direct TLS is always trusted. X-Forwarded-Proto: https is honoured only when
// the immediate peer is loopback or RFC1918 / unique-local — i.e., a reverse
// proxy on the same host or the same private network. Internet-facing clients
// can't influence this; if a proxy on a public IP fronts OnScreen, it must
// terminate TLS itself or this returns false.
func IsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https" && PeerIsTrustedProxy(r)
}

// PeerIsTrustedProxy reports whether the IMMEDIATE peer was loopback or
// RFC1918 / unique-local — the only sources we trust to set X-Forwarded-*.
//
// It reads the decision [TrustedRealIP] stamped into the request context
// rather than re-deriving it from r.RemoteAddr, because TrustedRealIP has by
// then REPLACED RemoteAddr with the forwarded client address. Re-deriving from
// the mutated field inverted the answer behind a reverse proxy — the documented
// deployment — so `X-Forwarded-Proto: https` was discarded, HSTS was not
// emitted, and auth cookies were issued without Secure. It failed silently:
// a LAN client forwards a private IP that still passes the range test, so it
// worked in local testing and broke only for remote users.
func PeerIsTrustedProxy(r *http.Request) bool {
	if trusted, recorded := peerTrust(r); recorded {
		return trusted
	}
	return RemoteAddrIsTrusted(r)
}

// trustedProxyNets, when non-empty, is the AUTHORITATIVE allowlist of peers
// permitted to set X-Forwarded-*. Configured from TRUSTED_PROXIES.
//
// Default (empty) keeps the historical behaviour: any loopback or RFC1918 /
// unique-local peer is trusted. That is too broad on the ordinary self-hosted
// deployment, where every client is on the LAN and can therefore forge
// X-Forwarded-For to rotate its per-IP rate-limit key and defeat login
// brute-force protection, or forge the audit-log IP.
//
// It is deliberately NOT tightened to loopback-only by default: the proxy is
// commonly a sibling CONTAINER (docker compose, a cloudflared tunnel, a
// TrueNAS app) reaching us from a private, non-loopback address. Refusing
// those by default would make the server ignore X-Forwarded-Proto from its own
// reverse proxy, which drops HSTS and strips Secure from auth cookies — the
// exact defect fixed above, reintroduced by a "hardening" default. Operators
// who know their proxy's address should set TRUSTED_PROXIES.
var trustedProxyNets []netip.Prefix

// SetTrustedProxies installs the allowlist. Each entry is a CIDR ("10.0.0.0/8")
// or a bare IP ("172.18.0.2", treated as a /32 or /128). Invalid entries are
// returned as an error and the allowlist is left unchanged, so a typo fails
// startup loudly rather than silently trusting nothing (which would break
// proxied deployments) or everything.
//
// Call once during startup, before serving.
func SetTrustedProxies(entries []string) error {
	var nets []netip.Prefix
	for _, raw := range entries {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			nets = append(nets, p)
			continue
		}
		addr, err := netip.ParseAddr(s)
		if err != nil {
			return fmt.Errorf("trusted proxy %q: not an IP or CIDR", s)
		}
		nets = append(nets, netip.PrefixFrom(addr, addr.BitLen()))
	}
	trustedProxyNets = nets
	return nil
}

// TrustedProxiesConfigured reports whether an explicit allowlist is in force.
// Startup logs a warning when it is not, so the broad default is visible.
func TrustedProxiesConfigured() bool { return len(trustedProxyNets) > 0 }

// RemoteAddrIsTrusted reports whether the request's RemoteAddr is a peer we
// accept X-Forwarded-* from: a member of the configured allowlist, or — when
// none is configured — any loopback / RFC1918 / unique-local address.
//
// Prefer [PeerIsTrustedProxy] for authorization-shaped decisions: downstream of
// TrustedRealIP this field holds the forwarded CLIENT address, not the peer.
func RemoteAddrIsTrusted(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if len(trustedProxyNets) > 0 {
		addr, perr := netip.ParseAddr(host)
		if perr != nil {
			return false
		}
		addr = addr.Unmap() // an IPv4-mapped IPv6 peer must match an IPv4 prefix
		for _, p := range trustedProxyNets {
			if p.Contains(addr) {
				return true
			}
		}
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

// TrustedRealIP rewrites r.RemoteAddr from the first IP in X-Forwarded-For (or
// the X-Real-IP header) ONLY when the immediate peer is a trusted private
// address. Public peers can spoof these headers freely, so we ignore them and
// keep the real RemoteAddr — preventing rate-limit / audit-log spoofing.
//
// This replaces chi's middleware.RealIP, which honours the headers
// unconditionally.
func TrustedRealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Decide trust against the TRUE peer and record it before the
		// overwrite below destroys the only copy of that address. Everything
		// downstream (HSTS, cookie Secure, audit) must read the recorded
		// decision, not re-derive it.
		trusted := RemoteAddrIsTrusted(r)
		r = WithPeerTrust(r, trusted)
		if trusted {
			if ip := firstForwardedIP(r); ip != "" {
				r.RemoteAddr = ip
			}
		}
		next.ServeHTTP(w, r)
	})
}

func firstForwardedIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		xff = strings.TrimSpace(xff)
		if net.ParseIP(xff) != nil {
			return xff
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		if net.ParseIP(xri) != nil {
			return xri
		}
	}
	return ""
}
