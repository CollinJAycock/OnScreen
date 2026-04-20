package worker

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/onscreen/onscreen/internal/valkey"
)

const (
	masterKey     = "master:lock"
	masterTTL     = 15 * time.Second
	masterRefresh = 5 * time.Second
)

// MasterLock implements a Valkey-backed leader election so that only one
// instance runs singleton background workers (hub refresh, partition
// maintenance, periodic library scans). Any instance can become master if
// the current master disappears (TTL expires on crash/restart).
type MasterLock struct {
	v               *valkey.Client
	id              string // unique per-process ID
	logger          *slog.Logger
	held            atomic.Bool
	refreshInterval time.Duration // how often Run ticks; overridden in tests
	checkInterval   time.Duration // how often RunIfMaster checks; overridden in tests
}

// NewMasterLock creates a MasterLock. instanceID must be unique per process
// (e.g. a UUID generated at startup).
func NewMasterLock(v *valkey.Client, instanceID string, logger *slog.Logger) *MasterLock {
	return &MasterLock{
		v:               v,
		id:              instanceID,
		logger:          logger,
		refreshInterval: masterRefresh,
		checkInterval:   2 * time.Second,
	}
}

// IsMaster reports whether this instance currently holds the master lock.
func (m *MasterLock) IsMaster() bool { return m.held.Load() }

// Run maintains the master lock until ctx is cancelled. It tries to acquire
// the lock immediately and re-tries every masterRefresh seconds. The lock is
// released cleanly on context cancellation so another instance can take over
// without waiting for the TTL to expire.
func (m *MasterLock) Run(ctx context.Context) {
	m.tryAcquire(ctx)

	ticker := time.NewTicker(m.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if m.held.Load() {
				// Best-effort release so another instance takes over immediately.
				_ = m.v.Del(context.Background(), masterKey)
				m.held.Store(false)
				m.logger.Info("master lock released", "id", m.id)
			}
			return
		case <-ticker.C:
			if m.held.Load() {
				m.tryRefresh(ctx)
			} else {
				m.tryAcquire(ctx)
			}
		}
	}
}

// RunIfMaster runs fn only while this instance is master. fn receives a
// context that is cancelled when master status is lost. If this instance later
// regains master status fn is restarted. Blocks until ctx is cancelled.
//
//nolint:govet // lostcancel false positive: cancel is stored in closure and invoked via defer or on state transition
func (m *MasterLock) RunIfMaster(ctx context.Context, fn func(context.Context)) {
	cancel := func() {}
	defer func() { cancel() }()
	var (
		done    chan struct{}
		running bool
	)

	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if running {
				<-done
			}
			return
		case <-ticker.C:
			isMaster := m.held.Load()
			switch {
			case isMaster && !running:
				var fnCtx context.Context
				fnCtx, cancel = context.WithCancel(ctx)
				done = make(chan struct{})
				running = true
				go func() {
					defer close(done)
					fn(fnCtx)
				}()
			case !isMaster && running:
				cancel()
				cancel = func() {}
				<-done
				running = false
			}
		}
	}
}

// tryAcquire attempts a SetNX to claim the master key.
func (m *MasterLock) tryAcquire(ctx context.Context) {
	ok, err := m.v.SetNX(ctx, masterKey, m.id, masterTTL)
	if err != nil {
		m.logger.Warn("master lock acquire error", "err", err)
		return
	}
	if ok {
		m.held.Store(true)
		m.logger.Info("became master instance", "id", m.id)
	}
}

// tryRefresh extends the TTL only if we still hold the key.
func (m *MasterLock) tryRefresh(ctx context.Context) {
	// Verify we still own it before refreshing to guard against split-brain.
	val, err := m.v.Get(ctx, masterKey)
	if err != nil || val != m.id {
		m.held.Store(false)
		m.logger.Warn("master lock lost", "id", m.id)
		return
	}
	if err := m.v.Expire(ctx, masterKey, masterTTL); err != nil {
		m.logger.Warn("master lock refresh error", "err", err)
		m.held.Store(false)
	}
}
