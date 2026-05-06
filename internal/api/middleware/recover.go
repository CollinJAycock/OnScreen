package middleware

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// recoverWriter tracks whether headers have been sent to the client.
type recoverWriter struct {
	http.ResponseWriter
	written bool
}

func (rw *recoverWriter) WriteHeader(code int) {
	rw.written = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *recoverWriter) Write(b []byte) (int, error) {
	rw.written = true
	return rw.ResponseWriter.Write(b)
}

func (rw *recoverWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// sanitizePanicValue extracts a safe-to-log representation of a panic
// value. The full value can be a third-party error wrapping sensitive
// state — e.g. an LDAP `Bind` error includes the bind DN and (in some
// adapters) the password attempt; a database driver panic could carry
// query parameters. Logging the raw value into the admin-visible
// /admin/logs ring buffer leaks those into anyone who reads logs.
//
// Strategy: log the type unconditionally (always safe; aids triage),
// plus the message ONLY for the small set of types we know are safe
// to surface verbatim (errors with our own controlled messages,
// runtime errors, plain strings panicked from our own code). Anything
// else gets the type alone — operator can reproduce in dev with full
// detail if needed.
func sanitizePanicValue(v any) string {
	switch x := v.(type) {
	case string:
		// Likely a panic("…") from our own code — safe.
		return x
	case error:
		// runtime.Error covers nil-pointer / index-out-of-range / etc.
		// Their messages are deterministic strings ("runtime error:
		// invalid memory address or nil pointer dereference"), no
		// secret material. Other error types we treat as opaque.
		if _, ok := x.(interface{ RuntimeError() }); ok {
			return x.Error()
		}
		return fmt.Sprintf("error type=%T", v)
	default:
		return fmt.Sprintf("type=%T", v)
	}
}

// Recover catches panics in downstream handlers and returns a 500 response
// rather than crashing the process. Panics are logged with a stack trace.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rw := &recoverWriter{ResponseWriter: w}
			defer func() {
				if v := recover(); v != nil {
					stack := debug.Stack()
					logger.ErrorContext(r.Context(), "panic recovered",
						"panic", sanitizePanicValue(v),
						"stack", string(stack),
						"request_id", requestIDFromContext(r.Context()),
						"method", r.Method,
						"path", r.URL.Path,
					)
					// Drain the request body so the connection can be reused for keep-alive.
					_, _ = io.Copy(io.Discard, r.Body)
					_ = r.Body.Close()
					// Only write the error response if headers have not been sent yet.
					if !rw.written {
						http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
					}
				}
			}()
			next.ServeHTTP(rw, r)
		})
	}
}
