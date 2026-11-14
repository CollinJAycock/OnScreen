package tvdb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TVDB responses used to be decoded with no cap, so an endless body (from the
// upstream or anything between) grew the decoder's buffer without bound.
func TestGetJSON_RefusesOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/login") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"token": "tok"}})
			return
		}
		_, _ = io.WriteString(w, `{"data":{"episodes":[{"name":"`)
		chunk := []byte(strings.Repeat("a", 1<<20))
		for sent := 0; sent < 4*maxResponseBytes; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	_, err := testClient(t, srv).GetEpisode(context.Background(), 81189, 1, 1)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("GetEpisode err = %v, want a response-size refusal", err)
	}
}
