package middleware

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/observability"
)

// ── proxy.go ─────────────────────────────────────────────────────────────────

func TestRemoteAddrIsTrusted_TrustsLoopback(t *testing.T) {
	cases := []string{"127.0.0.1:1234", "[::1]:1234", "127.0.0.1"}
	for _, addr := range cases {
		req := &http.Request{RemoteAddr: addr}
		if !RemoteAddrIsTrusted(req) {
			t.Errorf("loopback %q should be trusted", addr)
		}
	}
}

func TestRemoteAddrIsTrusted_TrustsRFC1918(t *testing.T) {
	cases := []string{
		"10.0.0.1:1234",
		"192.168.1.5:80",
		"172.16.0.1:443",
		"172.31.255.255:1",
	}
	for _, addr := range cases {
		req := &http.Request{RemoteAddr: addr}
		if !RemoteAddrIsTrusted(req) {
			t.Errorf("private %q should be trusted", addr)
		}
	}
}

func TestRemoteAddrIsTrusted_RejectsPublic(t *testing.T) {
	cases := []string{
		"8.8.8.8:1234",
		"1.1.1.1:443",
		"203.0.113.5:80", // documentation range, treated as public
	}
	for _, addr := range cases {
		req := &http.Request{RemoteAddr: addr}
		if RemoteAddrIsTrusted(req) {
			t.Errorf("public %q must NOT be trusted (X-Forwarded-* spoofing vector)", addr)
		}
	}
}

func TestRemoteAddrIsTrusted_RejectsGarbage(t *testing.T) {
	cases := []string{"not-an-ip", "", "999.999.999.999"}
	for _, addr := range cases {
		req := &http.Request{RemoteAddr: addr}
		if RemoteAddrIsTrusted(req) {
			t.Errorf("garbage %q must NOT be trusted", addr)
		}
	}
}

func TestIsSecure_TLSConnectionAlwaysSecure(t *testing.T) {
	req := httptest.NewRequest("GET", "https://example.com/", nil)
	if !IsSecure(req) {
		t.Error("TLS connection should always report Secure")
	}
}

func TestIsSecure_XForwardedProtoFromTrustedPeer(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:8080"
	req.Header.Set("X-Forwarded-Proto", "https")
	if !IsSecure(req) {
		t.Error("trusted private peer asserting X-Forwarded-Proto: https should be honoured")
	}
}

func TestIsSecure_XForwardedProtoFromUntrustedPeerIgnored(t *testing.T) {
	// Public peer claiming X-Forwarded-Proto: https — must NOT be
	// believed. Otherwise an attacker could trick the cookie path into
	// emitting Secure cookies over an actual plaintext connection.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	req.Header.Set("X-Forwarded-Proto", "https")
	if IsSecure(req) {
		t.Error("public peer's X-Forwarded-Proto must be ignored — security regression")
	}
}

// ── TrustedRealIP middleware ─────────────────────────────────────────────────

func TestTrustedRealIP_RewritesFromXFFWhenPeerIsLoopback(t *testing.T) {
	var got string
	h := TrustedRealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1234" // trusted (loopback proxy)
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "203.0.113.5" {
		t.Errorf("RemoteAddr after TrustedRealIP = %q, want 203.0.113.5 (first XFF)", got)
	}
}

func TestTrustedRealIP_LeavesRemoteAddrAloneWhenPeerIsUntrusted(t *testing.T) {
	// Direct internet client claims XFF — must be ignored. RateLimit
	// and audit log key off RemoteAddr; spoofing here would unlock both.
	var got string
	h := TrustedRealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "8.8.8.8:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "8.8.8.8:1234" {
		t.Errorf("RemoteAddr was rewritten to %q from a public peer's XFF — spoofing vector", got)
	}
}

func TestTrustedRealIP_FallsBackToXRealIP(t *testing.T) {
	var got string
	h := TrustedRealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.10:443" // trusted
	req.Header.Set("X-Real-IP", "203.0.113.99")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "203.0.113.99" {
		t.Errorf("X-Real-IP fallback: got %q, want 203.0.113.99", got)
	}
}

func TestTrustedRealIP_RejectsGarbageInHeader(t *testing.T) {
	// Garbage XFF must NOT silently overwrite RemoteAddr with junk.
	var got string
	h := TrustedRealIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "not-an-ip")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != "10.0.0.1:1234" {
		t.Errorf("got %q — garbage XFF must be ignored, original RemoteAddr preserved", got)
	}
}

