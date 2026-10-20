package valkey

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrLockTimeout is returned by Lock when another holder kept the key for the
// whole wait budget.
var ErrLockTimeout = errors.New("valkey lock: wait budget exhausted")

// lockPollInterval is how often Lock retries a contended key.
const lockPollInterval = 50 * time.Millisecond

// Lease is a held distributed lock: a key SET NX PX to a random per-holder
// token. Release deletes the key only while it still holds that token
// (CompareAndDelete), so a holder whose lease expired can never evict the next
// holder. While held, a background goroutine extends the TTL every ttl/3
// (CompareAndExpire), so a critical section that runs longer than one TTL
// keeps the lock; a holder that crashes stops renewing and the key expires
// after at most one TTL.
type Lease struct {
	c     *Client
	key   string
	token string

	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// TryLock makes one attempt to take key for ttl. It returns (nil, false, nil)
// when someone else holds the key.
func (c *Client) TryLock(ctx context.Context, key string, ttl time.Duration) (*Lease, bool, error) {
	token, err := lockToken()
	if err != nil {
		return nil, false, err
	}
	ok, err := c.SetNX(ctx, key, token, ttl)
	if err != nil || !ok {
		return nil, false, err
	}
	l := &Lease{c: c, key: key, token: token, stop: make(chan struct{}), done: make(chan struct{})}
	go l.renew(ttl)
	return l, true, nil
}

// Lock takes key for ttl, polling for up to wait while another holder has it.
// It returns ErrLockTimeout when the budget runs out, ctx.Err() when ctx ends
// first, and the Valkey error when the server can't be reached (callers decide
// whether to fail open).
func (c *Client) Lock(ctx context.Context, key string, ttl, wait time.Duration) (*Lease, error) {
	deadline := time.Now().Add(wait)
	for {
		l, ok, err := c.TryLock(ctx, key, ttl)
		if err != nil {
			return nil, err
		}
		if ok {
			return l, nil
		}
		if !time.Now().Before(deadline) {
			return nil, ErrLockTimeout
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

// Release stops renewal and deletes the key if this lease still owns it. It
// is safe to call more than once; later calls are no-ops.
func (l *Lease) Release(ctx context.Context) error {
	var err error
	l.once.Do(func() {
		close(l.stop)
		<-l.done
		_, err = l.c.CompareAndDelete(ctx, l.key, l.token)
	})
	return err
}

// renew extends the lease every ttl/3 until Release, or until the key no longer
// holds our token (expired and re-taken, or deleted by hand) — renewing someone
// else's lease is the split-brain the token exists to prevent.
func (l *Lease) renew(ttl time.Duration) {
	defer close(l.done)
	every := ttl / 3
	if every <= 0 {
		every = ttl
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), every)
			ok, err := l.c.CompareAndExpire(ctx, l.key, l.token, ttl)
			cancel()
			if err == nil && !ok {
				return // lost the key; nothing left to renew
			}
			// A transient error is retried on the next tick; the TTL still
			// bounds how long the key outlives us if Valkey stays unreachable.
		}
	}
}

func lockToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("lock token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
