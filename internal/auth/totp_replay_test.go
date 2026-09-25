package auth

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
)

func TestMatchTOTPStep_WindowAndStep(t *testing.T) {
	secret, _, err := GenerateTOTPSecret("alice")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_010, 0) // mid-step, away from a boundary
	cur := now.Unix() / 30

	for off := int64(-1); off <= 1; off++ {
		code, _ := totp.GenerateCode(secret, now.Add(time.Duration(off)*30*time.Second))
		step, ok := MatchTOTPStep(code, secret, now)
		if !ok || step != cur+off {
			t.Errorf("offset %d: MatchTOTPStep = (%d,%v), want (%d,true)", off, step, ok, cur+off)
		}
	}
	for _, off := range []int64{-3, 3} {
		code, _ := totp.GenerateCode(secret, now.Add(time.Duration(off)*30*time.Second))
		if code == mustCode(t, secret, now) {
			continue // astronomically unlikely collision
		}
		if _, ok := MatchTOTPStep(code, secret, now); ok {
			t.Errorf("offset %d is outside the ±1 skew window but matched", off)
		}
	}
	if _, ok := MatchTOTPStep("12345", secret, now); ok {
		t.Error("5-digit input must not match")
	}
	if _, ok := MatchTOTPStep(" "+mustCode(t, secret, now)+" ", secret, now); !ok {
		t.Error("surrounding whitespace should be tolerated like ValidateTOTPCode")
	}
}

func mustCode(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMemoryTOTPReplayGuard_RefusesReplay(t *testing.T) {
	var g MemoryTOTPReplayGuard // zero value must be usable
	ctx := context.Background()
	u := uuid.New()

	steps := []struct {
		step int64
		want bool
	}{{100, true}, {100, false}, {99, false}, {101, true}, {101, false}}
	for _, s := range steps {
		if got, _ := g.ConsumeTOTPStep(ctx, u, s.step); got != s.want {
			t.Fatalf("ConsumeTOTPStep(%d) = %v, want %v", s.step, got, s.want)
		}
	}
	if ok, _ := g.ConsumeTOTPStep(ctx, uuid.New(), 100); !ok {
		t.Fatal("steps are per user")
	}
}

func TestMemoryTOTPReplayGuard_ExpiresAndSweeps(t *testing.T) {
	now := time.Now()
	g := &MemoryTOTPReplayGuard{now: func() time.Time { return now }}
	ctx := context.Background()
	u := uuid.New()
	if ok, _ := g.ConsumeTOTPStep(ctx, u, 10); !ok {
		t.Fatal("first use must be fresh")
	}
	now = now.Add(memoryStepTTL + time.Second)
	// After the TTL every step that can still validate is newer anyway; the
	// record is dropped so the map can't grow without bound.
	if ok, _ := g.ConsumeTOTPStep(ctx, uuid.New(), 1); !ok {
		t.Fatal("unrelated user must be fresh")
	}
	g.mu.Lock()
	_, still := g.last[u]
	g.mu.Unlock()
	if still {
		t.Fatal("expired entry was not swept")
	}
}

func TestMemoryTOTPReplayGuard_ConcurrentOneWinner(t *testing.T) {
	var g MemoryTOTPReplayGuard
	u := uuid.New()
	var wins atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := g.ConsumeTOTPStep(context.Background(), u, 7); ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d concurrent uses of one step succeeded, want 1", wins.Load())
	}
}
