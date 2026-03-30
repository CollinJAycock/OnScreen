package middleware

import (
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
						"panic", v,
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
