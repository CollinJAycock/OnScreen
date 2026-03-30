// Package streaming provides a tracker for active direct-play HTTP streams
// (clients hitting /media/files/* directly). In production it uses Valkey so
// all API instances share the same view of active streams. In tests it falls
// back to an in-memory map (no Valkey required).
package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/valkey"
)

const entryTTL = 45 * time.Second

// Entry represents one active direct-play stream.
type Entry struct {
	FilePath   string
	ClientIP   string
	ClientName string
	FirstSeen  time.Time
	LastSeen   time.Time
}

// Tracker records and expires direct-play stream activity.
// Create with NewValkeyTracker for production or NewTracker for tests.
type Tracker struct {
	v *valkey.Client // nil → in-memory mode

	// in-memory fallback (tests / single-instance with no Valkey dependency)
	mu             sync.Mutex
	entries        map[string]*Entry
	posMu          sync.Mutex
	positionByItem map[uuid.UUID]itemPlayState
}

type itemPlayState struct {
	PositionMS int64     `json:"position_ms"`
	DurationMS int64     `json:"duration_ms"`
	LastUpdate time.Time `json:"last_update"`
}

// NewTracker creates an in-memory Tracker (used in tests).
func NewTracker() *Tracker {
	return &Tracker{
		entries:        make(map[string]*Entry),
		positionByItem: make(map[uuid.UUID]itemPlayState),
	}
}

// NewValkeyTracker creates a Tracker backed by Valkey so all instances sharing
// the same Valkey server see the same stream activity.
func NewValkeyTracker(v *valkey.Client) *Tracker {
	return &Tracker{v: v}
}

// SetItemState records the latest playback position and duration for a media item.
func (t *Tracker) SetItemState(mediaItemID uuid.UUID, positionMS, durationMS int64) {
	if t.v != nil {
		state := itemPlayState{PositionMS: positionMS, DurationMS: durationMS, LastUpdate: time.Now()}
		if b, err := json.Marshal(state); err == nil {
			_ = t.v.Set(context.Background(), posKey(mediaItemID), string(b), entryTTL)
		}
		return
	}
	t.posMu.Lock()
	defer t.posMu.Unlock()
	t.positionByItem[mediaItemID] = itemPlayState{PositionMS: positionMS, DurationMS: durationMS, LastUpdate: time.Now()}
}

// GetItemState returns the last known playback position and duration.
// Both values are 0 if no state has been recorded.
func (t *Tracker) GetItemState(mediaItemID uuid.UUID) (positionMS, durationMS int64) {
	if t.v != nil {
		raw, err := t.v.Get(context.Background(), posKey(mediaItemID))
		if err != nil {
			return 0, 0
		}
		var state itemPlayState
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return 0, 0
		}
		return state.PositionMS, state.DurationMS
	}
	t.posMu.Lock()
	s := t.positionByItem[mediaItemID]
	t.posMu.Unlock()
	return s.PositionMS, s.DurationMS
}

// Touch records or refreshes an active stream entry.
func (t *Tracker) Touch(clientIP, filePath, clientName string) {
	key := clientIP + "|" + filePath
	now := time.Now()

	if t.v != nil {
		var e Entry
		if raw, err := t.v.Get(context.Background(), streamKey(key)); err == nil {
			_ = json.Unmarshal([]byte(raw), &e)
		}
		if e.FirstSeen.IsZero() {
			e = Entry{FilePath: filePath, ClientIP: clientIP, FirstSeen: now}
		}
		e.LastSeen = now
		e.ClientName = clientName
		if b, err := json.Marshal(e); err == nil {
			_ = t.v.Set(context.Background(), streamKey(key), string(b), entryTTL)
		}
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.entries[key]; ok {
		e.LastSeen = now
		e.ClientName = clientName
	} else {
		t.entries[key] = &Entry{FilePath: filePath, ClientIP: clientIP, ClientName: clientName, FirstSeen: now, LastSeen: now}
	}
}

// List returns all entries seen within entryTTL. Expired in-memory entries
// are pruned on each call; Valkey entries expire automatically via TTL.
func (t *Tracker) List() []Entry {
	if t.v != nil {
		keys, err := t.v.Scan(context.Background(), "stream:entry:*")
		if err != nil || len(keys) == 0 {
			return nil
		}
		var live []Entry
		for _, k := range keys {
			raw, err := t.v.Get(context.Background(), k)
			if err != nil {
				continue
			}
			var e Entry
			if err := json.Unmarshal([]byte(raw), &e); err == nil {
				live = append(live, e)
			}
		}
		return live
	}

	cutoff := time.Now().Add(-entryTTL)
	t.mu.Lock()
	defer t.mu.Unlock()
	var live []Entry
	for k, e := range t.entries {
		if e.LastSeen.Before(cutoff) {
			delete(t.entries, k)
			continue
		}
		live = append(live, *e)
	}
	return live
}

// Middleware wraps an http.Handler and records each request in the tracker.
func (t *Tracker) Middleware(urlPrefix, diskBase string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			rel := strings.TrimPrefix(r.URL.Path, urlPrefix)
			decoded, err := url.PathUnescape(rel)
			if err != nil {
				decoded = rel
			}
			filePath := filepath.Join(diskBase, decoded)
			clientIP := r.RemoteAddr
			if host, _, err := net.SplitHostPort(clientIP); err == nil {
				clientIP = host
			}
			clientName := r.Header.Get("X-Device-Name")
			if clientName == "" {
				clientName = r.Header.Get("User-Agent")
				if idx := strings.IndexByte(clientName, '/'); idx > 0 {
					clientName = clientName[:idx]
				}
			}
			t.Touch(clientIP, filePath, clientName)
		}
		next.ServeHTTP(w, r)
	})
}

func streamKey(entryKey string) string { return "stream:entry:" + entryKey }
func posKey(id uuid.UUID) string       { return fmt.Sprintf("stream:pos:%s", id) }
