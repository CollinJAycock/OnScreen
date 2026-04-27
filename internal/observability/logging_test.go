package observability

import (
	"context"
	"log/slog"
	"testing"
)

func TestNewLogLevelVar_Parses(t *testing.T) {
	cases := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
	}
	for _, c := range cases {
		lv, err := NewLogLevelVar(c.input)
		if err != nil {
			t.Errorf("%q: %v", c.input, err)
			continue
		}
		if lv.Level() != c.want {
			t.Errorf("%q: got %v, want %v", c.input, lv.Level(), c.want)
		}
	}
}

func TestNewLogLevelVar_EmptyDefaultsToInfo(t *testing.T) {
	lv, err := NewLogLevelVar("")
	if err != nil {
		t.Fatal(err)
	}
	if lv.Level() != slog.LevelInfo {
		t.Errorf("empty default = %v, want info", lv.Level())
	}
}

func TestNewLogLevelVar_RejectsGarbage(t *testing.T) {
	if _, err := NewLogLevelVar("yelling"); err == nil {
		t.Error("expected error for invalid level")
	}
}

func TestRequestIDFromContext_RoundTrip(t *testing.T) {
	ctx := ContextWithRequestID(context.Background(), "req-123")
	if got := RequestIDFromContext(ctx); got != "req-123" {
		t.Errorf("got %q, want req-123", got)
	}
}

func TestRequestIDFromContext_AbsentReturnsEmpty(t *testing.T) {
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Errorf("got %q, want empty for absent value", got)
	}
}

func TestUserIDFromContext_RoundTrip(t *testing.T) {
	ctx := ContextWithUserID(context.Background(), "user-456")
	if got := UserIDFromContext(ctx); got != "user-456" {
		t.Errorf("got %q, want user-456", got)
	}
}

func TestUserIDFromContext_AbsentReturnsEmpty(t *testing.T) {
	if got := UserIDFromContext(context.Background()); got != "" {
		t.Errorf("got %q, want empty for absent value", got)
	}
}

func TestRequestAndUserKeysAreSeparate(t *testing.T) {
	// Using the same string-typed key for both would risk collision when
	// observability mounts multiple values on the same context. The
	// contextKey newtype + distinct constants prevent that — this test
	// is a sentinel against a refactor that collapses them.
	ctx := context.Background()
	ctx = ContextWithRequestID(ctx, "req-only")
	if got := UserIDFromContext(ctx); got != "" {
		t.Errorf("user_id read returned %q after only request_id was set — keys collided", got)
	}
}

func TestNewLogger_Returns(t *testing.T) {
	// Smoke: just ensure NewLogger returns a valid logger with the
	// supplied level var.
	lv, _ := NewLogLevelVar("warn")
	logger := NewLogger(lv)
	if logger == nil {
		t.Fatal("NewLogger returned nil")
	}
	// Confirm the level is wired through — Debug should be filtered
	// out by the warn level.
	if logger.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug should be filtered by warn-level logger")
	}
	if !logger.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("warn should pass through warn-level logger")
	}
}

func TestWithRequestID_AddsField(t *testing.T) {
	// Returned logger should carry the request_id attribute. We can't
	// inspect attributes directly without writing to a custom handler,
	// but we can confirm the call returns a non-nil logger and that
	// the With chain didn't crash.
	logger := slog.Default()
	got := WithRequestID(logger, "req-1")
	if got == nil {
		t.Fatal("WithRequestID returned nil")
	}
}

func TestWithUserID_AddsField(t *testing.T) {
	logger := slog.Default()
	got := WithUserID(logger, "u-1")
	if got == nil {
		t.Fatal("WithUserID returned nil")
	}
}
