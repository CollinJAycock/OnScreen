package scrobble

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// nowPlayingWindow is how often a still-playing item may re-announce itself.
// Long enough that heartbeats cost nothing, short enough that a seek shows up
// on Trakt's "watching" progress within a few minutes.
const nowPlayingWindow = 10 * time.Minute

// nowPlayingGate lets one "now playing" dispatch through per (user, item) per
// window. Clients report "playing" on every progress heartbeat (every few
// seconds per stream), and each one reaches the scrobble hook; the gate turns
// that into one announcement without touching the database. A pause or stop
// releases the item, so resuming announces again straight away.
//
// It is per process. Behind several API instances an item can be announced
// once per instance per window, which every service treats as a refresh.
type nowPlayingGate struct {
	mu     sync.Mutex
	window time.Duration
	last   map[nowPlayingKey]time.Time
	swept  time.Time
}

type nowPlayingKey struct{ user, item uuid.UUID }

func newNowPlayingGate(window time.Duration) *nowPlayingGate {
	return &nowPlayingGate{window: window, last: map[nowPlayingKey]time.Time{}}
}

// claim reports whether (user, item) may announce now, and if so records it.
func (g *nowPlayingGate) claim(user, item uuid.UUID, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	// Drop expired entries once per window, so the map holds only items
	// played recently rather than everything since startup.
	if now.Sub(g.swept) >= g.window {
		for k, t := range g.last {
			if now.Sub(t) >= g.window {
				delete(g.last, k)
			}
		}
		g.swept = now
	}
	k := nowPlayingKey{user, item}
	if t, ok := g.last[k]; ok && now.Sub(t) < g.window {
		return false
	}
	g.last[k] = now
	return true
}

// release forgets (user, item), so its next play announces immediately.
func (g *nowPlayingGate) release(user, item uuid.UUID) {
	g.mu.Lock()
	delete(g.last, nowPlayingKey{user, item})
	g.mu.Unlock()
}
