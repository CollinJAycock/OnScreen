package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/onscreen/onscreen/internal/valkey"
)

var testRelayTiming = relayTiming{
	minBackoff:     10 * time.Millisecond,
	maxBackoff:     100 * time.Millisecond,
	pingEvery:      200 * time.Millisecond,
	publishTimeout: 500 * time.Millisecond,
}

// relayedBroker is one API instance: a broker relayed through its own Valkey
// connection to mr.
func relayedBroker(t *testing.T, ctx context.Context, mr *miniredis.Miniredis) *Broker {
	t.Helper()
	v, err := valkey.New(ctx, "redis://"+mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	// Stop the relay before its client closes (cleanups run last-first).
	rctx, stop := context.WithCancel(ctx)
	t.Cleanup(stop)
	return NewBroker().startRelay(rctx, v.Raw(), slog.Default(), testRelayTiming)
}

// waitSubscribers waits until n instances are subscribed to the relay channel.
func waitSubscribers(t *testing.T, mr *miniredis.Miniredis, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for mr.PubSubNumSub(relayChannel)[relayChannel] != n {
		if time.Now().After(deadline) {
			t.Fatalf("relay subscribers = %d, want %d", mr.PubSubNumSub(relayChannel)[relayChannel], n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func recv(t *testing.T, ch chan Event, what string) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: no event", what)
		return Event{}
	}
}

func noEvent(t *testing.T, ch chan Event, what string) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Errorf("%s: unexpected event %+v", what, ev)
	case <-time.After(150 * time.Millisecond):
	}
}

// The scan / stop-playback / cross-device events were published on whichever
// instance handled the request and reached only that instance's SSE
// subscribers. Through the relay they reach the user's subscribers on every
// instance, exactly once each, payload intact.
func TestRelay_DeliversAcrossInstancesOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mr := miniredis.RunT(t)
	a, b := relayedBroker(t, ctx, mr), relayedBroker(t, ctx, mr)
	waitSubscribers(t, mr, 2)

	user, other := uuid.New(), uuid.New()
	onA, onB := a.Subscribe(user), b.Subscribe(user)
	otherOnB := b.Subscribe(other)
	itemID := uuid.NewString()
	sent := Event{ID: "1", Type: "progress.updated", ItemID: &itemID, CreatedAt: 42,
		Data: json.RawMessage(`{"item_id":"x","position_ms":1000}`)}
	a.Publish(user, sent)

	got := recv(t, onB, "subscriber on the other instance")
	if got.ID != sent.ID || got.Type != sent.Type || got.ItemID == nil || *got.ItemID != itemID ||
		got.CreatedAt != 42 || string(got.Data) != string(sent.Data) {
		t.Errorf("relayed event = %+v, want %+v", got, sent)
	}
	recv(t, onA, "subscriber on the publishing instance")
	noEvent(t, onA, "publishing instance (its own event coming back)")
	noEvent(t, onB, "other instance (a second copy)")
	noEvent(t, otherOnB, "another user's subscriber")
}

