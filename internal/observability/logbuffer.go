package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// LogRingBuffer is a fixed-capacity, thread-safe ring of recent log records.
// It wraps an inner slog.Handler so every Handle call writes through to the
// real destination (stdout JSON in production) AND captures the formatted
// JSON line in a ring. The /admin/logs endpoint reads from this ring so
// operators can pull recent server output without shell access to the host.
//
// It keeps TWO rings:
//
//   - all:  every record the inner handler emits (INFO+ in production). This is
//     the "recent activity" view. Under load it's dominated by high-volume INFO
//     (request logs, per-item scan/enrichment lines) and wraps within minutes —
//     that's fine, it's meant to show what's happening *now*.
//   - warn: WARN and ERROR records only. Warnings/errors are rare, so this ring
//     retains them for a long time REGARDLESS of INFO volume. Without it, a
//     burst of INFO evicts a warning within minutes and an operator pulling
//     `?level=warn` later sees nothing — the exact failure mode that made this
//     buffer useless for diagnosing intermittent transcode/playback issues.
//
// A WARN-or-higher Snapshot reads the warn ring; anything lower reads all.
//
// Capacity is fixed at construction; oldest entries are evicted in O(1) when a
// ring fills. Memory ceiling is roughly 2 × capacity × line size — for
// capacity=2000 and ~500 B per line that's ~2 MB, comfortable on the heap.
// LogRingBuffer fields are POINTERS so that handlers derived via WithAttrs/
// WithGroup share the same mutex and ring backing — only `inner` differs per
// derivation. Value-copying the rings (the previous bug) gave each derived
// handler its own mutex while sharing the same backing array, so concurrent
// logging through a root + a `.With(...)`-derived logger raced on that array
// under different locks.
type LogRingBuffer struct {
	mu    *sync.Mutex
	all   *ringBuf // all levels — recent-activity view, INFO-dominated
	warn  *ringBuf // WARN/ERROR only — durable; an INFO flood can't evict it
	inner slog.Handler
}

// ringBuf is a fixed-capacity ring of log entries. It is not safe for
// concurrent use on its own — LogRingBuffer.mu guards every access.
type ringBuf struct {
	entries []logEntry
	pos     int
	full    bool
}

// push appends e, evicting the oldest entry when the ring is full.
func (r *ringBuf) push(e logEntry) {
	r.entries[r.pos] = e
	r.pos = (r.pos + 1) % len(r.entries)
	if r.pos == 0 {
		r.full = true
	}
}

// scan returns the ring's entries oldest-to-newest, excluding levels below
// minLevel.
func (r *ringBuf) scan(minLevel slog.Level) []LogEntry {
	count := r.pos
	start := 0
	if r.full {
		count = len(r.entries)
		start = r.pos
	}
	out := make([]LogEntry, 0, count)
	for i := 0; i < count; i++ {
		e := r.entries[(start+i)%len(r.entries)]
		if e.level < minLevel {
			continue
		}
		entry := LogEntry{
			Time:    e.t,
			Level:   e.level.String(),
			Message: e.msg,
		}
		if len(e.rawAttrs) > 0 {
			var attrs map[string]any
			if err := json.Unmarshal(e.rawAttrs, &attrs); err == nil {
				entry.Attrs = attrs
			}
		}
		out = append(out, entry)
	}
	return out
}

// LogEntry is the public shape returned to API consumers. Fields mirror the
// JSON keys slog emits so the API response matches what operators see in
// stdout.
type LogEntry struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"msg"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

type logEntry struct {
	t     time.Time
	level slog.Level
	msg   string
	// raw JSON-encoded attrs from the inner handler — re-decoded on
	// Snapshot rather than parsed up-front to keep Handle() cheap.
	rawAttrs []byte
}

// sensitiveLogKeySubstrings name attribute keys whose values are redacted
// before entering the admin-readable log ring. Kept specific (api_key, not a
// bare "key") so common non-secret keys (cache_key, request_id) aren't hit.
var sensitiveLogKeySubstrings = []string{
	"password", "secret", "token", "authorization", "api_key", "apikey", "credential",
}

