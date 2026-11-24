package v1

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
)

// A JSON error must not answer 502 or 504: Cloudflare replaces the body of
// an origin 502/504 with its own HTML page, so the code and the translated
// message never reach the app (docs/cloudflare-5xx.md). An outside service
// failing answers 503; the server's own failure 500.
//
// Allowed: replies whose clients act on the status alone and never read a
// body — the worker segment relay (players retry a 5xx segment) and the
// live-TV playlist's tuner-not-ready 504 (the TV apps map the status).
var gatewayStatusAllowed = map[string]int{
	"transcode.go":     2, // proxyWorkerFile: plain-text segment relay
	"livetv_stream.go": 1, // STREAM_NOT_READY on the HLS playlist
}

func TestNoGatewayStatusesOnJSONErrors(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		n := strings.Count(string(src), "http.StatusBadGateway") + strings.Count(string(src), "http.StatusGatewayTimeout")
		if n != gatewayStatusAllowed[f] {
			t.Errorf("%s: %d uses of http.StatusBadGateway / http.StatusGatewayTimeout, want %d: answer 503 for an outside service failing (Cloudflare replaces a 502/504 body)", f, n, gatewayStatusAllowed[f])
		}
	}
}

// TMDB down and TMDB not configured both answer 503 now; the code tells
// them apart.
func TestDiscoverSeasons_TMDBDownSaysSo(t *testing.T) {
	uid := uuid.New()
	h := NewDiscoverHandler(nil, nil, nil, slog.Default()).
		WithSeasons(&fakeSeasonTMDB{err: errors.New("boom")}, seasonDBFixture())
	rec := httptest.NewRecorder()
	h.Seasons(rec, seasonsRequest(uid, "1399", &auth.Claims{UserID: uid}))
	if rec.Code != http.StatusServiceUnavailable || issueErrorCode(t, rec) != "TMDB_LOOKUP_FAILED" {
		t.Errorf("status %d code %q, want 503 TMDB_LOOKUP_FAILED (%s)", rec.Code, issueErrorCode(t, rec), rec.Body)
	}
}