// ── RequestID middleware ─────────────────────────────────────────────────────

func TestRequestID_GeneratesNewIDWhenAbsent(t *testing.T) {
	var ctxID string
	var hdrID string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID = observability.RequestIDFromContext(r.Context())
		hdrID = w.Header().Get("X-Request-ID")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if ctxID == "" {
		t.Error("context request ID should be set even when header absent")
	}
	// Header is set BEFORE next ServeHTTP runs; the handler observes
	// the same value.
	if hdrID == "" {
		t.Error("response header X-Request-ID should be set")
	}
	if hdrID != ctxID {
		t.Errorf("ctx ID %q differs from header ID %q", ctxID, hdrID)
	}
}

func TestRequestID_RespectsUpstreamHeader(t *testing.T) {
	// Reverse proxies sometimes inject their own request IDs — we
	// honour them so tracing across the proxy boundary is continuous.
	const upstream = "fdc4f3b2-c4d2-4d35-8a96-9e9f1c5dbe55"

	var got string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = observability.RequestIDFromContext(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", upstream)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got != upstream {
		t.Errorf("got %q, want %q (upstream X-Request-ID should win)", got, upstream)
	}
}

func TestRequestID_AlsoEchoesInResponseHeader(t *testing.T) {
	const upstream = "trace-from-proxy"
	h := RequestID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", upstream)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != upstream {
		t.Errorf("response X-Request-ID = %q, want %q", got, upstream)
	}
}

// ── Logger middleware ────────────────────────────────────────────────────────

func TestLogger_LogsMethodAndPath(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	h := Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hello"))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/items/123", nil))

	out := buf.String()
	if !strings.Contains(out, `"method":"GET"`) {
		t.Errorf("log line missing method: %s", out)
	}
	if !strings.Contains(out, `"path":"/items/123"`) {
		t.Errorf("log line missing path: %s", out)
	}
	if !strings.Contains(out, `"status":418`) {
		t.Errorf("log line missing status: %s", out)
	}
	// Body bytes are tracked through Write — should reflect "hello" = 5 bytes.
	if !strings.Contains(out, `"size":5`) {
		t.Errorf("log line size mismatch: %s", out)
	}
}

func TestLogger_NeverLogsRawQueryString(t *testing.T) {
	// Security regression guard: the Logger middleware must NOT echo
	// r.URL.RawQuery into the structured log. Tokens land in query
	// strings (HLS segment tokens, paste-pair fallback) and we hardened
	// the auth paths against query-param secrets — this prevents the
	// log layer from quietly re-introducing the leak.
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	h := Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/transcode/sessions/abc/seg/seg00001.ts?token=SECRET-TOKEN-VALUE", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if strings.Contains(buf.String(), "SECRET-TOKEN-VALUE") {
		t.Errorf("query-string secret leaked into log line: %s", buf.String())
	}
}

func TestLogger_CapturesRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// The logging middleware reads the request_id from context. Wire
	// RequestID upstream so it's set by the time Logger runs.
	chain := RequestID(Logger(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "req-test-id")
	chain.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), `"request_id":"req-test-id"`) {
		t.Errorf("log line missing request_id: %s", buf.String())
	}
}

// ── Recover middleware ───────────────────────────────────────────────────────

func TestRecover_PanicReturns500(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	h := Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("oh no")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/blow-up", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "oh no") {
		t.Error("panic value leaked into client response — must NEVER expose internals")
	}
	// Logger should record the panic for operators.
	if !strings.Contains(buf.String(), "panic") {
		t.Errorf("panic was not logged: %s", buf.String())
	}
}

func TestRecover_HandlesPanicAfterPartialResponse(t *testing.T) {
	// If headers were already sent, the middleware must NOT try to
	// write the 500 page over the in-flight response.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := Recover(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		panic("then explodes")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (already sent before panic)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "partial") {
		t.Errorf("body = %q, want \"partial\" (pre-panic write)", rec.Body.String())
	}
	// The 500 page must NOT be appended after.
	if strings.Contains(rec.Body.String(), "Internal Server Error") {
		t.Error("recover wrote 500 page after headers — corrupts in-flight response")
	}
}

func TestRecover_NoOpWhenHandlerSucceeds(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	h := Recover(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fine"))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "fine" {
		t.Errorf("body = %q, want \"fine\"", rec.Body.String())
	}
}
