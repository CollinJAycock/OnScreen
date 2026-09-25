package valkey

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func newTestReplayGuard(t *testing.T) (*TOTPReplayGuard, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewTOTPReplayGuard(&Client{rdb: rdb}), mr
}

func TestTOTPReplayGuard_RefusesSameOrOlderStep(t *testing.T) {
	g, mr := newTestReplayGuard(t)
	ctx := context.Background()
	u := uuid.New()

	mustConsume := func(step int64, want bool) {
		t.Helper()
		got, err := g.ConsumeTOTPStep(ctx, u, step)
		if err != nil {
			t.Fatalf("ConsumeTOTPStep(%d): %v", step, err)
		}
		if got != want {
			t.Fatalf("ConsumeTOTPStep(%d) = %v, want %v", step, got, want)
		}
	}
	mustConsume(100, true)
	mustConsume(100, false) // replay of the same code
	mustConsume(99, false)  // an older code still inside the skew window
	mustConsume(101, true)  // the next code

	if ttl := mr.TTL("totp:last_step:" + u.String()); ttl <= 0 {
		t.Fatalf("last-step key must expire, TTL = %v", ttl)
	}

	// Per user: another account's step is independent.
	if ok, _ := g.ConsumeTOTPStep(ctx, uuid.New(), 100); !ok {
		t.Fatal("a different user's step 100 must be fresh")
	}
}

// Two concurrent submissions of one code: exactly one may win.
func TestTOTPReplayGuard_ConcurrentSameStepOneWinner(t *testing.T) {
	g, _ := newTestReplayGuard(t)
	ctx := context.Background()
	u := uuid.New()

	var wins atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if ok, err := g.ConsumeTOTPStep(ctx, u, 555); err == nil && ok {
				wins.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d concurrent submissions of one code succeeded, want 1", wins.Load())
	}
}

func TestTOTPReplayGuard_ErrorsWhenValkeyDown(t *testing.T) {
	g, mr := newTestReplayGuard(t)
	mr.Close()
	if _, err := g.ConsumeTOTPStep(context.Background(), uuid.New(), 1); err == nil {
		t.Fatal("an unreachable Valkey must surface an error so the caller falls back")
	}
}
