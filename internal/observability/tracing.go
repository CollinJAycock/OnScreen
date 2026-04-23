package observability

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TracerOptions is the configuration for NewTracerProvider. ServiceName should
// differ per binary (e.g. "onscreen" vs "onscreen-worker") so spans group
// correctly in the trace UI.
type TracerOptions struct {
	Endpoint       string
	ServiceName    string
	ServiceVersion string
	DeploymentEnv  string
	SampleRatio    float64
}

// NewTracerProvider builds an OTLP/gRPC-backed tracer provider and registers
// it as the global tracer + propagator.
//
// Returns (nil, nil) when opts.Endpoint is empty — the global tracer provider
// stays at OTel's built-in no-op, so tracer.Start() returns cheap no-op spans
// and instrumentation is effectively free. Callers should pass the returned
// value to ShutdownTracer unconditionally (it tolerates nil).
//
// The endpoint must include a scheme (http:// or https://); TLS is enabled
// automatically for https.
func NewTracerProvider(ctx context.Context, opts TracerOptions) (*sdktrace.TracerProvider, error) {
	if opts.Endpoint == "" {
		return nil, nil
	}

	exp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(opts.Endpoint))
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	attrs := []attribute.KeyValue{
		attribute.String("service.name", opts.ServiceName),
	}
	if opts.ServiceVersion != "" {
		attrs = append(attrs, attribute.String("service.version", opts.ServiceVersion))
	}
	if opts.DeploymentEnv != "" {
		attrs = append(attrs, attribute.String("deployment.environment", opts.DeploymentEnv))
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(attrs...))
	if err != nil {
		return nil, fmt.Errorf("merge resource: %w", err)
	}

	// Clamp ratio to [0,1]. 0 = drop everything (effectively disables tracing
	// even with endpoint set); 1 = sample everything (dev default).
	ratio := opts.SampleRatio
	if ratio < 0 {
		ratio = 0
	} else if ratio > 1 {
		ratio = 1
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp, nil
}

// ShutdownTracer flushes buffered spans and closes tp. Safe to call with nil.
// Uses a bounded timeout so shutdown can't hang the process on a dead collector.
func ShutdownTracer(ctx context.Context, tp *sdktrace.TracerProvider) {
	if tp == nil {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := tp.Shutdown(sctx); err != nil {
		slog.Default().Error("otel tracer shutdown failed", "err", err)
	}
}
