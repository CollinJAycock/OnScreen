// Package valkey wraps go-redis to provide the Valkey client used across
// OnScreen. Valkey is wire-compatible with Redis; any Redis v7+ client works.
package valkey

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client is the shared Valkey client. Embed or inject this into services that
// need cache, pub/sub, or distributed locks.
type Client struct {
	rdb *redis.Client
}

// New connects to a single Valkey node and verifies the connection with PING.
func New(ctx context.Context, url string) (*Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse valkey URL: %w", err)
	}

	rdb := redis.NewClient(opts)

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		return nil, fmt.Errorf("valkey ping: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

// NewFailover connects via Valkey/Redis Sentinel for automatic master failover
// (HA). The Sentinels track the current master and the client transparently
// reconnects to the promoted node after a failover — so the leader lock,
// sessions, dispatch counters, and caches survive losing a Valkey node.
//
// redisURL supplies the data-node auth/db/TLS (its host is ignored — Sentinel
// discovers the actual master), keeping one source for credentials whether a
// deployment runs single-node or Sentinel. NewFailoverClient returns the same
// *redis.Client type as the single-node path, so everything downstream (Raw(),
// pipelines, Lua, the master lock) is unchanged.
func NewFailover(ctx context.Context, masterName string, sentinelAddrs []string, sentinelPassword, redisURL string) (*Client, error) {
	if masterName == "" {
		return nil, fmt.Errorf("valkey sentinel: VALKEY_SENTINEL_MASTER required")
	}
	if len(sentinelAddrs) == 0 {
		return nil, fmt.Errorf("valkey sentinel: VALKEY_SENTINEL_ADDRS required")
	}
	base, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse valkey URL (auth/db for sentinel): %w", err)
	}

	rdb := redis.NewFailoverClient(&redis.FailoverOptions{
		MasterName:       masterName,
		SentinelAddrs:    sentinelAddrs,
		SentinelPassword: sentinelPassword,
		Username:         base.Username,
		Password:         base.Password,
		DB:               base.DB,
		TLSConfig:        base.TLSConfig,
	})

	// Slightly longer than the single-node ping: a fresh client first asks a
	// Sentinel for the master address before it can PING the master.
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		return nil, fmt.Errorf("valkey sentinel ping: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

// Ping checks the connection. Used by the health handler.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// Close closes the underlying connection pool.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// Raw returns the underlying go-redis client for callers that need
// operations not wrapped here (e.g. pub/sub, Lua scripts).
func (c *Client) Raw() *redis.Client {
	return c.rdb
}

// Set sets key to value with an expiry.
func (c *Client) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

// Get returns the string value for key. Returns ("", redis.Nil) if the key
// does not exist — callers should check errors.Is(err, redis.Nil).
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	return c.rdb.Get(ctx, key).Result()
}

// Del deletes one or more keys.
func (c *Client) Del(ctx context.Context, keys ...string) error {
	return c.rdb.Del(ctx, keys...).Err()
}

// SetNX sets key to value only if the key does not already exist.
// Returns true if the key was set.
func (c *Client) SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, key, value, ttl).Result()
}

// Expire resets the TTL on an existing key.
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return c.rdb.Expire(ctx, key, ttl).Err()
}

// casDelete deletes key only if its current value equals expected.
var casDelete = redis.NewScript(`
	if redis.call("GET", KEYS[1]) == ARGV[1] then
		return redis.call("DEL", KEYS[1])
	end
	return 0
`)

// casExpire resets key's TTL only if its current value equals expected.
var casExpire = redis.NewScript(`
	if redis.call("GET", KEYS[1]) == ARGV[1] then
		return redis.call("PEXPIRE", KEYS[1], ARGV[2])
	end
	return 0
`)

// CompareAndDelete deletes key only if it currently holds expected, and reports
// whether it did.
//
// A plain Del on a lease key is unsafe: between deciding to release and issuing
// the delete, the lease can expire and be re-acquired by another instance, and
// the delete then evicts THAT holder's lease. The check and the delete must be
// one atomic step, which is what the script gives us.
func (c *Client) CompareAndDelete(ctx context.Context, key, expected string) (bool, error) {
	n, err := casDelete.Run(ctx, c.rdb, []string{key}, expected).Int64()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// CompareAndExpire extends key's TTL only if it currently holds expected, and
// reports whether it did.
//
// The GET-then-EXPIRE pair it replaces was a read-modify-write race: the key
// could expire and be re-acquired between the two calls, and the EXPIRE would
// then extend the NEW owner's lease on the old owner's behalf — both instances
// believing they hold it.
func (c *Client) CompareAndExpire(ctx context.Context, key, expected string, ttl time.Duration) (bool, error) {
	n, err := casExpire.Run(ctx, c.rdb, []string{key}, expected, ttl.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// SAdd adds members to a set. Used by the segment-token manager to maintain a
// user → tokens index so revocation on password reset / admin demote can wipe
// every outstanding token for a user.
func (c *Client) SAdd(ctx context.Context, key string, members ...any) error {
	return c.rdb.SAdd(ctx, key, members...).Err()
}

// SRem removes members from a set.
func (c *Client) SRem(ctx context.Context, key string, members ...any) error {
	return c.rdb.SRem(ctx, key, members...).Err()
}

// SMembers returns all members of a set. Returns an empty slice (not an error)
// when the key doesn't exist.
func (c *Client) SMembers(ctx context.Context, key string) ([]string, error) {
	return c.rdb.SMembers(ctx, key).Result()
}

// Scan iterates over keys matching pattern using cursor-based SCAN (non-blocking).
// This is O(1) per call instead of the O(n) KEYS command which blocks the server.
func (c *Client) Scan(ctx context.Context, pattern string) ([]string, error) {
	var keys []string
	var cursor uint64
	for {
		batch, next, err := c.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return keys, nil
}

// ErrNotFound is returned by Get when the key does not exist in Valkey.
var ErrNotFound = redis.Nil
