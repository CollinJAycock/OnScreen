package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// LogRingBuffer is a fixed-capacity, thread-safe ring of recent log records.
// It wraps an inner slog.Handler so every Handle call writes through to the
// real destination (stdout JSON in production) AND captures the formatted
// JSON line in the ring. The /admin/logs endpoint reads from this ring so
// operators can pull recent server output without shell access to the host.
//
// Capacity is fixed at construction; oldest entries are evicted in O(1) when
// the ring fills. Memory ceiling is roughly capacity × line size — for
// capacity=2000 and ~500 B per line that's ~1 MB, comfortable on the heap
// without becoming the largest single allocation in the process.
type LogRingBuffer struct {
	mu      sync.Mutex
	entries []logEntry
	pos     int
	full    bool
	inner   slog.Handler
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

// NewLogRingBuffer wraps inner with a ring of the given capacity.
func NewLogRingBuffer(inner slog.Handler, capacity int) *LogRingBuffer {
	if capacity <= 0 {
		capacity = 1000
	}
	return &LogRingBuffer{
		entries: make([]logEntry, capacity),
		inner:   inner,
	}
}

// Enabled defers to the inner handler — the ring captures whatever the inner
// would emit, so disabling at the inner level (Info+ only) keeps the ring
// small and matches what operators see in stdout.
func (r *LogRingBuffer) Enabled(ctx context.Context, level slog.Level) bool {
	return r.inner.Enabled(ctx, level)
}

// Handle writes the record to the inner handler and appends it to the ring.
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
		attrs[a.Key] = v
		return true
	})
	if len(attrs) > 0 {
		_ = enc.Encode(attrs)
	}

	r.mu.Lock()
	r.entries[r.pos] = logEntry{
		t:        rec.Time,
		level:    rec.Level,
		msg:      rec.Message,
		rawAttrs: append([]byte(nil), buf.Bytes()...),
	}
	r.pos = (r.pos + 1) % len(r.entries)
	if r.pos == 0 {
		r.full = true
	}
	r.mu.Unlock()

	return r.inner.Handle(ctx, rec)
}

// WithAttrs / WithGroup delegate to the inner handler. The ring captures
// only the per-record attrs (not the bound ones) — operators reading
// /admin/logs care about the message + immediate context; the bound
// request_id / user_id are already in the per-record stream.
func (r *LogRingBuffer) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogRingBuffer{entries: r.entries, pos: r.pos, full: r.full, inner: r.inner.WithAttrs(attrs)}
}

func (r *LogRingBuffer) WithGroup(name string) slog.Handler {
	return &LogRingBuffer{entries: r.entries, pos: r.pos, full: r.full, inner: r.inner.WithGroup(name)}
}

// Snapshot returns the ring entries in oldest-to-newest order. Callers that
// want only the tail should slice from the end. Levels below minLevel are
// excluded; pass slog.LevelDebug to include everything.
func (r *LogRingBuffer) Snapshot(minLevel slog.Level) []LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()

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
