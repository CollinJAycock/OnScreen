package notifyagents

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// ── Scheduled tasks ──────────────────────────────────────────────────────────

// backupTaskType is the scheduler's database-backup task (backup_failed).
const backupTaskType = "backup_database"

// TaskResult is the scheduler's per-run hook. A failed run emits task_failed
// (backup_failed for the backup task) — once: while the task keeps failing,
// repeats are suppressed until it succeeds again or taskRealert passes, so a
// sync task that fails every five minutes doesn't flood the channel.
func (s *Service) TaskResult(ctx context.Context, taskID uuid.UUID, name, taskType string, runErr error) {
	now := s.now()
	s.mu.Lock()
	if runErr == nil {
		delete(s.taskAlerts, taskID)
		s.mu.Unlock()
		return
	}
	if last, ok := s.taskAlerts[taskID]; ok && now.Sub(last) < s.taskRealert {
		s.mu.Unlock()
		return
	}
	s.taskAlerts[taskID] = now
	s.mu.Unlock()

	reason := truncateRunes(singleLine(runErr.Error()), 300)
	if name == "" {
		name = taskType
	}
	m := Message{Event: EventTaskFailed, Severity: SeverityError, Time: now}
	if taskType == backupTaskType {
		m.Event = EventBackupFailed
		m.Title = "Backup failed"
		m.Body = fmt.Sprintf("The scheduled backup %q failed: %s", name, reason)
	} else {
		m.Title = "Scheduled task failed: " + name
		m.Body = fmt.Sprintf("%s (%s) failed: %s", name, taskType, reason)
	}
	m.Body += "\nFurther failures of this task are held back until it succeeds again."
	m.URL = s.link(ctx, eventPath(m.Event))
	s.Emit(m)
}

// ── Transcode worker fleet ───────────────────────────────────────────────────

// FleetMonitor watches the transcode worker registry and emits
// worker_fleet_down when no worker is available — the embedded worker is off
// (or dead) and no remote worker has checked in — so every playback that
// needs a remux or transcode will fail. It alerts on the transition only, and
// once more when a worker is back.
//
// Across server nodes (WithClaim) each transition is announced once. Every
// node polls on its own; a transition is identified by its state and the
// wall-clock tick (interval-sized bucket) the node detected it in, and the
// claim covers that tick AND the one before: nodes see the same registry, so
// they detect the same transition less than a poll apart — at most one tick
// off — and the later one finds the earlier one's key. Detections of the same
// state are at least threshold+1 polls apart on a node (down, up, then
// threshold empty polls), so a real repeat never collides with an old key —
// down → up → down inside any window is announced every time.
type FleetMonitor struct {
	s      *Service
	count  func(ctx context.Context) (int, error)
	claim  func(ctx context.Context, key string, ttl time.Duration) bool
	logger *slog.Logger

	interval  time.Duration // poll interval, and the claim tick size
	grace     time.Duration // after start, before the first alert
	threshold int           // consecutive empty polls before "down"
	start     time.Time
	now       func() time.Time

	down        bool
	emptyStreak int
	// sawUp: this node has seen a live worker since it started. A "down"
	// before that is boot-time: the outage may predate this node and have
	// been announced by another, so it is only announced when no node holds
	// the down state (see holdDown).
	sawUp bool
}

// Fleet claim keys. They are per tick, so they never collide with a later
// transition; the TTL only has to outlast the skew between nodes' polls.
const (
	fleetClaimPrefix = "notifyagents:fleet:"
	fleetClaimTTL    = 10 * time.Minute
	// fleetHeldLookback caps how many past ticks a boot-time "down" checks
	// for a node holding the down state.
	fleetHeldLookback = 16
)

// NewFleetMonitor watches count, which returns the number of registered,
// live transcode workers (the embedded worker included).
func NewFleetMonitor(s *Service, count func(ctx context.Context) (int, error), logger *slog.Logger) *FleetMonitor {
	if logger == nil {
		logger = slog.Default()
	}
	return &FleetMonitor{
		s:         s,
		count:     count,
		logger:    logger,
		interval:  time.Minute,
		grace:     3 * time.Minute,
		threshold: 2,
		start:     time.Now(),
		now:       time.Now,
	}
}

