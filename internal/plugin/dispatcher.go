package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/audit"
)

// Audit action constants for plugin dispatch outcomes. Kept inside this
// package so adding a new plugin role doesn't require touching internal/audit.
const (
	ActionPluginNotifyDelivered    = "plugin.notify.delivered"
	ActionPluginNotifyFailed       = "plugin.notify.failed"
	ActionPluginNotifyDropped      = "plugin.notify.dropped"
	ActionPluginNotifyBreakerOpen  = "plugin.notify.breaker_open"
	ActionPluginNotifyBreakerClose = "plugin.notify.breaker_close"
)

const (
	// queueDepth caps in-flight events per plugin. A slow or hung plugin can
	// only soak up this many events before further dispatches are dropped
	// (with an audit row) instead of growing unbounded.
	queueDepth = 64
	// breakerThreshold is the consecutive-failure count that opens the
	// per-plugin circuit breaker.
	breakerThreshold = 3
	// breakerCooldown is how long a tripped breaker stays open. The next
	// dispatch after cooldown is treated as a probe; success closes the
	// breaker, failure re-opens for another cooldown.
	breakerCooldown = 5 * time.Minute
)

// NotificationDispatcher fans an event out to every enabled notification
// plugin. Each plugin has its own goroutine + bounded queue so a slow plugin
// can't block fast ones, and a failing plugin is short-circuited by a
// per-plugin breaker rather than burning CPU on retries.
type NotificationDispatcher struct {
	registry *Registry
	logger   *slog.Logger
	audit    *audit.Logger

	mu      sync.Mutex
	workers map[uuid.UUID]*pluginWorker

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
}

// NewNotificationDispatcher constructs a dispatcher. audit may be nil in tests.
func NewNotificationDispatcher(reg *Registry, logger *slog.Logger, auditLog *audit.Logger) *NotificationDispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &NotificationDispatcher{
		registry: reg,
		logger:   logger,
		audit:    auditLog,
		workers:  map[uuid.UUID]*pluginWorker{},
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Dispatch fans evt out to every enabled notification plugin. Returns
// immediately — delivery happens in per-plugin background workers. Caller
// should not block on the result.
func (d *NotificationDispatcher) Dispatch(evt NotificationEvent) {
	if d == nil {
		return
	}
	if evt.CorrelationID == "" {
		evt.CorrelationID = uuid.NewString()
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()

	// Look up the active plugin set on every dispatch — the admin UI may have
	// added/removed/disabled plugins since the last call. The query is a single
	// indexed read against `plugins WHERE role='notification' AND enabled`.
	listCtx, cancel := context.WithTimeout(d.ctx, 5*time.Second)
	defer cancel()
	plugins, err := d.registry.ListEnabledByRole(listCtx, RoleNotification)
	if err != nil {
		d.logger.WarnContext(d.ctx, "plugin dispatch: list plugins", "err", err)
		return
	}

	for _, p := range plugins {
		w, err := d.workerFor(p)
		if err != nil {
			d.logger.WarnContext(d.ctx, "plugin dispatch: build client",
				"plugin", p.Name, "err", err)
			continue
		}
		w.enqueue(evt)
	}
}

// TestDispatch synchronously sends a single notify event to one plugin and
// returns the result. Bypasses the worker queue and circuit breaker so the
// caller gets immediate feedback and a genuine probe doesn't pollute steady-
// state health tracking. Intended for the admin "Test" button only.
func (d *NotificationDispatcher) TestDispatch(ctx context.Context, id uuid.UUID, evt NotificationEvent) error {
	if evt.CorrelationID == "" {
		evt.CorrelationID = uuid.NewString()
	}
	p, err := d.registry.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("lookup plugin: %w", err)
	}
	if p.Role != RoleNotification {
		return fmt.Errorf("plugin role %q does not accept notify", p.Role)
	}
	client, err := newPluginClient(p)
	if err != nil {
		return fmt.Errorf("build client: %w", err)
	}
	defer client.close()

	callCtx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	defer cancel()
	if _, err := client.callTool(callCtx, NotifyToolName, evt); err != nil {
		return err
	}
	return nil
}

// Close stops every worker and waits for in-flight calls to finish.
func (d *NotificationDispatcher) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	workers := d.workers
	d.workers = nil
	d.mu.Unlock()

	for _, w := range workers {
		w.stop()
	}
	d.cancel()
	d.wg.Wait()
}

// workerFor returns the cached worker for a plugin, creating one on first use.
// A plugin whose endpoint URL or allowlist changed since the worker started
// is replaced, so admin edits take effect on the next dispatch.
func (d *NotificationDispatcher) workerFor(p Plugin) (*pluginWorker, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if w, ok := d.workers[p.ID]; ok && w.matches(p) {
		return w, nil
	}
	if existing, ok := d.workers[p.ID]; ok {
		existing.stop()
		delete(d.workers, p.ID)
	}
	pc, err := newPluginClient(p)
	if err != nil {
		return nil, err
	}
	w := &pluginWorker{
		plugin: p,
		client: pc,
		queue:  make(chan NotificationEvent, queueDepth),
		stopCh: make(chan struct{}),
		logger: d.logger,
		audit:  d.audit,
	}
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		w.run(d.ctx)
	}()
	d.workers[p.ID] = w
	return w, nil
}

