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
	client  *Client
	logger  *slog.Logger
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
//   key      e.g. "ratelimit:auth:192.0.2.1" or "ratelimit:session:<hash>"
//   limit    max requests per window
//   window   sliding window duration
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
