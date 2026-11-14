package audiodb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TheAudioDB responses used to be decoded with no cap, so an endless body grew
// the decoder's buffer without bound.
func TestGet_RefusesOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"artists":[{"strArtist":"`)
		chunk := []byte(strings.Repeat("a", 1<<20))
		for sent := 0; sent < 4*maxResponseBytes; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	_, err := testClient(t, srv).SearchArtist(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("SearchArtist err = %v, want a response-size refusal", err)
	}
}
