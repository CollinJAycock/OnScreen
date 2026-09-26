package auth

import (
	"context"
	"sync"
	"time"
)

// TOTPChallengeGuard records redeemed TOTP login challenges by jti.
// RedeemTOTPChallenge must atomically mark the jti spent for ttl and report
// whether this call was the first to do so — no check-then-set window in which
// two concurrent verifies of one challenge both mint a session.
//
// Implemented by valkey.TOTPChallengeGuard (shared across instances) and
// MemoryTOTPChallengeGuard (per process; fallback when Valkey is unreachable).
type TOTPChallengeGuard interface {
	RedeemTOTPChallenge(ctx context.Context, jti string, ttl time.Duration) (fresh bool, err error)
}

// MemoryTOTPChallengeGuard is an in-process TOTPChallengeGuard: a
// mutex-protected map of jti → expiry. The zero value is ready to use.
type MemoryTOTPChallengeGuard struct {
	mu        sync.Mutex
	spent     map[string]time.Time
	lastSweep time.Time
	now       func() time.Time // nil = time.Now (tests override)
}

// RedeemTOTPChallenge implements TOTPChallengeGuard. Never returns an error.
func (g *MemoryTOTPChallengeGuard) RedeemTOTPChallenge(_ context.Context, jti string, ttl time.Duration) (bool, error) {
	now := time.Now()
	if g.now != nil {
		now = g.now()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.spent == nil {
		g.spent = make(map[string]time.Time)
	}
	// Bound memory: sweep expired entries at most once per challenge TTL
	// (no record outlives a challenge by more than that).
	if now.Sub(g.lastSweep) >= TOTPChallengeTTL {
		for k, exp := range g.spent {
			if !now.Before(exp) {
				delete(g.spent, k)
			}
		}
		g.lastSweep = now
	}
	if exp, ok := g.spent[jti]; ok && now.Before(exp) {
		return false, nil
	}
	g.spent[jti] = now.Add(ttl)
	return true, nil
}
