package tmdb

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// TestRedactKey pins that a transport error never carries the API key: a
// *url.Error embeds the request URL (with ?api_key=) in its message, which
// callers log.
func TestRedactKey(t *testing.T) {
	c := &Client{apiKey: "s3cr3t-key"}
	err := &url.Error{Op: "Get", URL: "https://api.themoviedb.org/3/movie/1?api_key=s3cr3t-key&language=en", Err: errors.New("dial tcp: timeout")}
	got := c.redactKey(err).Error()
	if strings.Contains(got, "s3cr3t-key") {
		t.Fatalf("API key leaked in error string: %s", got)
	}
	if !strings.Contains(got, "REDACTED") {
		t.Errorf("expected a redaction marker; got %s", got)
	}
}
