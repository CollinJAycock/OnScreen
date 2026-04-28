package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// noopHandler is the inner handler used in tests where we only care about
// what the ring captures, not what stdout sees.
type noopHandler struct{ enabled slog.Level }

func (n *noopHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= n.enabled }
func (n *noopHandler) Handle(_ context.Context, _ slog.Record) error { return nil }
func (n *noopHandler) WithAttrs(_ []slog.Attr) slog.Handler          { return n }
func (n *noopHandler) WithGroup(_ string) slog.Handler               { return n }

func newRing(capacity int, level slog.Level) *LogRingBuffer {
	return NewLogRingBuffer(&noopHandler{enabled: level}, capacity)
}

// emit writes a single record at the given level + message through the
// slog.Logger so the buffer's Handle path is exercised exactly the way
// production traffic exercises it.
func emit(t *testing.T, l *slog.Logger, lvl slog.Level, msg string, attrs ...any) {
	t.Helper()
	switch lvl {
	case slog.LevelDebug:
		l.Debug(msg, attrs...)
	case slog.LevelInfo:
		l.Info(msg, attrs...)
	case slog.LevelWarn:
		l.Warn(msg, attrs...)
	case slog.LevelError:
		l.Error(msg, attrs...)
	}
}

func TestLogRingBuffer_AppendsInOrder(t *testing.T) {
	buf := newRing(10, slog.LevelDebug)
	logger := slog.New(buf)

	emit(t, logger, slog.LevelInfo, "first")
	emit(t, logger, slog.LevelInfo, "second")
	emit(t, logger, slog.LevelInfo, "third")

	got := buf.Snapshot(slog.LevelDebug)
	if len(got) != 3 {
		t.Fatalf("snapshot len: got %d, want 3", len(got))
	}
	if got[0].Message != "first" || got[1].Message != "second" || got[2].Message != "third" {
		t.Errorf("order wrong: %+v", got)
	}
}

func TestLogRingBuffer_WrapsAtCapacity(t *testing.T) {
	buf := newRing(3, slog.LevelDebug)
	logger := slog.New(buf)

	for i := 0; i < 5; i++ {
		emit(t, logger, slog.LevelInfo, "msg", "n", i)
	}

	got := buf.Snapshot(slog.LevelDebug)
	if len(got) != 3 {
		t.Fatalf("after 5 writes to cap=3 ring: got %d entries, want 3 (oldest evicted)", len(got))
	}
	// After 5 writes, the surviving entries are n=2, n=3, n=4.
	wantNs := []float64{2, 3, 4}
	for i, e := range got {
		v, ok := e.Attrs["n"].(float64)
		if !ok {
			t.Fatalf("entry %d: missing n attr or wrong type: %+v", i, e.Attrs)
		}
		if v != wantNs[i] {
			t.Errorf("entry %d: got n=%v, want %v", i, v, wantNs[i])
		}
	}
}

func TestLogRingBuffer_FiltersByLevel(t *testing.T) {
	buf := newRing(10, slog.LevelDebug)
	logger := slog.New(buf)

	emit(t, logger, slog.LevelDebug, "dbg")
	emit(t, logger, slog.LevelInfo, "inf")
	emit(t, logger, slog.LevelWarn, "wrn")
	emit(t, logger, slog.LevelError, "err")

	if got := buf.Snapshot(slog.LevelInfo); len(got) != 3 {
		t.Errorf("level=info: got %d entries, want 3 (info+warn+error)", len(got))
	}
	if got := buf.Snapshot(slog.LevelWarn); len(got) != 2 {
		t.Errorf("level=warn: got %d entries, want 2", len(got))
	}
	if got := buf.Snapshot(slog.LevelError); len(got) != 1 {
		t.Errorf("level=error: got %d entries, want 1", len(got))
	}
}

func TestLogRingBuffer_RespectsInnerEnabled(t *testing.T) {
	// Inner handler suppresses Debug — the ring should never see those
	// records since slog.Logger short-circuits on Enabled() == false.
	buf := newRing(10, slog.LevelInfo)
	logger := slog.New(buf)

	emit(t, logger, slog.LevelDebug, "dbg")
	emit(t, logger, slog.LevelInfo, "inf")

	got := buf.Snapshot(slog.LevelDebug)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1 (debug filtered before reaching ring)", len(got))
	}
	if got[0].Message != "inf" {
		t.Errorf("got message %q, want inf", got[0].Message)
	}
}