func isSensitiveLogKey(k string) bool {
	k = strings.ToLower(k)
	for _, s := range sensitiveLogKeySubstrings {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// NewLogRingBuffer wraps inner with rings of the given capacity (one for all
// records, one for WARN+).
func NewLogRingBuffer(inner slog.Handler, capacity int) *LogRingBuffer {
	if capacity <= 0 {
		capacity = 1000
	}
	return &LogRingBuffer{
		mu:    &sync.Mutex{},
		all:   &ringBuf{entries: make([]logEntry, capacity)},
		warn:  &ringBuf{entries: make([]logEntry, capacity)},
		inner: inner,
	}
}

// Enabled defers to the inner handler — the ring captures whatever the inner
// would emit, so disabling at the inner level (Info+ only) keeps the ring
// small and matches what operators see in stdout.
func (r *LogRingBuffer) Enabled(ctx context.Context, level slog.Level) bool {
	return r.inner.Enabled(ctx, level)
}

// Handle writes the record to the inner handler and appends it to the ring(s).
// Inner-write errors are returned so the caller (slog) sees the same outcome
// it would without us in the chain.
func (r *LogRingBuffer) Handle(ctx context.Context, rec slog.Record) error {
	// Capture attrs as JSON. Cheaper than walking the slog.Attrs into a map
	// because slog.Record.Attrs is iterator-based and JSON is what the
	// admin endpoint will emit anyway.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	attrs := map[string]any{}
	rec.Attrs(func(a slog.Attr) bool {
		// error values come through as opaque interfaces and JSON-
		// marshal to "{}" because the standard error structs have no
		// exported fields. Stringify here so the API surfaces the
		// actual message — that's the whole reason an admin would
		// pull logs.
		v := a.Value.Any()
		if e, ok := v.(error); ok {
			v = e.Error()
		}
		// Belt-and-suspenders: source code already avoids logging secrets, but
		// redact by key name so a stray attr can't leak one into the
		// admin-readable ring.
		if isSensitiveLogKey(a.Key) {
			v = "[redacted]"
		}
		attrs[a.Key] = v
		return true
	})
	if len(attrs) > 0 {
		_ = enc.Encode(attrs)
	}

	e := logEntry{
		t:        rec.Time,
		level:    rec.Level,
		msg:      rec.Message,
		rawAttrs: append([]byte(nil), buf.Bytes()...),
	}

	r.mu.Lock()
	r.all.push(e)
	// Mirror warnings/errors into their own ring so a high-volume INFO stream
	// (scans, request logs) can't evict them — this is what keeps a warning
	// retrievable via `?level=warn` minutes or hours after it happened.
	if rec.Level >= slog.LevelWarn {
		r.warn.push(e)
	}
	r.mu.Unlock()

	return r.inner.Handle(ctx, rec)
}

// WithAttrs / WithGroup delegate to the inner handler. The ring captures
// only the per-record attrs (not the bound ones) — operators reading
// /admin/logs care about the message + immediate context; the bound
// request_id / user_id are already in the per-record stream. The derived
// handler shares the parent's mutex AND ring pointers, so every handler in the
// tree funnels through one lock and one set of ring counters.
func (r *LogRingBuffer) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogRingBuffer{mu: r.mu, all: r.all, warn: r.warn, inner: r.inner.WithAttrs(attrs)}
}

func (r *LogRingBuffer) WithGroup(name string) slog.Handler {
	return &LogRingBuffer{mu: r.mu, all: r.all, warn: r.warn, inner: r.inner.WithGroup(name)}
}

// Snapshot returns ring entries in oldest-to-newest order. Levels below
// minLevel are excluded; pass slog.LevelDebug to include everything. A
// WARN-or-higher minLevel reads the dedicated warn ring (durable across INFO
// floods); anything lower reads the all-level ring.
func (r *LogRingBuffer) Snapshot(minLevel slog.Level) []LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if minLevel >= slog.LevelWarn {
		return r.warn.scan(minLevel)
	}
	return r.all.scan(minLevel)
}
