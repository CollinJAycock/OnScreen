package middleware

import (
	"context"
	"net"
	"net/http"
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

// RemoteAddrIsTrusted reports whether the request's RemoteAddr is loopback or
// in an RFC1918 / unique-local range.
//
// Prefer [PeerIsTrustedProxy] for authorization-shaped decisions: downstream of
// TrustedRealIP this field holds the forwarded CLIENT address, not the peer.
func RemoteAddrIsTrusted(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
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
