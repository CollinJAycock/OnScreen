package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// recordingHandler captures slog records into a buffer for assertions.
func newRecordingHandler() (slog.Handler, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}), &buf
}

func TestTraceHandler_NoSpanLeavesRecordUntouched(t *testing.T) {
	inner, buf := newRecordingHandler()
	h := NewTraceHandler(inner)
	logger := slog.New(h)

	logger.InfoContext(context.Background(), "no-span message", "k", "v")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (raw=%s)", err, buf.String())
	}
	if _, ok := got["trace_id"]; ok {
		t.Errorf("trace_id leaked into record without an active span: %v", got)
	}
	if _, ok := got["span_id"]; ok {
		t.Errorf("span_id leaked into record without an active span: %v", got)
	}
}

func TestTraceHandler_AddsTraceFieldsWhenSpanActive(t *testing.T) {
	// Construct a SpanContext directly and stash it on a context, then
	// log through the trace handler. Real spans come from a TracerProvider
	// but we don't need one — SpanContextFromContext + ContextWithSpanContext
	// give us the wire-level test surface.
	traceID, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	spanID, _ := trace.SpanIDFromHex("aabbccddeeff0011")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	inner, buf := newRecordingHandler()
	h := NewTraceHandler(inner)
	logger := slog.New(h)

	logger.InfoContext(ctx, "span message")

	if !strings.Contains(buf.String(), "0102030405060708090a0b0c0d0e0f10") {
		t.Errorf("trace_id not in output: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "aabbccddeeff0011") {
		t.Errorf("span_id not in output: %s", buf.String())
	}
}

func TestTraceHandler_EnabledDelegates(t *testing.T) {
	inner, _ := newRecordingHandler() // JSONHandler is debug-level
	h := NewTraceHandler(inner)
	if !h.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Enabled(debug) should pass through to inner debug-level handler")
	}
}

func TestTraceHandler_WithAttrsPreservesWrap(t *testing.T) {
	// WithAttrs / WithGroup return a NEW traceHandler so trace_id
	// continues to be added through the chain.
	traceID, _ := trace.TraceIDFromHex("11223344556677889900aabbccddeeff")
	spanID, _ := trace.SpanIDFromHex("0011223344556677")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	inner, buf := newRecordingHandler()
	h := NewTraceHandler(inner).WithAttrs([]slog.Attr{slog.String("svc", "test")})
	logger := slog.New(h)

	logger.InfoContext(ctx, "with-attrs message")

	out := buf.String()
	if !strings.Contains(out, "11223344556677889900aabbccddeeff") {
		t.Errorf("trace_id missing through WithAttrs chain: %s", out)
	}
	if !strings.Contains(out, "\"svc\":\"test\"") {
		t.Errorf("WithAttrs payload missing from chain: %s", out)
	}
}

func TestTraceHandler_WithGroupPreservesWrap(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("11223344556677889900aabbccddeeff")
	spanID, _ := trace.SpanIDFromHex("0011223344556677")
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	inner, buf := newRecordingHandler()
	h := NewTraceHandler(inner).WithGroup("subsystem")
	logger := slog.New(h)

	logger.InfoContext(ctx, "grouped message", "k", "v")
	out := buf.String()
	if !strings.Contains(out, "11223344556677889900aabbccddeeff") {
		t.Errorf("trace_id missing through WithGroup chain: %s", out)
	}
}
