package v1

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/watchlimit"
)

// watchLimitMemoTTL is how long a policy/usage read is reused. Short enough
// that a limit change or a day rollover takes effect within seconds, long
// enough that a range-heavy direct play costs one round trip rather than one
// per request.
//
// Being up to this stale can only ever let a restricted profile watch a few
// extra seconds past its cap — the accounting itself (AddTick) is never memoed,
// so nothing is under-counted, and the next read inside the window still blocks.
const watchLimitMemoTTL = 10 * time.Second

// watchLimitMemo wraps an ItemWatchLimit with a short per-user cache of the two
// READ calls the gate makes.
//
// The gate has to run on every byte-serving request: the alternative — only
// checking requests that look like a fresh playback open — is exactly the hole
// that let `Range: bytes=1-` stream a whole file ungated. But GetPolicy is a DB
// round trip, and unrestricted users pay it too (Restricted() is only consulted
// after the read), so gating unconditionally without a memo would put Postgres
// in the path of every range request on a large file.
//
// AddTick is deliberately NOT memoed — it is the write that accumulates usage,
// and dropping ticks would under-count watch time.
type watchLimitMemo struct {
	inner ItemWatchLimit

	mu      sync.Mutex
	policy  map[uuid.UUID]policyEntry
	usage   map[uuid.UUID]usageEntry
	nowFunc func() time.Time // overridable in tests
}

type policyEntry struct {
	val watchlimit.Policy
	at  time.Time
}

type usageEntry struct {
	day  time.Time
	secs int
	at   time.Time
}

// newWatchLimitMemo wraps inner. A nil inner returns nil so the "no limits
// configured" path stays a nil interface rather than a non-nil wrapper around
// nothing — watchLimitBlocks tests `wl == nil` to mean "feature off".
func newWatchLimitMemo(inner ItemWatchLimit) ItemWatchLimit {
	if inner == nil {
		return nil
	}
	return &watchLimitMemo{
		inner:  inner,
		policy: map[uuid.UUID]policyEntry{},
		usage:  map[uuid.UUID]usageEntry{},
	}
}

func (m *watchLimitMemo) now() time.Time {
	if m.nowFunc != nil {
		return m.nowFunc()
	}
	return time.Now()
}

func (m *watchLimitMemo) GetPolicy(ctx context.Context, userID uuid.UUID) (watchlimit.Policy, error) {
	now := m.now()
	m.mu.Lock()
	if e, ok := m.policy[userID]; ok && now.Sub(e.at) < watchLimitMemoTTL {
		m.mu.Unlock()
		return e.val, nil
	}
	m.mu.Unlock()

	// Fetched outside the lock: a slow DB must not serialize every other user's
	// gate behind this one. A concurrent duplicate fetch is harmless.
	p, err := m.inner.GetPolicy(ctx, userID)
	if err != nil {
		return p, err
	}
	m.mu.Lock()
	m.policy[userID] = policyEntry{val: p, at: now}
	m.mu.Unlock()
	return p, nil
}

func (m *watchLimitMemo) TodayUsageSeconds(ctx context.Context, userID uuid.UUID, day time.Time) (int, error) {
	now := m.now()
	m.mu.Lock()
	// The day is part of the key, not just the value: a cached entry from
	// yesterday must not satisfy today's read across a local-midnight rollover.
	if e, ok := m.usage[userID]; ok && e.day.Equal(day) && now.Sub(e.at) < watchLimitMemoTTL {
		m.mu.Unlock()
		return e.secs, nil
	}
	m.mu.Unlock()

	secs, err := m.inner.TodayUsageSeconds(ctx, userID, day)
	if err != nil {
		return secs, err
	}
	m.mu.Lock()
	m.usage[userID] = usageEntry{day: day, secs: secs, at: now}
	m.mu.Unlock()
	return secs, nil
}

// AddTick passes straight through and invalidates this user's cached usage, so
// the accumulated total the gate sees advances with the write rather than
// lagging it by the TTL.
func (m *watchLimitMemo) AddTick(ctx context.Context, userID uuid.UUID, day, now time.Time) (int, error) {
	total, err := m.inner.AddTick(ctx, userID, day, now)
	m.mu.Lock()
	delete(m.usage, userID)
	m.mu.Unlock()
	return total, err
}
