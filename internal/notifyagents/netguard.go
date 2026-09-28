package notifyagents

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/onscreen/onscreen/internal/safehttp"
)

// Network safety. Agents are admin-configured outbound URLs, so the same SSRF
// posture as webhooks and plugin egress applies:
//
//   - Delivery dials through safehttp's dialer, which checks the RESOLVED
//     address in the dialer's Control hook (DNS-rebinding safe: the IP that
//     is checked is the IP that is connected to) and refuses private,
//     loopback, link-local (cloud metadata), CGNAT, multicast and NAT64/6to4-
//     wrapped private targets. No proxy from the environment — a proxy would
//     move the real dial outside the check.
//   - allow_private_network (ntfy / Gotify only) opts into RFC 1918, CGNAT
//     and loopback for a self-hosted server on the LAN or the same box.
//     Link-local stays blocked even then. Plain http is only accepted with
//     the opt-in, and then ONLY to those private addresses — checked at
//     save and again at every dial (netPrivateOnly) — so a token never
//     crosses the internet in the clear.
//   - Discord and Telegram never allow private targets: Discord webhook URLs
//     must be on discord.com / discordapp.com, and Telegram always goes to
//     the fixed api.telegram.org.
//   - Redirects are never followed: a 3xx would carry the POST (and, for
//     Discord and Telegram, the secret-bearing path) to a host nobody
//     approved. The 3xx surfaces as a delivery error instead.

// discordHosts are the only hosts a Discord webhook URL may name.
var discordHosts = map[string]bool{"discord.com": true, "discordapp.com": true}

// discordPath matches /api/webhooks/{id}/{token}, optionally versioned
// (/api/v10/webhooks/…).
var discordPath = regexp.MustCompile(`^/api/(?:v\d{1,2}/)?webhooks/\d{1,25}/[A-Za-z0-9_-]{1,200}$`)

// discordThreadID is the only query parameter a Discord webhook URL may carry.
var discordThreadID = regexp.MustCompile(`^[0-9]{1,25}$`)

// validateDiscordWebhookURL checks a Discord webhook URL against the host
// allow-list. Only https on the default port, no userinfo, and at most a
// numeric thread_id query (forum / thread webhooks).
func validateDiscordWebhookURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Opaque != "" {
		return invalid("the Discord webhook URL is not a valid URL")
	}
	if u.Scheme != "https" {
		return invalid("the Discord webhook URL must use https")
	}
	if u.User != nil {
		return invalid("the Discord webhook URL must not contain credentials")
	}
	if !discordHosts[strings.ToLower(u.Hostname())] || (u.Port() != "" && u.Port() != "443") {
		return invalid("the Discord webhook URL must be on discord.com or discordapp.com")
	}
	if !discordPath.MatchString(u.EscapedPath()) {
		return invalid("the Discord webhook URL must look like https://discord.com/api/webhooks/<id>/<token>")
	}
	if u.Fragment != "" {
		return invalid("the Discord webhook URL must not have a fragment")
	}
	for k, vs := range u.Query() {
		if k != "thread_id" || len(vs) != 1 || !discordThreadID.MatchString(vs[0]) {
			return invalid("the Discord webhook URL may only carry a numeric thread_id parameter")
		}
	}
	return nil
}

// telegramToken matches a bot token: "<bot id>:<secret>".
var telegramToken = regexp.MustCompile(`^\d{3,20}:[A-Za-z0-9_-]{20,100}$`)

// telegramChatID matches a numeric chat id (groups are negative) or a public
// @channel username.
var telegramChatID = regexp.MustCompile(`^(?:-?\d{1,20}|@[A-Za-z][A-Za-z0-9_]{3,31})$`)

// ntfyTopic matches ntfy's topic rules.
var ntfyTopic = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// normalizeServerURL validates an ntfy / Gotify server URL and returns it
// without a trailing slash. http is only accepted with allowPrivate (a LAN
// server without TLS); a public server must use https so a token never
// crosses the internet in the clear.
func normalizeServerURL(raw string, allowPrivate bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", invalid("the server URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Host == "" {
		return "", invalid("the server URL is not a valid URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !allowPrivate {
			return "", invalid("the server URL must use https (plain http is only allowed for a server on your private network)")
		}
	default:
		return "", invalid("the server URL must be http or https")
	}
	if u.User != nil {
		return "", invalid("the server URL must not contain credentials — use the token field")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", invalid("the server URL must not have a query or fragment")
	}
	if u.Hostname() == "" {
		return "", invalid("the server URL must include a host")
	}
	return strings.TrimRight(u.Scheme+"://"+u.Host+u.EscapedPath(), "/"), nil
}

