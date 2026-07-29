package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/plugin"
	"github.com/onscreen/onscreen/internal/webhook"
)

// PluginNotifier is the subset of plugin.NotificationDispatcher the webhook
// fan-out needs. Defined as an interface so tests don't need a real plugin
// registry.
type PluginNotifier interface {
	Dispatch(plugin.NotificationEvent)
}

// WebhookServerInfo identifies this server in webhook payloads.
type WebhookServerInfo struct {
	Title     string
	MachineID string
}

// webhookDeliveryDB is the subset of DB operations needed by the dispatcher.
type webhookDeliveryDB interface {
	ListWebhookEndpoints(ctx context.Context) ([]gen.WebhookEndpoint, error)
	CreateWebhookFailure(ctx context.Context, arg gen.CreateWebhookFailureParams) (gen.WebhookFailure, error)
}

// webhookMediaDB is used to enrich payloads with media metadata.
type webhookMediaDB interface {
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
}

// maxConcurrentDeliveries limits the number of concurrent webhook delivery goroutines.
// Each goroutine may sleep up to 5m30s during retries, so bounding this prevents
// unbounded memory/goroutine growth under heavy webhook load.
const maxConcurrentDeliveries = 20

// maxConcurrentDispatches caps the OUTER Dispatch goroutines (the
// per-event fan-outs that read endpoints + build the payload). Without
// this, a mass-import scan that fires 5,000 `library.scan.complete` /
// `item.added` events back-to-back stacks 5,000 outer goroutines —
// each holding a 30 s context and an endpoint-list query — even
// though delivery itself is bounded by maxConcurrentDeliveries. 50
// is enough headroom for normal traffic; 51st event briefly blocks
// the producer (scanner / API), which is fine — they're not in a
// hot path.
const maxConcurrentDispatches = 50

// WebhookDispatcher delivers webhook events asynchronously to all subscribed
// endpoints. If a PluginNotifier is attached via WithPluginNotifier, every
// dispatched event is also fanned out to the registered notification plugins;
// the two paths are independent (a webhook delivery failure does not affect
// plugin delivery, and vice versa).
type WebhookDispatcher struct {
	db          webhookDeliveryDB
	media       webhookMediaDB
	enc         *auth.Encryptor
	server      WebhookServerInfo
	client      *http.Client
	logger      *slog.Logger
	sem         chan struct{} // concurrency limiter for delivery goroutines
	dispatchSem chan struct{} // concurrency limiter for outer Dispatch fan-outs
	wg          sync.WaitGroup
	ctx         context.Context // cancelled on Close to interrupt retries
	cancel      context.CancelFunc
	plugins     PluginNotifier
	metrics     *observability.Metrics
}

// WithMetrics enables Prometheus instrumentation (delivery failures by URL). nil
// is a no-op.
func (d *WebhookDispatcher) WithMetrics(m *observability.Metrics) *WebhookDispatcher {
	d.metrics = m
	return d
}

