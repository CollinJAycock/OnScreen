package v1

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/watchlimit"
)

// usageAccrueInterval is how often a byte-serving path writes a usage tick per
// user. AddTick adds the CLAMPED WALL-CLOCK DELTA since the user's previous
// tick (whoever wrote it), so ticking from several surfaces at once cannot
// double-count — a second tick moments after the first adds only the moments.
// The throttle exists purely to keep DB writes off the per-request hot path.
var usageAccrueInterval = 15 * time.Second

// usageAccruer accrues parental watch-limit usage from SERVER-side playback
// signals — segment fetches and media byte reads — rather than trusting the
// client's progress beacon.
//
// The beacon already accrues (see the Progress handler), but it is
// client-driven: a client that simply doesn't send it consumed direct-play
// and live-TV bytes without a second of usage recorded, making
// daily_limit_minutes decorative for exactly the clients most likely to be
// configured that way. Byte-serving is the one signal the server always sees.
//
// It is an approximation: a client that aggressively prefetches accrues while
// fetching, not while watching, and one that buffers a whole file up front
// still under-accrues the tail. The allowed-hours gate on every serving path
// (watchLimitBlocks) stays the hard stop; this closes the accounting half for
// every client that streams roughly as it plays.
type usageAccruer struct {
	wl ItemWatchLimit

	mu   sync.Mutex
	last map[uuid.UUID]time.Time
}

func newUsageAccruer(wl ItemWatchLimit) *usageAccruer {
	if wl == nil {
		return nil
	}
	return &usageAccruer{wl: wl, last: map[uuid.UUID]time.Time{}}
}

// Tick records usage for userID if the per-user throttle window has passed.
// Only RESTRICTED users are ticked — the policy read is memoized upstream, so
// unrestricted users cost a map lookup. Safe on a nil receiver.
func (a *usageAccruer) Tick(ctx context.Context, logger *slog.Logger, userID uuid.UUID) {
	if a == nil {
		return
	}
	now := time.Now()
	a.mu.Lock()
	if last, ok := a.last[userID]; ok && now.Sub(last) < usageAccrueInterval {
		a.mu.Unlock()
		return
	}
	a.last[userID] = now
	a.mu.Unlock()

	policy, err := a.wl.GetPolicy(ctx, userID)
	if err != nil || !policy.Restricted() {
		return
	}
	if _, err := a.wl.AddTick(ctx, userID, watchlimit.LocalDay(now), now); err != nil && logger != nil {
		logger.WarnContext(ctx, "watch-limit: accrue from byte serving", "user_id", userID, "err", err)
	}
}