// netPolicy is the address policy one delivery dials under.
type netPolicy int

const (
	// netPublic: public addresses only — Discord, Telegram, and an ntfy /
	// Gotify server without the private-network opt-in.
	netPublic netPolicy = iota
	// netPrivateAllowed: allow_private_network over https — public, RFC 1918,
	// CGNAT and loopback targets.
	netPrivateAllowed
	// netPrivateOnly: allow_private_network over plain http — ONLY RFC 1918,
	// CGNAT and loopback targets. The opt-in is what makes http acceptable
	// at all (a LAN server without TLS), so a token sent in the clear must
	// never reach a public address — not a public host saved with the
	// opt-in, and not a LAN name that later resolves to a public IP.
	netPrivateOnly

	netPolicyCount // number of policies (array size)
)

// agentNetPolicy picks the policy for a delivery to rawURL. Discord and
// Telegram are public services: never private, whatever the row says. Plain
// http is only ever sent under netPrivateOnly; ok is false when rawURL is
// http but the agent hasn't opted into the private network (a row written
// around the API) — the caller must refuse to send.
func agentNetPolicy(k Kind, allowPrivate bool, rawURL string) (p netPolicy, ok bool) {
	private := allowPrivate && k.selfHostable()
	plainHTTP := true
	if u, err := url.Parse(rawURL); err == nil && u.Scheme == "https" {
		plainHTTP = false
	}
	switch {
	case plainHTTP && !private:
		return netPublic, false
	case plainHTTP:
		return netPrivateOnly, true
	case private:
		return netPrivateAllowed, true
	}
	return netPublic, true
}

// errPublicOverHTTP: a plain-http delivery resolved to a public address.
var errPublicOverHTTP = errors.New("plain http to a public address refused (use https)")

// newDialer is the SSRF-guarded dialer for policy p. The address checks run
// in the Control hook, on the RESOLVED address (DNS-rebinding safe).
func newDialer(p netPolicy) *net.Dialer {
	allowPrivate := p != netPublic
	d := safehttp.NewDialer(safehttp.DialPolicy{AllowPrivate: allowPrivate, AllowLoopback: allowPrivate})
	if p == netPrivateOnly {
		guard := d.Control
		d.Control = func(network, address string, c syscall.RawConn) error {
			if err := guard(network, address, c); err != nil {
				return err
			}
			return requireNonPublic(address)
		}
	}
	return d
}

// requireNonPublic refuses a resolved address that is a public one. With the
// safehttp guard run first (link-local, multicast and unspecified already
// refused), what passes is RFC 1918 / CGNAT / loopback — the LAN.
func requireNonPublic(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil || !safehttp.IsBlocked(ip) {
		return fmt.Errorf("%w: %s", errPublicOverHTTP, host)
	}
	return nil
}

// checkResolvedHost is the save-time check that gives the admin an immediate
// "that's a private address" error. The dial-time check is the real guard
// (DNS can change between save and send); this one is for the UX.
func checkResolvedHost(ctx context.Context, lookup func(context.Context, string) ([]string, error), host string, p netPolicy) error {
	var addrs []string
	if ip := net.ParseIP(host); ip != nil {
		addrs = []string{host}
	} else {
		lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		resolved, err := lookup(lctx, host)
		if err != nil || len(resolved) == 0 {
			return invalid("cannot resolve the server host " + host)
		}
		addrs = resolved
	}
	d := newDialer(p)
	for _, a := range addrs {
		if err := d.Control("tcp", net.JoinHostPort(a, "443"), nil); err != nil {
			switch {
			case errors.Is(err, errPublicOverHTTP):
				return invalid("the server host " + host + " resolves to a public address (" + a + ") — plain http is only allowed for a server on your private network; use https")
			case p != netPublic:
				return invalid("the server host " + host + " resolves to a blocked address (" + a + ")")
			}
			return invalid("the server host " + host + " resolves to a private address (" + a + ") — turn on \"Allow private network\" for a server on your LAN")
		}
	}
	return nil
}

// newHTTPClient builds the delivery client for policy p: SSRF-guarded dialer,
// no environment proxy, no redirects, a whole-request timeout. The Service
// builds one per policy and reuses it, so keep-alive connections are pooled
// rather than leaked with a throwaway Transport per send.
func newHTTPClient(p netPolicy, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		// Never the environment proxy: the dial would go to the proxy and
		// the real target would be resolved outside the guard (safehttp).
		Proxy:                 nil,
		DialContext:           newDialer(p).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
