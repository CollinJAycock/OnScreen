package arr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// endlessJSON starts a JSON string and never finishes it, stopping only when
// the client hangs up (or at limit, so a client that ignores the cap fails the
// test instead of hanging it).
func endlessJSON(limit int64) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"appName":"`)
		chunk := []byte(strings.Repeat("a", 1<<20))
		for sent := int64(0); sent < limit; sent += int64(len(chunk)) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}
}

// A success body used to be decoded with no cap: an upstream streaming an
// endless body grew the decoder's buffer until the process died.
func TestDo_RefusesOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(endlessJSON(4 * maxResponseBytes))
	defer srv.Close()
	_, err := newTestClient(srv, "k").Ping(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Ping err = %v, want a response-size refusal", err)
	}
}
