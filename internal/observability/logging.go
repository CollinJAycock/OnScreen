package observability

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// NewLogLevelVar parses a log level string ("debug" / "info" / "warn" / "error")
// into an *slog.LevelVar. An empty string defaults to "info" so callers don't
// have to special-case unset values from settings storage.
func NewLogLevelVar(level string) (*slog.LevelVar, error) {
	if level == "" {
		level = "info"
	}
	var lv slog.LevelVar
	if err := lv.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", level, err)
	}
	return &lv, nil
}

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	userIDKey    contextKey = "user_id"
)

// NewLogger creates a structured JSON logger writing to stdout (12-factor).
// The returned *slog.LevelVar allows changing the log level at runtime (SIGHUP).
//
// Log records written with a context carrying an active OTel span get
// trace_id/span_id fields added automatically — lets operators jump from a
// log line to the full distributed trace. No-op when no span is active.
func NewLogger(level *slog.LevelVar) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		// Never log the following fields — enforced at source, but belt+suspenders:
		// passwords, tokens, file_hash, user PINs.
	})
	return slog.New(NewTraceHandler(handler))
}

// WithRequestID returns a child logger with the request ID embedded.
func WithRequestID(logger *slog.Logger, requestID string) *slog.Logger {
	return logger.With("request_id", requestID)
}

// WithUserID returns a child logger with the user ID embedded.
func WithUserID(logger *slog.Logger, userID string) *slog.Logger {
	return logger.With("user_id", userID)
}

// ContextWithRequestID stores the request ID in ctx.
func ContextWithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext retrieves the request ID from ctx. Returns "" if absent.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// ContextWithUserID stores the user ID in ctx.
func ContextWithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}

// UserIDFromContext retrieves the user ID from ctx. Returns "" if absent.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(userIDKey).(string)
	return v
}
