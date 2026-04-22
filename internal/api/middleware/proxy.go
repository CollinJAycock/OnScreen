package middleware

import (
	"net"
	"net/http"
	"strings"
)

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
	if r.Header.Get("X-Forwarded-Proto") == "https" && RemoteAddrIsTrusted(r) {
		return true
	}
	return false
}

// RemoteAddrIsTrusted reports whether the request's RemoteAddr is loopback or
// in an RFC1918 / unique-local range — the only sources we trust to set
// X-Forwarded-* headers.
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
		if RemoteAddrIsTrusted(r) {
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