func TestLogRingBuffer_WritesThroughToInner(t *testing.T) {
	// Use a real JSON handler so we can confirm production output isn't
	// swallowed by the ring wrapper.
	var stdout bytes.Buffer
	inner := slog.NewJSONHandler(&stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	buf := NewLogRingBuffer(inner, 10)
	logger := slog.New(buf)

	emit(t, logger, slog.LevelInfo, "stdout-check")

	if !strings.Contains(stdout.String(), "stdout-check") {
		t.Errorf("inner handler didn't receive record: %q", stdout.String())
	}
	if got := buf.Snapshot(slog.LevelDebug); len(got) != 1 {
		t.Errorf("ring missed record: got %d entries", len(got))
	}
}

func TestLogRingBuffer_WithAttrs_PropagatesToInner(t *testing.T) {
	// Bound attrs should appear in stdout output (proves we delegate to
	// inner.WithAttrs) — they aren't required to appear in the ring.
	var stdout bytes.Buffer
	inner := slog.NewJSONHandler(&stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	buf := NewLogRingBuffer(inner, 10)
	logger := slog.New(buf).With("request_id", "rid-42")

	logger.Info("with-attrs")

	if !strings.Contains(stdout.String(), `"request_id":"rid-42"`) {
		t.Errorf("bound attr missing from stdout: %q", stdout.String())
	}
}

func TestLogRingBuffer_RecordsAttrs(t *testing.T) {
	buf := newRing(10, slog.LevelDebug)
	logger := slog.New(buf)

	logger.Error("oops", "code", "E_CONN", "session", "abc-123")

	got := buf.Snapshot(slog.LevelDebug)
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].Attrs["code"] != "E_CONN" {
		t.Errorf("code attr: got %v, want E_CONN", got[0].Attrs["code"])
	}
	if got[0].Attrs["session"] != "abc-123" {
		t.Errorf("session attr: got %v, want abc-123", got[0].Attrs["session"])
	}
	if got[0].Level != "ERROR" {
		t.Errorf("level: got %q, want ERROR", got[0].Level)
	}
}

func TestLogRingBuffer_TimeIsRecorded(t *testing.T) {
	buf := newRing(10, slog.LevelDebug)
	logger := slog.New(buf)

	before := time.Now()
	logger.Info("t-test")
	after := time.Now()

	got := buf.Snapshot(slog.LevelDebug)
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].Time.Before(before) || got[0].Time.After(after) {
		t.Errorf("time %v outside [%v, %v]", got[0].Time, before, after)
	}
}

func TestLogRingBuffer_ErrorAttrStringified(t *testing.T) {
	// Plain `error` interface values JSON-marshal to "{}" (no exported
	// fields), so the buffer must stringify them up-front. Otherwise
	// /admin/logs returns "err":{} for the most diagnostically useful
	// log line: an error.
	buf := newRing(10, slog.LevelDebug)
	logger := slog.New(buf)
	logger.Error("boom", "err", errExample{msg: "connection refused"})

	got := buf.Snapshot(slog.LevelDebug)
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].Attrs["err"] != "connection refused" {
		t.Errorf("err attr: got %v (%T), want \"connection refused\" string",
			got[0].Attrs["err"], got[0].Attrs["err"])
	}
}

type errExample struct{ msg string }

func (e errExample) Error() string { return e.msg }

func TestLogRingBuffer_EmptySnapshot(t *testing.T) {
	buf := newRing(10, slog.LevelDebug)
	if got := buf.Snapshot(slog.LevelDebug); len(got) != 0 {
		t.Errorf("empty ring: got %d entries, want 0", len(got))
	}
}

func TestLogRingBuffer_DefaultCapacityOnZero(t *testing.T) {
	// Zero / negative capacity should fall back to a sensible default
	// (1000) instead of producing a zero-length ring that panics on
	// the first write.
	buf := NewLogRingBuffer(&noopHandler{enabled: slog.LevelDebug}, 0)
	logger := slog.New(buf)
	logger.Info("smoke") // would panic with cap=0

	if got := buf.Snapshot(slog.LevelDebug); len(got) != 1 {
		t.Errorf("default-cap buffer: got %d entries, want 1", len(got))
	}
}