// NewWebhookDispatcher creates a WebhookDispatcher.
func NewWebhookDispatcher(
	db webhookDeliveryDB,
	media webhookMediaDB,
	enc *auth.Encryptor,
	server WebhookServerInfo,
	logger *slog.Logger,
) *WebhookDispatcher {
	ctx, cancel := context.WithCancel(context.Background())
	return &WebhookDispatcher{
		db:     db,
		media:  media,
		enc:    enc,
		server: server,
		// SafeClient = SafeTransport (private-IP block at dial time) +
		// CheckRedirect rejecting 3xx so a receiver can't bounce the
		// signed POST body to an unapproved host.
		client:      webhook.SafeClient(10 * time.Second),
		logger:      logger,
		sem:         make(chan struct{}, maxConcurrentDeliveries),
		dispatchSem: make(chan struct{}, maxConcurrentDispatches),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// WithPluginNotifier returns d with a PluginNotifier attached. nil disables
// plugin fan-out for this dispatcher (the webhook path is unaffected either way).
func (d *WebhookDispatcher) WithPluginNotifier(p PluginNotifier) *WebhookDispatcher {
	d.plugins = p
	return d
}

// Dispatch fires eventType to all enabled, subscribed endpoints.
// Non-blocking — each delivery runs in its own goroutine. If a PluginNotifier
// is attached, the same event is also fanned out to notification plugins.
func (d *WebhookDispatcher) Dispatch(eventType string, userID, mediaID uuid.UUID) {
	if d.plugins != nil {
		d.plugins.Dispatch(d.buildPluginEvent(eventType, userID, mediaID))
	}

	// Acquire an outer-dispatch slot before spawning the goroutine.
	// Without this cap, a mass-event burst (5000 item.added during
	// a library import) would spawn 5000 outer goroutines holding
	// 30 s contexts each — bounded by inner sem at delivery time
	// but unbounded at fan-out time.
	//
	// SHED rather than block. This used to be a bare send, which parked the
	// CALLER — and Dispatch is called from the request path, including
	// PUT /items/{id}/progress on every playback heartbeat. Because the
	// fan-out goroutine below then blocks on the delivery semaphore while
	// still holding its dispatch slot, one unreachable endpoint drains both
	// pools and every subsequent progress report stalls in the handler, with
	// no context to honour and no deadline. Webhooks are fire-and-forget
	// best-effort; dropping an event under saturation is strictly better than
	// converting a webhook outage into an API outage. Same posture as
	// internal/plugin/dispatcher.go.
	select {
	case d.dispatchSem <- struct{}{}:
	default:
		d.logger.Warn("webhook dispatch: fan-out saturated, dropping event",
			"event", eventType)
		return
	}
	d.wg.Add(1)
	// SafeGo at both levels — a JSON-marshal or DB-call panic in the
	// dispatcher fan-out, or a panic mid-delivery (per-endpoint HTTP /
	// HMAC / retry), shouldn't take the worker down. The deferred
	// WaitGroup decrements + semaphore releases still run via defer
	// even when recover catches a panic.
	observability.SafeGo(d.logger, "webhook-dispatcher:fan-out", func() {
		defer d.wg.Done()
		defer func() { <-d.dispatchSem }()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		endpoints, err := d.db.ListWebhookEndpoints(ctx)
		if err != nil {
			d.logger.Warn("webhook dispatch: list endpoints", "err", err)
			return
		}

		evt := webhookEvent(eventType)
		payload := d.buildPayload(ctx, eventType, userID, mediaID)
		body, err := json.Marshal(payload)
		if err != nil {
			d.logger.Warn("webhook dispatch: marshal payload", "err", err)
			return
		}

		for _, ep := range endpoints {
			if !ep.Enabled || !slices.Contains(ep.Events, evt) {
				continue
			}
			ep := ep // capture loop var
			// Shed here too. Blocking on the delivery semaphore while
			// holding a dispatch slot is what coupled the two pools and let
			// a single dead endpoint exhaust both.
			select {
			case d.sem <- struct{}{}:
			default:
				d.logger.Warn("webhook dispatch: delivery pool saturated, dropping",
					"event", evt, "url", ep.Url)
				continue
			}
			d.wg.Add(1)
			observability.SafeGo(d.logger, "webhook-dispatcher:deliver", func() {
				defer d.wg.Done()
				defer func() { <-d.sem }()
				d.deliverWithRetry(d.ctx, ep, body)
			})
		}
	})
}

// Close cancels in-flight retry sleeps and blocks until all deliveries finish.
func (d *WebhookDispatcher) Close() {
	d.cancel()
	d.wg.Wait()
}

// WebhookPayload is the webhook payload sent to external endpoints (Overseerr, Tautulli, etc.).
type WebhookPayload struct {
	Event    string           `json:"event"`
	User     bool             `json:"user"`
	Owner    bool             `json:"owner"`
	Server   WebhookServer    `json:"Server"`
	Metadata *WebhookMetadata `json:"Metadata,omitempty"`
}

// WebhookServer describes the server in a webhook payload.
type WebhookServer struct {
	Title string `json:"title"`
	UUID  string `json:"uuid"`
}

// WebhookMetadata is the media item portion of a webhook payload.
type WebhookMetadata struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	Year  *int   `json:"year,omitempty"`
	Key   string `json:"key"`
}

func (d *WebhookDispatcher) buildPayload(ctx context.Context, eventType string, userID, mediaID uuid.UUID) WebhookPayload {
	hasUser := userID != uuid.Nil
	p := WebhookPayload{
		Event: webhookEvent(eventType),
		User:  hasUser,
		Owner: hasUser,
		Server: WebhookServer{
			Title: d.server.Title,
			UUID:  d.server.MachineID,
		},
	}
	if mediaID != uuid.Nil {
		if item, err := d.media.GetItem(ctx, mediaID); err == nil {
			p.Metadata = &WebhookMetadata{
				Type:  item.Type,
				Title: item.Title,
				Year:  item.Year,
				Key:   "/api/v1/items/" + mediaID.String(),
			}
		}
	}
	return p
}

// buildPluginEvent translates the webhook-style (eventType, userID, mediaID)
// triple into the plugin notification payload. We don't reuse the webhook
// payload struct because plugins receive the canonical event name only — no
// Plex-compatibility shape, no Server section.
func (d *WebhookDispatcher) buildPluginEvent(eventType string, userID, mediaID uuid.UUID) plugin.NotificationEvent {
	evt := plugin.NotificationEvent{Event: webhookEvent(eventType)}
	if userID != uuid.Nil {
		evt.UserID = userID.String()
	}
	if mediaID != uuid.Nil {
		evt.MediaID = mediaID.String()
		// Best-effort title — failure here is fine, the plugin gets an empty title.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if item, err := d.media.GetItem(ctx, mediaID); err == nil {
			evt.Title = item.Title
		}
	}
	return evt
}

