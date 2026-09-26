package auth

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The challenge carries the user's session epoch (so verify can refuse it once
// the epoch moves) and a fresh random jti (the handle verify burns).
func TestIssueTOTPChallengeToken_CarriesEpochAndUniqueJTI(t *testing.T) {
	tm, err := NewTokenMaker(DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	uid := uuid.New()
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		tok, err := tm.IssueTOTPChallengeToken(uid, 42)
		if err != nil {
			t.Fatal(err)
		}
		c, err := tm.ValidateAccessToken(tok)
		if err != nil {
			t.Fatal(err)
		}
		if c.Purpose != "totp_challenge" || c.UserID != uid || c.SessionEpoch != 42 {
			t.Fatalf("claims = %+v", c)
		}
		if c.TokenID == "" || seen[c.TokenID] {
			t.Fatalf("jti %q missing or reused", c.TokenID)
		}
		seen[c.TokenID] = true
	}
	// Only the challenge carries a jti.
	at, _ := tm.IssueAccessToken(Claims{UserID: uid})
	if c, _ := tm.ValidateAccessToken(at); c.TokenID != "" {
		t.Fatalf("access token has jti %q", c.TokenID)
	}
}

func TestMemoryTOTPChallengeGuard_SingleUseUntilExpiry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := &MemoryTOTPChallengeGuard{now: func() time.Time { return now }}
	ctx := context.Background()

	if ok, _ := g.RedeemTOTPChallenge(ctx, "a", time.Minute); !ok {
		t.Fatal("first redeem must be fresh")
	}
	if ok, _ := g.RedeemTOTPChallenge(ctx, "a", time.Minute); ok {
		t.Fatal("second redeem of the same jti must be refused")
	}
	if ok, _ := g.RedeemTOTPChallenge(ctx, "b", time.Minute); !ok {
		t.Fatal("a different jti is independent")
	}
	// Past the record's TTL (the token itself is long dead by then) the entry
	// is swept, so memory stays bounded.
	now = now.Add(TOTPChallengeTTL + time.Minute)
	if ok, _ := g.RedeemTOTPChallenge(ctx, "c", time.Minute); !ok {
		t.Fatal("fresh jti after sweep")
	}
	g.mu.Lock()
	_, stillA := g.spent["a"]
	g.mu.Unlock()
	if stillA {
		t.Fatal("expired record was not swept")
	}
}

func TestMemoryTOTPChallengeGuard_ConcurrentOneWinner(t *testing.T) {
	var g MemoryTOTPChallengeGuard // zero value must be usable
	var wins atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if ok, _ := g.RedeemTOTPChallenge(context.Background(), "jti", time.Minute); ok {
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
