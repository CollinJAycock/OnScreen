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
// The caller must call Unsubscribe when done.
func (b *Broker) Subscribe(userID uuid.UUID) chan Event {
	ch := make(chan Event, 16)
	b.mu.Lock()
	if b.subs[userID] == nil {
		b.subs[userID] = make(map[chan Event]struct{})
	}
	b.subs[userID][ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a client channel and closes it.
func (b *Broker) Unsubscribe(userID uuid.UUID, ch chan Event) {
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
