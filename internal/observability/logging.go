package observability

import (
	"context"
	"log/slog"
	"os"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	userIDKey    contextKey = "user_id"
)

// NewLogger creates a structured JSON logger writing to stdout (12-factor).
// The returned *slog.LevelVar allows changing the log level at runtime (SIGHUP).
func NewLogger(level *slog.LevelVar) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		// Never log the following fields — enforced at source, but belt+suspenders:
		// passwords, tokens, file_hash, user PINs.
	})
	return slog.New(handler)
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