// deliverWithRetry attempts delivery up to 3 times with cancellable sleeps.
// Delays: attempt 1 immediate, attempt 2 +30s, attempt 3 +5min.
// On total failure writes to webhook_failures.
func (d *WebhookDispatcher) deliverWithRetry(ctx context.Context, ep gen.WebhookEndpoint, body []byte) {
	delays := []time.Duration{0, 30 * time.Second, 5 * time.Minute}
	var lastErr error
	for attempt, delay := range delays {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return // shutdown — abandon retries
			case <-time.After(delay):
			}
		}
		// Use a standalone timeout so that a shutdown cancel (ctx) interrupts
		// retry sleeps but does not abort an in-flight HTTP request.
		reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := deliverWebhookHTTP(reqCtx, d.client, d.enc, ep, body)
		cancel()
		if err == nil {
			return
		}
		lastErr = err
		d.logger.Warn("webhook delivery failed",
			"url", ep.Url, "attempt", attempt+1, "err", err)
	}

	// All attempts exhausted.
	if d.metrics != nil {
		d.metrics.WebhookFailuresTotal.WithLabelValues(ep.Url).Inc()
	}
	// Record failure using context.Background on purpose: the caller's ctx may
	// already be cancelled (shutdown), but we still want the failure audit row
	// persisted so ops can see which webhooks dropped. A short detached context
	// would be better if the DB is slow; if that becomes an issue, wrap with a
	// 5s WithTimeout on context.Background.
	if _, err := d.db.CreateWebhookFailure(context.Background(), gen.CreateWebhookFailureParams{
		EndpointID:   ep.ID,
		Url:          ep.Url,
		Payload:      body,
		LastError:    lastErr.Error(),
		AttemptCount: 3,
	}); err != nil {
		d.logger.Error("record webhook failure", "url", ep.Url, "err", err)
	}
}

// deliverWebhookHTTP delegates to the shared webhook.Deliver helper.
func deliverWebhookHTTP(ctx context.Context, client *http.Client, enc *auth.Encryptor, ep gen.WebhookEndpoint, body []byte) error {
	return webhook.Deliver(ctx, client, enc, ep, body)
}

func webhookEvent(eventType string) string {
	switch eventType {
	case "play":
		return "media.play"
	case "pause":
		return "media.pause"
	case "resume":
		return "media.resume"
	case "stop":
		return "media.stop"
	case "scrobble":
		return "media.scrobble"
	default:
		// Already fully-qualified event names (e.g. "library.scan.complete")
		// are passed through as-is.
		if strings.Contains(eventType, ".") {
			return eventType
		}
		return "media." + eventType
	}
}
