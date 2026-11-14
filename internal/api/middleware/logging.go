package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"time"
)

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (rw *responseWriter) WriteHeader(status int) {
	rw.status = status
	rw.ResponseWriter.WriteHeader(status)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	n, err := rw.ResponseWriter.Write(b)
	rw.size += n
	return n, err
}

// ReadFrom keeps the kernel's sendfile path open through this wrapper.
// http.ServeContent (direct play, downloads, local segments) copies a file
// with io.Copy, which only reaches the connection's own ReadFrom (sendfile on
// Linux) when every writer in between implements io.ReaderFrom; without this
// method each file went through a 32 KB userspace buffer instead. Bytes are
// still counted, and the status stays the implicit 200 it starts as, exactly
// as a Write without WriteHeader would leave it.
func (rw *responseWriter) ReadFrom(src io.Reader) (int64, error) {
	n, err := readFrom(rw.ResponseWriter, src)
	rw.size += int(n)
	return n, err
}

// readFrom copies src to w through w's own io.ReaderFrom when it has one (the
// net/http connection, or the next wrapper in the chain), and through a plain
// copy otherwise (HTTP/2, httptest recorders).
func readFrom(w http.ResponseWriter, src io.Reader) (int64, error) {
	if rf, ok := w.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(w, src)
}

// Flush delegates to the underlying writer if it implements http.Flusher.
func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying http.ResponseWriter so http.ResponseController
// can discover wrapped interfaces (e.g. http.Flusher, http.Hijacker).
func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}

// Logger logs each request at INFO level with method, path, status, and duration.
// The request ID (set by RequestID middleware) is included automatically via
// the context-enriched logger.
func Logger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rw, r)

			reqID := requestIDFromContext(r.Context())
			userID := userIDFromContext(r.Context())
			duration := time.Since(start)

			attrs := []any{
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.status,
				"duration_ms", duration.Milliseconds(),
				"size", rw.size,
				"remote_addr", r.RemoteAddr,
			}
			if userID != "" {
				attrs = append(attrs, "user_id", userID)
			}

			logger.InfoContext(r.Context(), "request", attrs...)
		})
	}
}
