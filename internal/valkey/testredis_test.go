package valkey

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// downFlags marks the miniredis servers a test has taken down (takeDown).
var downFlags sync.Map // *miniredis.Miniredis -> *atomic.Bool

var errValkeyDown = errors.New("test: valkey is down")

// newTestRedis returns a go-redis client for mr. Its dialer refuses once
// takeDown(mr) has run. Closing mr alone isn't enough to make Valkey
// unreachable: it frees the port, and a test server in another package
// (go test runs packages in parallel) can bind it, so the "down" client
// talks to someone else's server and the test sees a working Valkey (CI,
// 2026-11-09: TestAllow_FailsOpenWhenValkeyDown got remaining=4).
func newTestRedis(t *testing.T, mr *miniredis.Miniredis) *redis.Client {
	t.Helper()
	down := new(atomic.Bool)
	downFlags.Store(mr, down)
	var d net.Dialer
	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if down.Load() {
				return nil, errValkeyDown
			}
			return d.DialContext(ctx, network, addr)
		},
	})
	t.Cleanup(func() {
		_ = rdb.Close()
		downFlags.Delete(mr)
	})
	return rdb
}

// takeDown makes mr unreachable for the clients newTestRedis made for it:
// no new connections, and closing mr drops the pooled ones.
func takeDown(mr *miniredis.Miniredis) {
	if f, ok := downFlags.Load(mr); ok {
		f.(*atomic.Bool).Store(true)
	}
	mr.Close()
}

// The CI flake, on purpose: another server takes the freed port right after
// takeDown. The limiter must still see Valkey as down and fail open.
func TestTakeDown_IgnoresAServerThatReusesThePort(t *testing.T) {
	r, mr, counter := newTestLimiter(t)
	addr := mr.Addr()
	takeDown(mr)

	thief := miniredis.NewMiniRedis()
	if err := thief.StartAddr(addr); err != nil {
		t.Skipf("couldn't rebind %s: %v", addr, err)
	}
	t.Cleanup(thief.Close)

	allowed, remaining, _, err := r.Allow(context.Background(), "k", 5, time.Second)
	if err != nil || !allowed || remaining != 0 || counter.Load() != 1 {
		t.Fatalf("want fail-open: allowed=%v remaining=%d err=%v failOpen=%d", allowed, remaining, err, counter.Load())
	}
	if thief.Exists("ratelimit:k") || len(thief.Keys()) != 0 {
		t.Fatalf("the down client wrote to the server on the reused port: keys=%v", thief.Keys())
	}
}