// WithClaim de-duplicates alerts across server nodes: claim(key, ttl) must
// return true for exactly one caller per key and ttl (a Valkey SET NX). Each
// node still tracks its own state; only the claimant sends.
func (m *FleetMonitor) WithClaim(claim func(ctx context.Context, key string, ttl time.Duration) bool) *FleetMonitor {
	m.claim = claim
	return m
}

// Run polls until ctx is cancelled.
func (m *FleetMonitor) Run(ctx context.Context) {
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.poll(ctx)
		}
	}
}

// tick is the wall-clock bucket a poll at t falls in — the same on every
// node for the same moment.
func (m *FleetMonitor) tick(t time.Time) int64 {
	return t.UnixNano() / int64(m.interval)
}

// poll takes one reading and emits on a state change. The state is recorded
// whether or not this node wins the claim: a lost claim means another node
// announced the same transition.
func (m *FleetMonitor) poll(ctx context.Context) {
	now := m.now()
	tick := m.tick(now)
	defer m.holdDown(ctx, tick)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	n, err := m.count(cctx)
	cancel()
	if err != nil {
		// Can't tell (registry unreachable): hold the current state rather
		// than flap on a transient error.
		m.logger.Debug("fleet monitor: count workers", "err", err)
		return
	}
	if n > 0 {
		m.emptyStreak = 0
		m.sawUp = true
		if m.down {
			m.down = false
			m.announce(ctx, "up", tick, Message{
				Event:    EventWorkerFleetDown,
				Title:    "Transcode workers are back",
				Body:     fmt.Sprintf("%d transcode worker%s available again.", n, plural(n, " is", "s are")),
				Severity: SeveritySuccess,
			})
		}
		return
	}
	m.emptyStreak++
	if m.down || m.emptyStreak < m.threshold || now.Sub(m.start) < m.grace {
		return
	}
	m.down = true
	if !m.sawUp && m.downHeldElsewhere(ctx, tick) {
		m.logger.Info("fleet monitor: no transcode worker available (already announced)")
		return
	}
	m.announce(ctx, "down", tick, Message{
		Event: EventWorkerFleetDown,
		Title: "No transcode worker available",
		Body: "The embedded transcode worker is off or not responding and no remote worker has checked in. " +
			"Direct play still works; anything that needs a remux or transcode will fail until a worker is back.",
		Severity: SeverityError,
	})
}

// holdDown marks, once per poll, that some node is in the down state this
// tick — what a boot-time detection checks. Only a boot-time "down" reads
// these keys; an ordinary transition never does, so a node that missed a
// brief recovery can't hold back a later real "down".
func (m *FleetMonitor) holdDown(ctx context.Context, tick int64) {
	if m.down && m.claim != nil {
		m.claim(ctx, fleetClaimPrefix+"held:"+strconv.FormatInt(tick, 10), fleetClaimTTL)
	}
}

// downHeldElsewhere reports whether another node held the down state during
// the ticks since just before this node started (capped): the outage was
// announced, or adopted by a node that saw it announced. Checking claims the
// probed keys, which is harmless — only boot-time detections read them, and
// this node then either announces (and holds) or defers to one that did.
func (m *FleetMonitor) downHeldElsewhere(ctx context.Context, tick int64) bool {
	if m.claim == nil {
		return false
	}
	from := max(m.tick(m.start)-1, tick-fleetHeldLookback)
	for t := tick - 1; t >= from; t-- {
		if !m.claim(ctx, fleetClaimPrefix+"held:"+strconv.FormatInt(t, 10), fleetClaimTTL) {
			return true
		}
	}
	return false
}

// announce emits a transition unless another node already claimed it.
func (m *FleetMonitor) announce(ctx context.Context, state string, tick int64, msg Message) {
	key := func(t int64) string { return fleetClaimPrefix + state + ":" + strconv.FormatInt(t, 10) }
	if m.claim != nil && (!m.claim(ctx, key(tick), fleetClaimTTL) || !m.claim(ctx, key(tick-1), fleetClaimTTL)) {
		m.logger.Info("fleet monitor: transcode worker availability changed (announced by another node)", "state", state)
		return
	}
	msg.Time = m.now()
	msg.URL = m.s.link(ctx, eventPath(EventWorkerFleetDown))
	// A second outage inside the dedupe window renders the same text; it is
	// still a new event.
	msg.dedupe = key(tick)
	m.logger.Info("fleet monitor: transcode worker availability changed", "state", state)
	m.s.Emit(msg)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
