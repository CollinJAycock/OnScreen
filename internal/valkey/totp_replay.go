package valkey

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// totpStepTTL is how long the last-consumed TOTP step is remembered. A code is
// accepted for at most its own step ±1 (the skew window), so once four periods
// have passed every step that could still validate is strictly newer than the
// remembered one and the record can go.
const totpStepTTL = 4 * 30 * time.Second

// consumeStepScript atomically enforces "strictly newer than the last consumed
// step": it refuses a step <= the stored one, otherwise stores it (with TTL).
// The compare and the write are one script so two concurrent submissions of
// the same code cannot both see "not yet used".
//
// KEYS[1] = per-user last-step key
// ARGV[1] = step (decimal)
// ARGV[2] = TTL (ms)
//
// Returns 1 if the step was fresh (and is now consumed), 0 if replayed.
var consumeStepScript = redis.NewScript(`
local last = tonumber(redis.call('GET', KEYS[1]))
local step = tonumber(ARGV[1])
if last and step <= last then
    return 0
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return 1
`)

// TOTPReplayGuard is the shared (multi-instance) record of consumed TOTP time
// steps; it satisfies auth.TOTPReplayGuard. Keyed per user, no DB migration.
type TOTPReplayGuard struct {
	c *Client
}

// NewTOTPReplayGuard returns a guard backed by the given Valkey client.
func NewTOTPReplayGuard(c *Client) *TOTPReplayGuard { return &TOTPReplayGuard{c: c} }

// ConsumeTOTPStep records step as consumed for userID and reports whether it
// was fresh, i.e. strictly newer than any step this user already spent.
func (g *TOTPReplayGuard) ConsumeTOTPStep(ctx context.Context, userID uuid.UUID, step int64) (bool, error) {
	n, err := consumeStepScript.Run(ctx, g.c.rdb,
		[]string{"totp:last_step:" + userID.String()},
		fmt.Sprintf("%d", step),
		fmt.Sprintf("%d", totpStepTTL.Milliseconds()),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("totp replay guard: %w", err)
	}
	return n == 1, nil
}
