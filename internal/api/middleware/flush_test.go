package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// flushRecorder counts Flush calls that actually reach the underlying writer.
type flushRecorder struct {
	http.ResponseWriter
	flushed int
}

func (f *flushRecorder) Flush() { f.flushed++ }

// A streaming handler's Flush must propagate all the way to the socket through
// the Recover + Logger response-writer wrappers, in the order the router stacks
// them. recoverWriter previously lacked a Flush method, breaking the chain so
// SSE (/notifications/stream) and HLS bytes never reached non-browser clients
// until Go's write buffer filled.
func TestResponseWriterWrappers_PropagateFlush(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("handler did not receive an http.Flusher")
			return
		}
		_, _ = io.WriteString(w, ": keepalive\n\n")
		f.Flush()
	})

	// Recover is outermost, then Logger — the same order Routes uses.
	chain := Recover(log)(Logger(log)(handler))

	rec := &flushRecorder{ResponseWriter: httptest.NewRecorder()}
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/notifications/stream", nil))

	if rec.flushed == 0 {
		t.Fatal("Flush never reached the underlying writer — a wrapper in the chain swallowed it")
	}
}

// recoverWriter must implement http.Flusher, or it silently breaks the
// flush-delegation chain for every streaming response behind Recover.
func TestRecoverWriter_IsFlusher(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var sawFlusher bool
	h := Recover(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, sawFlusher = w.(http.Flusher)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !sawFlusher {
		t.Error("recoverWriter must implement http.Flusher for SSE/HLS streaming")
	}
}
