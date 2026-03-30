// Package streaming provides a lightweight in-memory tracker for active
// direct-play HTTP streams (i.e. clients hitting /media/files/* directly).
package streaming

import (
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	entryTTL        = 45 * time.Second
	maxStreamEntries = 10000
)

// Entry represents one active direct-play stream.
type Entry struct {
	FilePath   string
	ClientIP   string
	ClientName string
	FirstSeen  time.Time
	LastSeen   time.Time
}

// Tracker records and expires direct-play stream activity.
type Tracker struct {
	mu      sync.Mutex
	entries map[string]*Entry // key: clientIP + "|" + filePath

	posMu          sync.Mutex
	positionByItem map[uuid.UUID]itemPlayState // mediaItemID → latest play state
}

// NewTracker creates a Tracker.
func NewTracker() *Tracker {
	return &Tracker{
		entries:        make(map[string]*Entry),
		positionByItem: make(map[uuid.UUID]itemPlayState),
	}
}

const maxPositionEntries = 10000

type itemPlayState struct {
	positionMS int64
	durationMS int64
	lastUpdate time.Time
}

// SetItemState records the latest playback position and duration for a media item.
// Called by the progress endpoint on each player heartbeat.
func (t *Tracker) SetItemState(mediaItemID uuid.UUID, positionMS, durationMS int64) {
	t.posMu.Lock()
	defer t.posMu.Unlock()

	t.positionByItem[mediaItemID] = itemPlayState{
		positionMS: positionMS,
		durationMS: durationMS,
		lastUpdate: time.Now(),
	}

	// Evict stale entries when the map grows too large.
	if len(t.positionByItem) > maxPositionEntries {
		t.evictOldPositions()
	}
}

// evictOldPositions removes entries older than entryTTL. Must be called with posMu held.
func (t *Tracker) evictOldPositions() {
	cutoff := time.Now().Add(-entryTTL)
	for id, s := range t.positionByItem {
		if s.lastUpdate.Before(cutoff) {
			delete(t.positionByItem, id)
		}
	}
}

// GetItemState returns the last known playback position and duration for a media item.
// Both values are 0 if no state has been recorded.
func (t *Tracker) GetItemState(mediaItemID uuid.UUID) (positionMS, durationMS int64) {
	t.posMu.Lock()
	s := t.positionByItem[mediaItemID]
	t.posMu.Unlock()
	return s.positionMS, s.durationMS
}

// Touch records or refreshes an active stream entry.
func (t *Tracker) Touch(clientIP, filePath, clientName string) {
	key := clientIP + "|" + filePath
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.entries[key]; ok {
		e.LastSeen = now
		e.ClientName = clientName
	} else {
		t.entries[key] = &Entry{
			FilePath:   filePath,
			ClientIP:   clientIP,
			ClientName: clientName,
			FirstSeen:  now,
			LastSeen:   now,
		}
	}
	// Evict expired entries if the map grows too large.
	if len(t.entries) > maxStreamEntries {
		cutoff := now.Add(-entryTTL)
		for k, e := range t.entries {
			if e.LastSeen.Before(cutoff) {
				delete(t.entries, k)
			}
		}
	}
}

// List returns all entries that have been seen within entryTTL.
// Expired entries are pruned on each call.
func (t *Tracker) List() []Entry {
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
// urlPrefix is stripped from r.URL.Path; diskBase is prepended to get the
// absolute file path that matches media_files.file_path in the DB.
func (t *Tracker) Middleware(urlPrefix, diskBase string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			rel := strings.TrimPrefix(r.URL.Path, urlPrefix)
			// URL-decode so the path matches what's stored in the DB.
			decoded, err := url.PathUnescape(rel)
			if err != nil {
				decoded = rel
			}
			filePath := filepath.Join(diskBase, decoded)

			clientIP := r.RemoteAddr
			// Strip port — keeps the key stable across range requests.
			// chi's RealIP middleware already sets RemoteAddr to the real client IP.
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
