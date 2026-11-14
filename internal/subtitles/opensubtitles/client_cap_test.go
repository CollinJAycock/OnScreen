package opensubtitles

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The API response used to be decoded with no cap, so an endpoint streaming an
// endless body grew the decoder's buffer until the process died.
func TestDo_RefusesOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"attributes":{"release":"`)
		chunk := []byte(strings.Repeat("a", 1<<20))
		for sent := 0; sent < 4*maxJSONResponseBytes; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	_, err := newTestClient("k1", "", "", srv).Search(context.Background(), SearchOpts{Query: "x"})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Search err = %v, want a response-size refusal", err)
	}
}
