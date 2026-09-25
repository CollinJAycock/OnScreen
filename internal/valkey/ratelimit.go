package valkey

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	failOpenCounter func()           // increments onscreen_ratelimit_failopen_total
	now             func() time.Time // clock for failure reservations; nil = time.Now (tests override)
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
	return r.allow(ctx, key, limit, window, false)
}

// AllowFailClosed is like Allow but, when Valkey is unreachable, returns the
// error (which the middleware maps to 503) instead of failing open. Operators
// opt into this on the credential paths via OS_AUTH_RATE_LIMIT_FAIL_CLOSED so a
// Valkey outage can't silently disable brute-force protection — at the cost of
// rejecting auth attempts while the backend is down. Default stays fail-open
// (ADR-015) for every other endpoint.
func (r *RateLimiter) AllowFailClosed(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, remaining int, resetAt time.Time, err error) {
	return r.allow(ctx, key, limit, window, true)
}

func (r *RateLimiter) allow(ctx context.Context, key string, limit int, window time.Duration, failClosed bool) (allowed bool, remaining int, resetAt time.Time, err error) {
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
		if failClosed {
			// Surface the error so the caller rejects (503) rather than
			// granting a free pass while the backend is down.
			r.logger.Error("rate limiter Valkey error, failing closed", "key", key, "err", err)
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

// failurePendingTTL bounds how long an in-flight attempt's reservation (see
// CheckFailures) holds a slot. It only has to outlast one credential check —
// a bcrypt compare, an LDAP bind — and it is what frees the slot when an
// attempt ends in neither IncrFailure nor ResetFailures (a DB/LDAP outage
// error path). Generous on purpose: an attempt slower than this merely
// stops being counted as in flight; it is still counted once it fails.
const failurePendingTTL = 60 * time.Second

// failurePendingKey is the sorted set of in-flight reservations that sits
// next to the committed failure counter at `key`.
func failurePendingKey(key string) string { return key + ":pending" }

// failureReserveScript is the brute-force throttle's pre-auth gate. In ONE
// atomic step it counts committed failures plus live in-flight reservations
// and, if that total is under `limit`, reserves a slot for the caller.
//
// The previous gate only READ the committed counter and the failure path
// incremented it later, after the (slow, deliberately so) credential check.
// That check-then-increment split let a concurrent burst all read the same
// under-cap count before any of them incremented, so N parallel guesses got
// N tries against a cap of `limit`. Reserving inside the same script that
// compares means N concurrent attempts consume N slots; the (limit+1)th sees
// the cap and is refused.
//
// A reservation is not a failure: a successful attempt clears everything via
// ResetFailures, a failed one converts its reservation into a committed
// failure via IncrFailure, and an attempt that does neither (an infra error)
// simply ages out after failurePendingTTL. So a legitimate user logging in
// `limit` times is still never locked out.
//
// KEYS[1] = committed failure counter
// KEYS[2] = in-flight reservation zset (score = reservation expiry, ms)
// ARGV[1] = limit
// ARGV[2] = now (ms)
// ARGV[3] = reservation TTL (ms)
// ARGV[4] = unique reservation member
//
// Returns: slots in use before this reservation, or -1 if refused.
var failureReserveScript = redis.NewScript(`
local limit = tonumber(ARGV[1])
local now   = tonumber(ARGV[2])
local ttl   = tonumber(ARGV[3])
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
local failures = tonumber(redis.call('GET', KEYS[1])) or 0
local pending  = redis.call('ZCARD', KEYS[2])
if failures + pending >= limit then
    return -1
end
redis.call('ZADD', KEYS[2], now + ttl, ARGV[4])
redis.call('PEXPIRE', KEYS[2], ttl)
return failures + pending
`)

// failureCommitScript records a confirmed failure: INCR + (re)arm the TTL in
// one step — the old separate INCR and EXPIRE round trips could leave a
// counter with NO expiry (permanent lockout) if the second call failed or the
// process died between them — and retire one in-flight reservation, which is
// the one this failure was holding.
//
// KEYS[1] = committed failure counter
// KEYS[2] = in-flight reservation zset
// ARGV[1] = window (ms)
var failureCommitScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
redis.call('PEXPIRE', KEYS[1], ARGV[1])
redis.call('ZPOPMIN', KEYS[2])
return n
`)

// CheckFailures reports whether the caller is under the failure cap for `key`
// and, when it is, atomically RESERVES one slot for the attempt about to run.
// If allowed=false, the caller has hit the limit and should be rejected
// without performing the expensive operation (bcrypt compare). Fails open
// when Valkey is unavailable (matches Allow's posture).
//
// Pair it with IncrFailure on the failure path and ResetFailures on the
// success path, exactly as before — the reservation is what closes the
// check-then-increment race (see failureReserveScript) without counting
// successes toward the lockout.
func (r *RateLimiter) CheckFailures(ctx context.Context, key string, limit int) (allowed bool, err error) {
	member, err := reservationID()
	if err != nil {
		// crypto/rand failing is not a Valkey outage; don't hand out a free
		// pass on it — refuse this one attempt.
		r.logger.Error("rate limiter: reservation id", "key", key, "err", err)
		return false, err
	}
	res, err := failureReserveScript.Run(ctx, r.client.rdb,
		[]string{key, failurePendingKey(key)},
		fmt.Sprintf("%d", limit),
		fmt.Sprintf("%d", r.nowMS()),
		fmt.Sprintf("%d", failurePendingTTL.Milliseconds()),
		member,
	).Int64()
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

// IncrFailure records a confirmed failure at `key`: increments the counter,
// (re)sets its TTL to `window`, and retires the attempt's in-flight
// reservation — all atomically. The counter expires `window` after the LAST
// failure, so a slow drip below the cap never trips the lockout. Errors are
// logged and swallowed — failing to record a single failure shouldn't abort
// the auth response.
func (r *RateLimiter) IncrFailure(ctx context.Context, key string, window time.Duration) {
	if err := failureCommitScript.Run(ctx, r.client.rdb,
		[]string{key, failurePendingKey(key)},
		fmt.Sprintf("%d", window.Milliseconds()),
	).Err(); err != nil {
		r.logger.Warn("incr failure counter", "key", key, "err", err)
	}
}

// ResetFailures clears the failure counter at `key` (and any in-flight
// reservations). Call on successful auth so a user who eventually got their
// password right starts fresh next time. Errors are logged and swallowed.
func (r *RateLimiter) ResetFailures(ctx context.Context, key string) {
	if err := r.client.rdb.Del(ctx, key, failurePendingKey(key)).Err(); err != nil {
		r.logger.Warn("reset failure counter", "key", key, "err", err)
	}
}

func (r *RateLimiter) nowMS() int64 {
	if r.now != nil {
		return r.now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

// reservationID returns a random member name so concurrent reservations never
// collapse into one zset entry.
func reservationID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
