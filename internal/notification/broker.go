// Package notification provides real-time notification delivery via SSE.
package notification

import (
	"encoding/json"
	"sync"

	"github.com/google/uuid"
)

// Event is a payload sent to connected clients over the user's SSE
// channel. Two shapes share this struct so a single broker covers
// both:
//
//   - **User-facing notifications** (Type = "item_added", "scan_complete",
//     etc.): Title/Body are populated for the bell-icon UI.
//   - **Sync events** (Type = "progress.updated"): Title/Body are
//     empty; Data carries the structured payload (item_id +
//     position_ms + state) other devices use to refresh their
//     resume-position cache. Frontend filters on Type so sync
//     events don't render in the notification list.
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Title     string          `json:"title,omitempty"`
	Body      string          `json:"body,omitempty"`
	ItemID    *string         `json:"item_id,omitempty"`
	Read      bool            `json:"read"`
	CreatedAt int64           `json:"created_at"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// MaxSubscribersPerUser caps the number of simultaneous SSE
// connections a single authenticated user may hold. Without a cap, a
// buggy or hostile client can open thousands of /notifications/stream
// connections, each holding 16-event buffers + per-connection HTTP
// resources, until the server runs out of file descriptors.
//
// 8 covers the realistic upper bound (4 devices × 2 tabs each) with
// slack. Subscribe returns a closed channel when the cap is hit so
// the SSE handler can return 429 instead of holding a connection
// open with a never-firing reader.
const MaxSubscribersPerUser = 8

// Broker manages per-user SSE subscriptions. It is safe for concurrent use.
type Broker struct {
	mu   sync.RWMutex
	subs map[uuid.UUID]map[chan Event]struct{} // user_id → set of channels
}

// NewBroker creates a Broker.
func NewBroker() *Broker {
	return &Broker{subs: make(map[uuid.UUID]map[chan Event]struct{})}
}

// Subscribe registers a client channel for the given user.
// The caller must call Unsubscribe when done. Returns nil when the
// per-user subscriber cap is hit; the SSE handler should treat nil
// as "too many subscribers" and reject the request.
func (b *Broker) Subscribe(userID uuid.UUID) chan Event {
	b.mu.Lock()
	if existing := b.subs[userID]; len(existing) >= MaxSubscribersPerUser {
		b.mu.Unlock()
		return nil
	}
	ch := make(chan Event, 16)
	if b.subs[userID] == nil {
		b.subs[userID] = make(map[chan Event]struct{})
	}
	b.subs[userID][ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a client channel and closes it. No-op when ch is
// nil — callers can route the Subscribe result through `defer
// Unsubscribe(...)` without checking for nil first; Subscribe returns
// nil when the per-user cap is hit and the SSE handler bails before
// accepting any events.
func (b *Broker) Unsubscribe(userID uuid.UUID, ch chan Event) {
	if ch == nil {
		return
	}
	b.mu.Lock()
	if set, ok := b.subs[userID]; ok {
		delete(set, ch)
		if len(set) == 0 {
			delete(b.subs, userID)
		}
	}
	b.mu.Unlock()
	close(ch)
}

// Publish sends an event to all subscribers of a given user.
// Non-blocking: if a client channel is full the event is dropped for that client.
func (b *Broker) Publish(userID uuid.UUID, ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[userID] {
		select {
		case ch <- ev:
		default:
		}
	}
}