// Valkey being down costs only the cross-instance half: the publishing
// instance's subscribers get the event at once and Publish doesn't wait. When
// Valkey is back, both instances resubscribe and relaying resumes.
func TestRelay_ValkeyDownDeliversLocallyAndReconnects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mr := miniredis.RunT(t)
	a, b := relayedBroker(t, ctx, mr), relayedBroker(t, ctx, mr)
	waitSubscribers(t, mr, 2)
	user := uuid.New()
	onA, onB := a.Subscribe(user), b.Subscribe(user)

	mr.Close()
	start := time.Now()
	for i := 0; i < relayQueue+10; i++ { // more than the queue holds
		a.Publish(user, Event{ID: fmt.Sprint(i)})
		if i < 16 {
			if ev := recv(t, onA, "local subscriber while Valkey is down"); ev.ID != fmt.Sprint(i) {
				t.Fatalf("local event %d: got %q", i, ev.ID)
			}
		}
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("publishing with Valkey down took %v; it must not wait on Valkey", d)
	}

	if err := mr.Restart(); err != nil {
		t.Skipf("couldn't restart miniredis on its port: %v", err)
	}
	waitSubscribers(t, mr, 2)
	// What instance a queued while Valkey was down is published now, in order. A fixed
	// 200 ms wait for it was too short under the race detector (the backlog's
	// "239" came in ahead of "after"): wait until it has cleared instead.
	waitRelayIdle(t, a, onB)
	drain(onA)

	a.Publish(user, Event{ID: "after"})
	for {
		ev := recv(t, onB, "other instance after reconnect")
		if ev.ID == "after" {
			break
		}
		// The outage's last event, published just ahead of this one, is the
		// only thing that may come first.
		if _, err := strconv.Atoi(ev.ID); err != nil {
			t.Fatalf("after reconnect: got %q, want %q", ev.ID, "after")
		}
	}
}

// waitRelayIdle waits until b's relay has published everything it queued and
// nothing more arrives on ch, which it drains meanwhile (a full subscriber
// channel would drop the event the caller sends next).
func waitRelayIdle(t *testing.T, b *Broker, ch chan Event) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("relay still publishing its backlog: %d queued", len(b.relay.out))
		}
		if len(b.relay.out) > 0 {
			drain(ch)
			time.Sleep(10 * time.Millisecond)
			continue
		}
		select {
		case <-ch:
			// Still arriving: the event being published as the queue emptied.
		case <-time.After(250 * time.Millisecond):
			return
		}
	}
}

func drain(ch chan Event) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// A slow subscriber on the receiving instance drops relayed events exactly as
// it drops local ones, without stalling the relay for everyone else.
func TestRelay_SlowSubscriberDoesNotStallTheRelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mr := miniredis.RunT(t)
	a, b := relayedBroker(t, ctx, mr), relayedBroker(t, ctx, mr)
	waitSubscribers(t, mr, 2)
	slowUser, user := uuid.New(), uuid.New()
	_ = b.Subscribe(slowUser) // never read
	onB := b.Subscribe(user)

	for i := 0; i < 50; i++ {
		a.Publish(slowUser, Event{ID: fmt.Sprint(i)})
	}
	a.Publish(user, Event{ID: "next"})
	if ev := recv(t, onB, "subscriber behind a full one"); ev.ID != "next" {
		t.Errorf("got %q, want next", ev.ID)
	}

	// The per-user cap is unchanged: it is per instance, at subscribe time.
	for i := 0; i < MaxSubscribersPerUser-1; i++ {
		if b.Subscribe(user) == nil {
			t.Fatalf("subscriber %d refused under the cap", i+2)
		}
	}
	if b.Subscribe(user) != nil {
		t.Error("subscriber past the cap accepted")
	}
}

// A relay stops with its context: no goroutine keeps the subscription open.
func TestRelay_StopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mr := miniredis.RunT(t)
	_ = relayedBroker(t, ctx, mr)
	waitSubscribers(t, mr, 1)
	cancel()
	waitSubscribers(t, mr, 0)
}

// Garbage on the channel is ignored, not delivered or fatal.
func TestRelay_IgnoresUndecodableMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mr := miniredis.RunT(t)
	b := relayedBroker(t, ctx, mr)
	waitSubscribers(t, mr, 1)
	user := uuid.New()
	ch := b.Subscribe(user)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	_ = rdb.Publish(ctx, relayChannel, "not json").Err()
	env, _ := json.Marshal(relayEnvelope{Origin: "elsewhere", UserID: user, Event: Event{ID: "ok"}})
	_ = rdb.Publish(ctx, relayChannel, string(env)).Err()
	if ev := recv(t, ch, "event after garbage"); ev.ID != "ok" {
		t.Errorf("got %q, want ok", ev.ID)
	}
}
