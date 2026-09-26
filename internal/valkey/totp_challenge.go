package valkey

import (
	"context"
	"fmt"
	"time"
)

// TOTPChallengeGuard is the shared (multi-instance) record of redeemed TOTP
// login challenges; it satisfies auth.TOTPChallengeGuard. Keyed per challenge
// jti, no DB migration.
type TOTPChallengeGuard struct {
	c *Client
}

// NewTOTPChallengeGuard returns a guard backed by the given Valkey client.
func NewTOTPChallengeGuard(c *Client) *TOTPChallengeGuard { return &TOTPChallengeGuard{c: c} }

// RedeemTOTPChallenge marks jti redeemed for ttl (SET NX, so the check and the
// write are one atomic step) and reports whether this call was the first.
func (g *TOTPChallengeGuard) RedeemTOTPChallenge(ctx context.Context, jti string, ttl time.Duration) (bool, error) {
	fresh, err := g.c.SetNX(ctx, "totp:challenge:"+jti, "1", ttl)
	if err != nil {
		return false, fmt.Errorf("totp challenge guard: %w", err)
	}
	return fresh, nil
}
