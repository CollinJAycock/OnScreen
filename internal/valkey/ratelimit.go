package valkey

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimiter implements a sliding window rate limiter backed by Valkey.
// Fails open (allows the request) if Valkey is unavailable (ADR-015).
type RateLimiter struct {
	client          *Client
	logger          *slog.Logger
	failOpenCounter func() // increments onscreen_ratelimit_failopen_total
}

// NewRateLimiter creates a rate limiter backed by the given Valkey client.
// failOpenFn is called each time a request is allowed through due to Valkey
// being unavailable; use it to increment the Prometheus counter.
func NewRateLimiter(client *Client, logger *slog.Logger, failOpenFn func()) *RateLimiter {
	return &RateLimiter{
		client:          client,
		logger:          logger,
		failOpenCounter: failOpenFn,
	}
}

// slidingWindowScript is a Lua script that atomically implements a sliding window
// rate limiter. It counts requests in the last `window` milliseconds and rejects
// requests beyond `limit`.
//
// KEYS[1] = rate limit key (e.g. "ratelimit:auth:192.0.2.1")
// ARGV[1] = current time in milliseconds (string)
// ARGV[2] = window size in milliseconds (string)
// ARGV[3] = max requests allowed per window (string)
//
// Returns: 1 if allowed, 0 if denied.
// Also returns the number of remaining requests in the window.
var slidingWindowScript = redis.NewScript(`
local key      = KEYS[1]
local now      = tonumber(ARGV[1])
local window   = tonumber(ARGV[2])
local limit    = tonumber(ARGV[3])
local clearBefore = now - window

-- Remove timestamps outside the window
redis.call('ZREMRANGEBYSCORE', key, '-inf', clearBefore)

-- Count current requests in window
local count = redis.call('ZCARD', key)

if count >= limit then
    return {0, 0}
end

-- Add current request timestamp (use now+count as score member uniquifier)
redis.call('ZADD', key, now, now .. '-' .. count)
redis.call('PEXPIRE', key, window)

return {1, limit - count - 1}
`)

// Allow checks whether the given identifier is within the rate limit.
//
//	key      e.g. "ratelimit:auth:192.0.2.1" or "ratelimit:session:<hash>"
//	limit    max requests per window
//	window   sliding window duration
//
// Returns (allowed=true, remaining, resetAt, nil) on success.
// Returns (allowed=true, 0, zero, nil) when Valkey is unavailable (fail-open).
func (r *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, remaining int, resetAt time.Time, err error) {
	nowMS := time.Now().UnixMilli()
	windowMS := window.Milliseconds()

	results, err := slidingWindowScript.Run(ctx, r.client.rdb,
		[]string{key},
		fmt.Sprintf("%d", nowMS),
		fmt.Sprintf("%d", windowMS),
		fmt.Sprintf("%d", limit),
	).Int64Slice()

	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, 0, time.Time{}, err
		}
		// Valkey unavailable — fail open (ADR-015).
		r.logger.Error("rate limiter Valkey error, failing open", "key", key, "err", err)
		if r.failOpenCounter != nil {
			r.failOpenCounter()
		}
		return true, 0, time.Time{}, nil
	}

	allowed = results[0] == 1
	remaining = int(results[1])
	resetAt = time.Now().Add(window)
	return allowed, remaining, resetAt, nil
}

// failureCounterScript atomically reads the current failure count at `key`
// and refuses (returns -1) if it's already at or above `limit`. Otherwise
// returns the current count. Used by the brute-force throttle's pre-auth
// gate — the *check* must not increment, because that would count successful
// logins toward the lockout. Increments happen only after a confirmed failure
// (see IncrFailure).
//
// KEYS[1] = failure counter key
// ARGV[1] = limit (string)
//
// Returns: current count, or -1 if already at limit.
var failureCounterScript = redis.NewScript(`
local key   = KEYS[1]
local limit = tonumber(ARGV[1])
local v = redis.call('GET', key)
local count = tonumber(v) or 0
if count >= limit then
    return -1
end
return count
`)

// CheckFailures returns whether the caller is under the failure cap for `key`.
// If allowed=false, the caller has hit the limit and should be rejected
// without performing the expensive operation (bcrypt compare). Fails open
// when Valkey is unavailable (matches Allow's posture).
//
// CheckFailures does NOT increment the counter. Pair it with IncrFailure on
// the failure path and ResetFailures on the success path. This split is the
// fix for the prior bug where Allow() counted successes toward the cap, so
// a user logging in `limit` times legitimately got locked out.
func (r *RateLimiter) CheckFailures(ctx context.Context, key string, limit int) (allowed bool, err error) {
	res, err := failureCounterScript.Run(ctx, r.client.rdb, []string{key}, fmt.Sprintf("%d", limit)).Int64()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		r.logger.Error("rate limiter Valkey error, failing open", "key", key, "err", err)
		if r.failOpenCounter != nil {
			r.failOpenCounter()
		}
		return true, nil
	}
	return res >= 0, nil
}

// IncrFailure increments the failure counter at `key` and (re)sets its TTL
// to `window`. The counter naturally expires after `window` of inactivity,
// so a slow drip below the cap never trips the lockout. Errors are logged
// and swallowed — failing to record a single failure shouldn't abort the
// auth response.
func (r *RateLimiter) IncrFailure(ctx context.Context, key string, window time.Duration) {
	if _, err := r.client.rdb.Incr(ctx, key).Result(); err != nil {
		r.logger.Warn("incr failure counter", "key", key, "err", err)
		return
	}
	// EXPIRE on every increment is a small over-write but keeps the
	// window sliding — the counter expires `window` after the last
	// failure rather than after the first. Same posture as a typical
	// "lockout for 15 min from your last bad attempt" UX.
	if err := r.client.rdb.Expire(ctx, key, window).Err(); err != nil {
		r.logger.Warn("expire failure counter", "key", key, "err", err)
	}
}

// ResetFailures clears the failure counter at `key`. Call on successful auth
// so a user who eventually got their password right starts fresh next time.
// Errors are logged and swallowed.
func (r *RateLimiter) ResetFailures(ctx context.Context, key string) {
	if err := r.client.rdb.Del(ctx, key).Err(); err != nil {
		r.logger.Warn("reset failure counter", "key", key, "err", err)
	}
}
