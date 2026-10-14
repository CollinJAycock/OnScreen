package v1

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"unicode"

	"github.com/onscreen/onscreen/internal/audit"
)

// SessionClient describes the device a sign-in or token refresh came from, as
// recorded on its sessions row for the "Signed-in devices" list. Everything
// here is display-only: the label is derived from client-supplied headers and
// is never used for an authorization decision.
type SessionClient struct {
	// ClientName is a human label ("Chrome on Windows", "Roku"). From the
	// optional X-Device-Name header when a client sends one, else derived
	// from the User-Agent.
	ClientName string
	// Platform is a coarse family: web, android, ios, roku, tizen, webos,
	// xbox, or "" when nothing recognisable was sent.
	Platform string
	// IP is the trusted client address — r.RemoteAddr after TrustedRealIP,
	// the same value the audit log records. Never a raw forwarded header.
	IP *netip.Addr
}

type sessionClientKey struct{}

// CaptureSessionClient stores the request's SessionClient in the context so
// the session-minting code (cmd/server authService, which only sees a ctx)
// can label the sessions row it creates or refreshes. Mounted on the routes
// that mint or rotate refresh sessions: login, refresh, SSO callbacks, TOTP
// verify, invite accept, device-pairing poll. No DB access; cheap.
func CaptureSessionClient(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), sessionClientKey{}, describeSessionClient(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SessionClientFromContext returns the SessionClient CaptureSessionClient
// stored, or ok=false when the request didn't pass through it (tests, a
// session minted outside an HTTP request).
func SessionClientFromContext(ctx context.Context) (SessionClient, bool) {
	c, ok := ctx.Value(sessionClientKey{}).(SessionClient)
	return c, ok
}

// WithSessionClient attaches c to ctx. For tests and server-side callers.
func WithSessionClient(ctx context.Context, c SessionClient) context.Context {
	return context.WithValue(ctx, sessionClientKey{}, c)
}

// maxClientLabel bounds the stored device label; anything longer is a client
// sending junk, not a device name.
const maxClientLabel = 64

func describeSessionClient(r *http.Request) SessionClient {
	var c SessionClient
	if a, err := netip.ParseAddr(audit.ClientIP(r)); err == nil {
		a = a.Unmap()
		c.IP = &a
	}
	c.ClientName, c.Platform = describeUserAgent(r.UserAgent())
	if dn := cleanLabel(r.Header.Get("X-Device-Name")); dn != "" {
		c.ClientName = dn
	}
	return c
}

// cleanLabel strips control characters and bounds a client-supplied label.
func cleanLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len([]rune(s)) > maxClientLabel {
		s = string([]rune(s)[:maxClientLabel])
	}
	return s
}

// describeUserAgent turns a User-Agent into a device label and platform
// family. First-party clients don't send a device name, so this is what tells
// "Chrome on Windows" from "Roku" in the devices list. Order matters: TV
// browsers and the Xbox shell carry Chrome/Edge tokens too.
func describeUserAgent(ua string) (name, platform string) {
	l := strings.ToLower(ua)
	switch {
	case ua == "":
		return "", ""
	case strings.Contains(l, "roku"):
		return "Roku", "roku"
	case strings.Contains(l, "tizen"):
		return "Samsung TV", "tizen"
	case strings.Contains(l, "web0s") || strings.Contains(l, "webos"):
		return "LG TV", "webos"
	case strings.Contains(l, "xbox"):
		return "Xbox", "xbox"
	case strings.HasPrefix(l, "okhttp/") || strings.HasPrefix(l, "dalvik/") ||
		strings.HasPrefix(l, "onscreenphone"):
		// The Android TV and phone apps' API calls use OkHttp's default UA.
		return "Android app", "android"
	}
	if !strings.HasPrefix(l, "mozilla/") {
		// A script or tool: name it by its product token ("curl", "python-requests").
		tok := ua
		if i := strings.IndexAny(tok, "/ "); i > 0 {
			tok = tok[:i]
		}
		return cleanLabel(tok), ""
	}
	browser := "Browser"
	switch {
	case strings.Contains(l, "edg/"):
		browser = "Edge"
	case strings.Contains(l, "opr/"):
		browser = "Opera"
	case strings.Contains(l, "firefox/") || strings.Contains(l, "fxios/"):
		browser = "Firefox"
	case strings.Contains(l, "chrome/") || strings.Contains(l, "crios/"):
		browser = "Chrome"
	case strings.Contains(l, "safari/"):
		browser = "Safari"
	}
	osName, plat := "", "web"
	switch {
	case strings.Contains(l, "iphone") || strings.Contains(l, "ipad"):
		osName, plat = "iOS", "ios"
	case strings.Contains(l, "android"):
		osName = "Android"
	case strings.Contains(l, "windows"):
		osName = "Windows"
	case strings.Contains(l, "cros"):
		osName = "ChromeOS"
	case strings.Contains(l, "mac os x") || strings.Contains(l, "macintosh"):
		osName = "macOS"
	case strings.Contains(l, "linux"):
		osName = "Linux"
	}
	if osName == "" {
		return browser, plat
	}
	return browser + " on " + osName, plat
}
