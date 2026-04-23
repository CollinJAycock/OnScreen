package observability

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// traceHandler wraps a slog.Handler and adds trace_id/span_id fields from the
// context when an OTel span is active. Lets operators pivot from a log line
// to the full distributed trace in one click.
//
// Zero overhead when no span is recording: SpanContextFromContext on a bare
// context returns an invalid SpanContext, which short-circuits in O(1).
type traceHandler struct {
	inner slog.Handler
}

// NewTraceHandler wraps inner so every Handle call attaches the current span's
// trace_id and span_id (if any) to the log record.
func NewTraceHandler(inner slog.Handler) slog.Handler {
	return &traceHandler{inner: inner}
}

func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{inner: h.inner.WithGroup(name)}
}
