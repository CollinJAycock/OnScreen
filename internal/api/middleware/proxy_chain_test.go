package middleware_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onscreen/onscreen/internal/api/middleware"
)

var tlsState = tls.ConnectionState{HandshakeComplete: true}

// The defect these guard against: TrustedRealIP overwrites r.RemoteAddr with
// the forwarded CLIENT address, and IsSecure used to re-derive proxy trust from
// that same field. Behind a TLS-terminating reverse proxy — the deployment
// docs/deployment.md recommends — the field no longer held the proxy's address,
// so X-Forwarded-Proto: https was discarded: no HSTS, and auth cookies issued
// without Secure.
//
// The pre-existing tests could not catch it because they exercise IsSecure and
// TrustedRealIP in ISOLATION on hand-built requests. Only the composed chain
// reproduces it, so these tests run the real middleware stack in order.

// chain runs the middlewares outermost-first, mirroring chi's ordering in
// internal/api/router.go (TrustedRealIP then SecurityHeaders).
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// proxiedRequest models what a reverse proxy on the same host actually sends:
// it connects from loopback and forwards the real client's public IP + scheme.
func proxiedRequest(peer, clientIP, proto string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/hub", nil)
	r.RemoteAddr = peer
	r.Header.Set("X-Forwarded-For", clientIP)
	r.Header.Set("X-Forwarded-Proto", proto)
	return r
}

func TestIsSecure_SurvivesTrustedRealIPRewrite(t *testing.T) {
	var got bool
	h := chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = middleware.IsSecure(r)
		}),
		middleware.TrustedRealIP,
	)

	// Loopback proxy forwarding a PUBLIC client over https.
	h.ServeHTTP(httptest.NewRecorder(), proxiedRequest("127.0.0.1:54321", "203.0.113.7", "https"))

	if !got {
		t.Fatal("IsSecure() = false behind a loopback TLS-terminating proxy; " +
			"X-Forwarded-Proto was discarded because trust was re-derived from " +
			"the rewritten RemoteAddr. Auth cookies lose Secure and HSTS is dropped.")
	}
}

func TestSecurityHeaders_EmitsHSTSBehindProxy(t *testing.T) {
	h := chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }),
		middleware.TrustedRealIP,
		middleware.SecurityHeaders,
	)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, proxiedRequest("127.0.0.1:54321", "203.0.113.7", "https"))

	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("no Strict-Transport-Security behind a TLS-terminating proxy")
	}
}

func TestIsSecure_StillRejectsSpoofedProtoFromPublicPeer(t *testing.T) {
	// The control must not have been loosened into "trust X-Forwarded-Proto
	// unconditionally": a direct internet client setting the header itself
	// gets no credit.
	var got bool
	h := chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = middleware.IsSecure(r)
		}),
		middleware.TrustedRealIP,
	)

	h.ServeHTTP(httptest.NewRecorder(), proxiedRequest("203.0.113.7:44444", "10.0.0.9", "https"))

	if got {
		t.Fatal("IsSecure() = true for a public peer that set X-Forwarded-Proto itself")
	}
}

func TestIsSecure_PlainHTTPBehindProxyStaysInsecure(t *testing.T) {
	var got bool
	h := chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = middleware.IsSecure(r)
		}),
		middleware.TrustedRealIP,
	)

	h.ServeHTTP(httptest.NewRecorder(), proxiedRequest("127.0.0.1:54321", "203.0.113.7", "http"))

	if got {
		t.Fatal("IsSecure() = true when the proxy reported plain http")
	}
}

func TestTrustedRealIP_StillRewritesRemoteAddr(t *testing.T) {
	// The trust decision moved into the context, but the rewrite itself must
	// remain — rate limiting and audit keying depend on it.
	var seen string
	h := chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.RemoteAddr }),
		middleware.TrustedRealIP,
	)

	h.ServeHTTP(httptest.NewRecorder(), proxiedRequest("127.0.0.1:54321", "203.0.113.7", "https"))

	if seen != "203.0.113.7" {
		t.Fatalf("RemoteAddr = %q, want the forwarded client 203.0.113.7", seen)
	}
}

func TestIsSecure_WithoutTrustedRealIPFallsBackToRemoteAddr(t *testing.T) {
	// Not every mux installs TrustedRealIP (the metrics listener, the worker
	// health server, direct unit tests). With no recorded decision, IsSecure
	// must still work off the un-rewritten RemoteAddr.
	r := proxiedRequest("127.0.0.1:54321", "203.0.113.7", "https")

	if !middleware.IsSecure(r) {
		t.Fatal("IsSecure() = false for a loopback peer with no TrustedRealIP in the chain")
	}
}

func TestIsSecure_DirectTLSAlwaysSecure(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://example.test/api/v1/hub", nil)
	r.RemoteAddr = "203.0.113.7:44444"
	r.TLS = &tlsState

	if !middleware.IsSecure(r) {
		t.Fatal("IsSecure() = false for a direct TLS connection")
	}
}
