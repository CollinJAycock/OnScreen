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

// New connects to Valkey and verifies the connection with PING.
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
