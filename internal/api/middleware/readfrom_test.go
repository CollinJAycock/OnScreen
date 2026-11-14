package middleware

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onscreen/onscreen/internal/observability"
)

// readFromRecorder is a ResponseWriter that, like net/http's connection
// writer, implements io.ReaderFrom, and records whether that fast path was
// reached.
type readFromRecorder struct {
	*httptest.ResponseRecorder
	readFromCalls int
}

func (r *readFromRecorder) ReadFrom(src io.Reader) (int64, error) {
	r.readFromCalls++
	return io.Copy(r.ResponseRecorder, src)
}

// The router stacks Recover, Logger and Metrics around every handler, file
// servers included. http.ServeContent's io.Copy only reaches the connection's
// ReadFrom (sendfile) if every wrapper exposes io.ReaderFrom and delegates to
// the next one; each wrapper used to hide it, so files went through a 32 KB
// userspace buffer.
func TestResponseWriterWrappers_PropagateReadFrom(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := observability.NewMetrics(prometheus.NewRegistry())
	body := strings.Repeat("x", 100_000)

	var sawReaderFrom bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, sawReaderFrom = w.(io.ReaderFrom)
		// What http.ServeContent does with the file.
		if _, err := io.CopyN(w, strings.NewReader(body), int64(len(body))); err != nil {
			t.Errorf("copy: %v", err)
		}
	})
	// Recover outermost, then Logger, then Metrics — the order Routes uses.
	chain := Recover(log)(Logger(log)(Metrics(m)(handler)))

	rec := &readFromRecorder{ResponseRecorder: httptest.NewRecorder()}
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/stream/x", nil))

	if !sawReaderFrom {
		t.Fatal("handler did not receive an io.ReaderFrom")
	}
	if rec.readFromCalls != 1 {
		t.Fatalf("underlying ReadFrom called %d times, want 1 (a wrapper fell back to a buffered copy)", rec.readFromCalls)
	}
	if rec.Code != http.StatusOK || rec.Body.Len() != len(body) {
		t.Fatalf("got status %d, %d bytes; want 200, %d bytes", rec.Code, rec.Body.Len(), len(body))
	}
}

// The logging wrapper still counts bytes and reports the implicit 200 when the
// body goes through ReadFrom, and falls back to a plain copy when the
// underlying writer has no ReadFrom (HTTP/2, recorders).
func TestResponseWriter_ReadFromCountsAndFallsBack(t *testing.T) {
	rec := httptest.NewRecorder() // no ReadFrom
	rw := &responseWriter{ResponseWriter: rec, status: http.StatusOK}
	n, err := rw.ReadFrom(bytes.NewReader(make([]byte, 4096)))
	if err != nil || n != 4096 {
		t.Fatalf("ReadFrom = %d, %v; want 4096, nil", n, err)
	}
	if rw.size != 4096 || rw.status != http.StatusOK {
		t.Fatalf("size %d status %d; want 4096, 200", rw.size, rw.status)
	}
	if rec.Body.Len() != 4096 || rec.Code != http.StatusOK {
		t.Fatalf("recorder got %d bytes, status %d", rec.Body.Len(), rec.Code)
	}
}

// A body copied through ReadFrom commits the response, so a later panic must
// not try to write a 500 over it.
func TestRecoverWriter_ReadFromMarksWritten(t *testing.T) {
	rw := &recoverWriter{ResponseWriter: httptest.NewRecorder()}
	if _, err := rw.ReadFrom(strings.NewReader("abc")); err != nil {
		t.Fatal(err)
	}
	if !rw.written {
		t.Fatal("ReadFrom must mark the response written")
	}
}
