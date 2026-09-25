package v1

import (
	"net"
	"net/http"
	"strings"
)

// setupHostAllowed reports whether first-run setup may be completed through the
// Host the browser addressed.
//
// The local-peer gate on first-run registration is blind to DNS rebinding: a
// page on an attacker's domain, opened by anyone on the server's LAN, can
// re-point that domain at the server's private address. The browser then treats
// the server as same-origin with the attacker's page — the request comes from a
// LAN client (passes isLocalPeer), carries Sec-Fetch-Site: same-origin (passes
// CSRFGuard), and its response, token pair included, is readable by the
// attacker's script. The one thing the attacker cannot change is the Host
// header: it names their domain.
//
// So while setup is open, only accept Hosts an outside party cannot make resolve
// to this server: IP literals, localhost, single-label names, and the
// local-only suffixes (.local, .localhost, .home.arpa, .internal, .lan, .home).
// A homelab that reaches the server through a public-looking name (split-horizon
// DNS, a reverse proxy) completes setup via the IP address once, or sets
// ALLOW_PUBLIC_SETUP=true.
func setupHostAllowed(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil || host == "localhost" || !strings.Contains(host, ".") {
		return true
	}
	for _, suffix := range []string{".localhost", ".local", ".home.arpa", ".internal", ".lan", ".home"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}
