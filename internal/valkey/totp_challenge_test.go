package valkey

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestChallengeGuard(t *testing.T) (*TOTPChallengeGuard, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewTOTPChallengeGuard(&Client{rdb: rdb}), mr
}

func TestTOTPChallengeGuard_SingleUse(t *testing.T) {
	g, mr := newTestChallengeGuard(t)
	ctx := context.Background()

	if ok, err := g.RedeemTOTPChallenge(ctx, "jti-1", 5*time.Minute); err != nil || !ok {
		t.Fatalf("first redeem = %v, %v; want fresh", ok, err)
	}
	if ok, err := g.RedeemTOTPChallenge(ctx, "jti-1", 5*time.Minute); err != nil || ok {
		t.Fatalf("second redeem = %v, %v; want refused", ok, err)
	}
	if ok, _ := g.RedeemTOTPChallenge(ctx, "jti-2", 5*time.Minute); !ok {
		t.Fatal("a different challenge is independent")
	}
	if ttl := mr.TTL("totp:challenge:jti-1"); ttl <= 0 || ttl > 5*time.Minute {
		t.Fatalf("redeemed-challenge key TTL = %v, want (0, 5m]", ttl)
	}
	// Once the record ages out the key is gone (the token is dead by then).
	mr.FastForward(5*time.Minute + time.Second)
	if mr.Exists("totp:challenge:jti-1") {
		t.Fatal("redeemed-challenge key must expire")
	}
}

func TestTOTPChallengeGuard_ConcurrentOneWinner(t *testing.T) {
	g, _ := newTestChallengeGuard(t)
	var wins atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if ok, err := g.RedeemTOTPChallenge(context.Background(), "jti", time.Minute); err == nil && ok {
				wins.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d concurrent redeems succeeded, want 1", wins.Load())
	}
}

func TestTOTPChallengeGuard_ErrorsWhenValkeyDown(t *testing.T) {
	g, mr := newTestChallengeGuard(t)
	mr.Close()
	if _, err := g.RedeemTOTPChallenge(context.Background(), "jti", time.Minute); err == nil {
		t.Fatal("an unreachable Valkey must surface an error so the caller falls back")
	}
}
