package middleware

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/valkey"
)

// RateLimitConfig defines limits for a specific endpoint class.
type RateLimitConfig struct {
	Limit  int
	Window time.Duration
}

var (
	// AuthLimit applies to /auth/login — 10 req/min per IP.
	AuthLimit = RateLimitConfig{Limit: 10, Window: time.Minute}
	// SessionLimit applies to all authenticated endpoints — 1000 req/min per token.
	SessionLimit = RateLimitConfig{Limit: 1000, Window: time.Minute}
	// TranscodeStartLimit caps how often a session can spin up new ffmpeg
	// transcode jobs. Each Start kicks off a hardware encoder and writes a
	// segment directory; a runaway client (or a bug in the player) shouldn't
	// be able to DoS the host by hammering the endpoint.
	TranscodeStartLimit = RateLimitConfig{Limit: 10, Window: time.Minute}
	// DiscoverLimit caps TMDB-backed search hits per session. The Discover
	// endpoint proxies every keystroke to TMDB; even with debouncing in the
	// UI a noisy client could burn the operator's TMDB budget.
	DiscoverLimit = RateLimitConfig{Limit: 60, Window: time.Minute}
)

// RateLimit returns a middleware that enforces the given rate limit config.
// keyFn extracts the rate limit key from the request (e.g. client IP or session hash).
func RateLimit(limiter *valkey.RateLimiter, cfg RateLimitConfig, keyFn func(r *http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			allowed, remaining, resetAt, err := limiter.Allow(r.Context(), key, cfg.Limit, cfg.Window)
			if err != nil {
				writeRateLimitError(w, http.StatusServiceUnavailable,
					"RATE_LIMITER_UNAVAILABLE", "request cancelled")
				return
			}

			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", cfg.Limit))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
			if !resetAt.IsZero() {
				w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetAt.Unix()))
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(time.Until(resetAt).Seconds())))
			}

			if !allowed {
				writeRateLimitError(w, http.StatusTooManyRequests,
					"RATE_LIMITED", "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IPKey returns a key function that uses the client IP for rate limiting.
func IPKey(prefix string) func(r *http.Request) string {
	return func(r *http.Request) string {
		ip := clientIP(r)
		return fmt.Sprintf("%s:%s", prefix, ip)
	}
}

// SessionKey returns a key function using the user ID hash, falling back to IP.
func SessionKey(prefix string) func(r *http.Request) string {
	return func(r *http.Request) string {
		claims := ClaimsFromContext(r.Context())
		if claims == nil {
			return IPKey(prefix)(r)
		}
		return fmt.Sprintf("%s:%s", prefix, auth.HashToken(claims.UserID.String()))
	}
}

// writeRateLimitError emits an envelope-shaped error so clients see the
// same shape as every other endpoint (code/message/request_id). The
// respond package is in internal/api/respond, but importing it here
// would create a middleware→api cycle; duplicate the small shape
// inline instead.
func writeRateLimitError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

// clientIP extracts the client IP from r.RemoteAddr.
// chi's RealIP middleware (applied globally) already rewrites RemoteAddr
// from X-Forwarded-For / X-Real-IP, so we don't re-parse those headers here.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
