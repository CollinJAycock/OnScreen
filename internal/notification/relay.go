package notification

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/onscreen/onscreen/internal/valkey"
)

// relayChannel is the Valkey pub/sub channel every API instance publishes its
// events on and listens to.
const relayChannel = "notification:events"

// relayQueue bounds the events waiting to be published. Publish never blocks
// on Valkey: past this the event still reaches this instance's subscribers,
// only the other instances miss it.
const relayQueue = 256

// relayTiming is how the relay paces itself; tests shorten it.
type relayTiming struct {
	minBackoff, maxBackoff time.Duration // between resubscribe attempts
	pingEvery              time.Duration // idle subscription health check
	publishTimeout         time.Duration
}

var defaultRelayTiming = relayTiming{
	minBackoff:     250 * time.Millisecond,
	maxBackoff:     30 * time.Second,
	pingEvery:      30 * time.Second,
	publishTimeout: 2 * time.Second,
}

// relayEnvelope is one event on the wire, tagged with the instance that
// published it so that instance doesn't deliver it a second time.
type relayEnvelope struct {
	Origin string    `json:"o"`
	UserID uuid.UUID `json:"u"`
	Event  Event     `json:"e"`
}

// relay carries a Broker's events to the Brokers of the other API instances
// through Valkey pub/sub, and theirs to it.
//
// The broker's subscribers are this process's SSE connections, and the server
// is built to run several instances behind a load balancer: the admin's "stop
// playback", cross-device resume, "play on…" and scan notices were published
// on whichever instance handled the request, so they reached a viewer only
// when the viewer's stream happened to be held by the same instance.
//
// Local delivery stays direct and synchronous (Broker.Publish), so a Valkey
// outage costs only the cross-instance half: events published meanwhile, and
// those other instances publish while this one is resubscribing, are not
// replayed — pub/sub keeps nothing, and SSE was already best-effort (clients
// refetch on reconnect).
type relay struct {
	broker *Broker
	rdb    *redis.Client
	origin string // this instance; drops its own events coming back
	logger *slog.Logger
	timing relayTiming
	out    chan relayEnvelope

	lastWarn atomic.Int64 // unix nanos of the last rate-limited warning
}

// WithRelay connects the broker to the other instances' brokers through v
// (see relay). The relay runs until ctx ends. Call it before the broker is in
// use. A nil v leaves the broker process-local.
func (b *Broker) WithRelay(ctx context.Context, v *valkey.Client, logger *slog.Logger) *Broker {
	if v == nil {
		return b
	}
	return b.startRelay(ctx, v.Raw(), logger, defaultRelayTiming)
}

func (b *Broker) startRelay(ctx context.Context, rdb *redis.Client, logger *slog.Logger, timing relayTiming) *Broker {
	if logger == nil {
		logger = slog.Default()
	}
	r := &relay{
		broker: b,
		rdb:    rdb,
		origin: uuid.NewString(),
		logger: logger,
		timing: timing,
		out:    make(chan relayEnvelope, relayQueue),
	}
	b.relay = r
	go r.publishLoop(ctx)
	go r.subscribeLoop(ctx)
	return b
}

// send queues ev for the other instances. Never blocks.
func (r *relay) send(userID uuid.UUID, ev Event) {
	select {
	case r.out <- relayEnvelope{Origin: r.origin, UserID: userID, Event: ev}:
	default:
		r.warn("notification relay: queue full; event delivered on this instance only", nil)
	}
}

// publishLoop publishes queued events one at a time, so they reach the other
// instances in the order they were published here.
func (r *relay) publishLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case env := <-r.out:
			payload, err := json.Marshal(env)
			if err != nil {
				r.warn("notification relay: encode event", err)
				continue
			}
			pctx, cancel := context.WithTimeout(ctx, r.timing.publishTimeout)
			err = r.rdb.Publish(pctx, relayChannel, payload).Err()
			cancel()
			if err != nil && ctx.Err() == nil {
				r.warn("notification relay: publish failed; event delivered on this instance only", err)
			}
		}
	}
}

// subscribeLoop keeps a subscription to relayChannel open, resubscribing with
// exponential backoff whenever it drops (reset once a subscription is
// confirmed again).
func (r *relay) subscribeLoop(ctx context.Context) {
	backoff := r.timing.minBackoff
	for {
		subscribed, err := r.subscribeOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if subscribed {
			backoff = r.timing.minBackoff
		}
		r.logger.WarnContext(ctx, "notification relay: Valkey subscription lost; other instances' events are not delivered here until it is back",
			"err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, r.timing.maxBackoff)
	}
}

var errNoPong = errors.New("no reply to the subscription health check")

// subscribeOnce holds one subscription until it fails, delivering what
// arrives. subscribed reports whether Valkey confirmed it.
func (r *relay) subscribeOnce(ctx context.Context) (subscribed bool, err error) {
	ps := r.rdb.Subscribe(ctx, relayChannel)
	// A blocked read doesn't see ctx; closing the subscription ends it.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		_ = ps.Close()
	}()

	// A subscription only ever reads, and a peer that vanished without a
	// reset (NAT timeout, a failed-over primary) leaves the read waiting
	// forever. After pingEvery of silence, ping; a second silent interval
	// means the connection is dead.
	awaitingPong := false
	for {
		msg, err := ps.ReceiveTimeout(ctx, r.timing.pingEvery)
		if err != nil {
			var ne net.Error
			if !errors.As(err, &ne) || !ne.Timeout() {
				return subscribed, err
			}
			if awaitingPong {
				return subscribed, errNoPong
			}
			if err := ps.Ping(ctx); err != nil {
				return subscribed, err
			}
			awaitingPong = true
			continue
		}
		awaitingPong = false
		switch m := msg.(type) {
		case *redis.Subscription:
			if m.Kind == "subscribe" {
				subscribed = true
			}
		case *redis.Message:
			r.receive(m.Payload)
		}
	}
}

// receive delivers another instance's event to this instance's subscribers,
// through the same non-blocking path as a local publish (a slow client drops
// it; the per-user cap is the subscribe-time one).
func (r *relay) receive(payload string) {
	var env relayEnvelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		r.warn("notification relay: undecodable event", err)
		return
	}
	if env.Origin == r.origin {
		return // published here, and delivered here already
	}
	r.broker.deliver(env.UserID, env.Event)
}

// warn logs at most once a minute, so an outage doesn't log once per event.
func (r *relay) warn(msg string, err error) {
	now := time.Now().UnixNano()
	last := r.lastWarn.Load()
	if now-last < int64(time.Minute) || !r.lastWarn.CompareAndSwap(last, now) {
		return
	}
	if err != nil {
		r.logger.Warn(msg, "err", err)
		return
	}
	r.logger.Warn(msg)
}
