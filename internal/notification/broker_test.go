package notification

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBroker_SubscribeAndPublish(t *testing.T) {
	b := NewBroker()
	uid := uuid.New()
	ch := b.Subscribe(uid)

	ev := Event{ID: "1", Type: "test", Title: "Hello"}
	b.Publish(uid, ev)

	select {
	case got := <-ch:
		if got.Title != "Hello" {
			t.Errorf("Title: got %q, want %q", got.Title, "Hello")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}

	b.Unsubscribe(uid, ch)
}

func TestBroker_Unsubscribe_ClosesChannel(t *testing.T) {
	b := NewBroker()
	uid := uuid.New()
	ch := b.Subscribe(uid)
	b.Unsubscribe(uid, ch)

	// Channel should be closed.
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed after Unsubscribe")
	}
}

func TestBroker_Unsubscribe_RemovesEmptyUserEntry(t *testing.T) {
	b := NewBroker()
	uid := uuid.New()
	ch := b.Subscribe(uid)
	b.Unsubscribe(uid, ch)

	b.mu.RLock()
	_, exists := b.subs[uid]
	b.mu.RUnlock()
	if exists {
		t.Error("expected user entry to be removed after last subscription unsubscribed")
	}
}

func TestBroker_PublishToUnknownUser_NoPanic(t *testing.T) {
	b := NewBroker()
	// Publishing to a user with no subscribers should not panic.
	b.Publish(uuid.New(), Event{Title: "orphan"})
}

func TestBroker_MultipleSubscribers(t *testing.T) {
	b := NewBroker()
	uid := uuid.New()
	ch1 := b.Subscribe(uid)
	ch2 := b.Subscribe(uid)

	b.Publish(uid, Event{Title: "broadcast"})

	for _, ch := range []chan Event{ch1, ch2} {
		select {
		case got := <-ch:
			if got.Title != "broadcast" {
				t.Errorf("Title: got %q, want %q", got.Title, "broadcast")
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("timed out waiting for event on subscriber")
		}
	}

	b.Unsubscribe(uid, ch1)
	b.Unsubscribe(uid, ch2)
}

func TestBroker_PublishDoesNotBlockOnFullChannel(t *testing.T) {
	b := NewBroker()
	uid := uuid.New()
	ch := b.Subscribe(uid)

	// Fill the channel buffer (capacity 16).
	for i := 0; i < 16; i++ {
		b.Publish(uid, Event{Title: "fill"})
	}

	// This should not block — the event is dropped.
	done := make(chan struct{})
	go func() {
		b.Publish(uid, Event{Title: "overflow"})
		close(done)
	}()

	select {
	case <-done:
		// Success — publish returned without blocking.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Publish blocked on full channel")
	}

	b.Unsubscribe(uid, ch)
}

func TestBroker_IsolatesBetweenUsers(t *testing.T) {
	b := NewBroker()
	uid1 := uuid.New()
	uid2 := uuid.New()
	ch1 := b.Subscribe(uid1)
	ch2 := b.Subscribe(uid2)

	b.Publish(uid1, Event{Title: "for user1"})

	select {
	case got := <-ch1:
		if got.Title != "for user1" {
			t.Errorf("ch1 Title: got %q, want %q", got.Title, "for user1")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out on ch1")
	}

	// ch2 should have nothing.
	select {
	case ev := <-ch2:
		t.Errorf("ch2 should be empty, got %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// Expected — no event.
	}

	b.Unsubscribe(uid1, ch1)
	b.Unsubscribe(uid2, ch2)
}

func TestBroker_ConcurrentAccess(t *testing.T) {
	b := NewBroker()
	uid := uuid.New()
	var wg sync.WaitGroup

	// Spawn 10 subscribers concurrently.
	channels := make([]chan Event, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			channels[idx] = b.Subscribe(uid)
		}(i)
	}
	wg.Wait()

	// Publish concurrently.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.Publish(uid, Event{Title: "concurrent"})
		}()
	}
	wg.Wait()

	// Unsubscribe concurrently.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			b.Unsubscribe(uid, channels[idx])
		}(i)
	}
	wg.Wait()
}