// pluginWorker owns one plugin's queue + connection. All exported state is
// accessed only from run; callers interact via enqueue / stop.
type pluginWorker struct {
	plugin Plugin
	client *pluginClient
	queue  chan NotificationEvent
	stopCh chan struct{}
	logger *slog.Logger
	audit  *audit.Logger

	stopOnce sync.Once

	// Breaker fields are only touched from run, so no mutex needed.
	consecFails int
	openUntil   time.Time
}

func (w *pluginWorker) matches(p Plugin) bool {
	return w.plugin.EndpointURL == p.EndpointURL &&
		equalStringSets(w.plugin.AllowedHosts, p.AllowedHosts)
}

func (w *pluginWorker) enqueue(evt NotificationEvent) {
	select {
	case w.queue <- evt:
	default:
		// Queue full — drop and audit. Better to lose one event than to
		// block the dispatch caller (which may be on a hot path like the
		// playback handler).
		w.logger.Warn("plugin queue full, dropping event",
			"plugin", w.plugin.Name, "event", evt.Event,
			"correlation_id", evt.CorrelationID)
		if w.audit != nil {
			w.audit.Log(context.Background(), nil, ActionPluginNotifyDropped,
				w.plugin.Name, map[string]any{
					"event":          evt.Event,
					"correlation_id": evt.CorrelationID,
					"reason":         "queue_full",
				}, "")
		}
	}
}

func (w *pluginWorker) stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
		w.client.close()
	})
}

func (w *pluginWorker) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case evt := <-w.queue:
			w.deliver(ctx, evt)
		}
	}
}

func (w *pluginWorker) deliver(ctx context.Context, evt NotificationEvent) {
	if !w.openUntil.IsZero() && time.Now().Before(w.openUntil) {
		// Breaker open — drop with audit row.
		w.logger.Debug("plugin breaker open, dropping event",
			"plugin", w.plugin.Name, "event", evt.Event,
			"correlation_id", evt.CorrelationID)
		if w.audit != nil {
			w.audit.Log(ctx, nil, ActionPluginNotifyDropped, w.plugin.Name,
				map[string]any{
					"event":          evt.Event,
					"correlation_id": evt.CorrelationID,
					"reason":         "breaker_open",
				}, "")
		}
		return
	}

	callCtx, cancel := context.WithTimeout(ctx, defaultCallTimeout)
	_, err := w.client.callTool(callCtx, NotifyToolName, evt)
	cancel()

	if err == nil {
		if w.consecFails > 0 || !w.openUntil.IsZero() {
			w.openUntil = time.Time{}
			w.consecFails = 0
			if w.audit != nil {
				w.audit.Log(ctx, nil, ActionPluginNotifyBreakerClose,
					w.plugin.Name, nil, "")
			}
		}
		if w.audit != nil {
			w.audit.Log(ctx, nil, ActionPluginNotifyDelivered, w.plugin.Name,
				map[string]any{
					"event":          evt.Event,
					"correlation_id": evt.CorrelationID,
				}, "")
		}
		return
	}

	// A missing tool isn't a "transport failed" condition — the plugin is
	// healthy, it just doesn't implement the role we registered it under.
	// Don't trip the breaker, but do audit so the operator notices.
	var notAdvertised *ToolNotAdvertisedError
	if errors.As(err, &notAdvertised) {
		w.logger.Warn("plugin missing notify tool",
			"plugin", w.plugin.Name, "tool", notAdvertised.Tool)
		if w.audit != nil {
			w.audit.Log(ctx, nil, ActionPluginNotifyFailed, w.plugin.Name,
				map[string]any{
					"event":          evt.Event,
					"correlation_id": evt.CorrelationID,
					"reason":         "tool_not_advertised",
				}, "")
		}
		return
	}

	w.consecFails++
	w.logger.Warn("plugin delivery failed",
		"plugin", w.plugin.Name,
		"event", evt.Event,
		"correlation_id", evt.CorrelationID,
		"consec_fails", w.consecFails,
		"err", err)
	if w.audit != nil {
		w.audit.Log(ctx, nil, ActionPluginNotifyFailed, w.plugin.Name,
			map[string]any{
				"event":          evt.Event,
				"correlation_id": evt.CorrelationID,
				"err":            err.Error(),
			}, "")
	}

	if w.consecFails >= breakerThreshold {
		w.openUntil = time.Now().Add(breakerCooldown)
		w.logger.Warn("plugin breaker tripped",
			"plugin", w.plugin.Name,
			"cooldown", breakerCooldown,
			"open_until", w.openUntil)
		if w.audit != nil {
			w.audit.Log(ctx, nil, ActionPluginNotifyBreakerOpen,
				w.plugin.Name, map[string]any{
					"cooldown": breakerCooldown.String(),
				}, "")
		}
	}
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}
