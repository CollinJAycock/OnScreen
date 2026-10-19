package plugin

import (
	"sync"
	"time"
)

// breaker is the per-plugin circuit breaker every role shares: after
// breakerThreshold consecutive failures it opens for breakerCooldown, and
// calls made while it is open are skipped. The first call after the cooldown
// is the probe — success closes the breaker, and a failure re-opens it at once
// because the failure count was never reset.
//
// Safe for concurrent use: the notification worker calls it from one
// goroutine, but metadata enrichment runs up to four items at a time and a
// task can be started by the scheduler and by "Run now" together.
type breaker struct {
	mu          sync.Mutex
	consecFails int
	openUntil   time.Time
	// now is swappable so tests can step over the cooldown.
	now func() time.Time
}

func newBreaker() *breaker {
	return &breaker{now: time.Now}
}

// allow reports whether a call may go out now, and when not, until when the
// breaker stays open.
func (b *breaker) allow() (bool, time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.openUntil.IsZero() && b.now().Before(b.openUntil) {
		return false, b.openUntil
	}
	return true, time.Time{}
}

// success records a good call. Returns true when that closed a breaker that
// had failures on record, so the caller can audit the recovery once.
func (b *breaker) success() (closed bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	closed = b.consecFails > 0 || !b.openUntil.IsZero()
	b.consecFails = 0
	b.openUntil = time.Time{}
	return closed
}

// failure records a failed call. Returns true when it (re-)opened the
// breaker, with the new open-until time.
func (b *breaker) failure() (opened bool, until time.Time, fails int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.consecFails++
	if b.consecFails >= breakerThreshold {
		b.openUntil = b.now().Add(breakerCooldown)
		return true, b.openUntil, b.consecFails
	}
	return false, time.Time{}, b.consecFails
}

// state returns the failure count and open-until time (zero when closed).
// For tests and diagnostics.
func (b *breaker) state() (consecFails int, openUntil time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.consecFails, b.openUntil
}
