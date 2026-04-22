package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/testvalkey"
	"github.com/onscreen/onscreen/internal/valkey"
)

func newTestRateLimiter(t *testing.T) *valkey.RateLimiter {
	t.Helper()
	v := testvalkey.New(t)
	return valkey.NewRateLimiter(v, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
}

func sendRequest(handler http.Handler, remoteAddr string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.RemoteAddr = remoteAddr
	handler.ServeHTTP(rec, req)
	return rec
}

func TestRateLimit_AllowsUnderLimitAndSetsHeaders(t *testing.T) {
	rl := newTestRateLimiter(t)
	cfg := RateLimitConfig{Limit: 3, Window: 60 * 1000 * 1000 * 1000} // 60s in ns
	mw := RateLimit(rl, cfg, IPKey("test"))
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := sendRequest(handler, "192.0.2.1:1234")
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-RateLimit-Limit"); got != "3" {
		t.Errorf("X-RateLimit-Limit: got %q, want 3", got)
	}
	if got, _ := strconv.Atoi(rec.Header().Get("X-RateLimit-Remaining")); got != 2 {
		t.Errorf("X-RateLimit-Remaining: got %d, want 2", got)
	}
	if rec.Header().Get("X-RateLimit-Reset") == "" {
		t.Errorf("X-RateLimit-Reset header missing")
	}
}

func TestRateLimit_Returns429AtLimit(t *testing.T) {
	rl := newTestRateLimiter(t)
	cfg := RateLimitConfig{Limit: 2, Window: 60 * 1000 * 1000 * 1000}
	mw := RateLimit(rl, cfg, IPKey("ip"))
	called := 0
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++ }))

	for i := 0; i < 2; i++ {
		if rec := sendRequest(handler, "203.0.113.5:5555"); rec.Code != http.StatusOK {
			t.Fatalf("call %d: got %d, want 200", i, rec.Code)
		}
	}
	rec := sendRequest(handler, "203.0.113.5:5555")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("3rd call: got %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Errorf("Retry-After header missing on 429")
	}
	if called != 2 {
		t.Errorf("downstream handler called %d times, want 2", called)
	}
}

func TestRateLimit_KeysAreIsolatedByIP(t *testing.T) {
	rl := newTestRateLimiter(t)
	cfg := RateLimitConfig{Limit: 1, Window: 60 * 1000 * 1000 * 1000}
	mw := RateLimit(rl, cfg, IPKey("ip"))
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	if rec := sendRequest(handler, "10.0.0.1:1111"); rec.Code != http.StatusOK {
		t.Fatalf("alice first: %d", rec.Code)
	}
	if rec := sendRequest(handler, "10.0.0.1:1111"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("alice second: got %d, want 429", rec.Code)
	}
	if rec := sendRequest(handler, "10.0.0.2:2222"); rec.Code != http.StatusOK {
		t.Errorf("bob should not be affected by alice; got %d", rec.Code)
	}
}

func TestRateLimit_SessionKeyHashesPerUser(t *testing.T) {
	rl := newTestRateLimiter(t)
	cfg := RateLimitConfig{Limit: 1, Window: 60 * 1000 * 1000 * 1000}
	mw := RateLimit(rl, cfg, SessionKey("session"))
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	uidA, uidB := uuid.New(), uuid.New()
	withClaims := func(uid uuid.UUID) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = "127.0.0.1:9999"
		ctx := WithClaims(r.Context(), &auth.Claims{UserID: uid, Username: "x"})
		return r.WithContext(ctx)
	}

	// User A first call OK, second 429.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, withClaims(uidA))
	if rec.Code != http.StatusOK {
		t.Fatalf("A1: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, withClaims(uidA))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("A2: got %d, want 429", rec.Code)
	}

	// User B is independent.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, withClaims(uidB))
	if rec.Code != http.StatusOK {
		t.Errorf("B1: got %d, want 200 (different user)", rec.Code)
	}
}

func TestRateLimit_SessionKeyFallsBackToIPWhenAnonymous(t *testing.T) {
	rl := newTestRateLimiter(t)
	cfg := RateLimitConfig{Limit: 1, Window: 60 * 1000 * 1000 * 1000}
	mw := RateLimit(rl, cfg, SessionKey("session"))
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	// No claims attached — should bucket by IP.
	if rec := sendRequest(handler, "198.51.100.1:1234"); rec.Code != http.StatusOK {
		t.Fatalf("first anon call: %d", rec.Code)
	}
	if rec := sendRequest(handler, "198.51.100.1:1234"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("second anon call: got %d, want 429 (IP fallback should kick in)", rec.Code)
	}
}

// TestLimitConfigsAreSane is a smoke test ensuring the limit configs declared
// in ratelimit.go aren't accidentally zeroed or set to absurd values. These
// constants gate real production protections; a typo (Limit: 0 = always 429)
// would silently take the API offline.
func TestLimitConfigsAreSane(t *testing.T) {
	cases := []struct {
		name string
		cfg  RateLimitConfig
	}{
		{"AuthLimit", AuthLimit},
		{"SessionLimit", SessionLimit},
		{"TranscodeStartLimit", TranscodeStartLimit},
		{"DiscoverLimit", DiscoverLimit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.cfg.Limit <= 0 {
				t.Errorf("Limit must be >0, got %d", c.cfg.Limit)
			}
			if c.cfg.Window <= 0 {
				t.Errorf("Window must be >0, got %v", c.cfg.Window)
			}
		})
	}

	// TranscodeStart must be tighter than SessionLimit — otherwise wiring the
	// extra middleware accomplishes nothing.
	if TranscodeStartLimit.Limit >= SessionLimit.Limit {
		t.Errorf("TranscodeStartLimit (%d) should be tighter than SessionLimit (%d)",
			TranscodeStartLimit.Limit, SessionLimit.Limit)
	}
	if DiscoverLimit.Limit >= SessionLimit.Limit {
		t.Errorf("DiscoverLimit (%d) should be tighter than SessionLimit (%d)",
			DiscoverLimit.Limit, SessionLimit.Limit)
	}
}
