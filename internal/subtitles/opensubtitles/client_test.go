package opensubtitles

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/time/rate"
)

// newTestClient returns a Client whose baseURL points at the given test server
// and whose rate limiter is effectively disabled so tests don't sleep.
func newTestClient(apiKey, username, password, base string) *Client {
	return New(apiKey, username, password, "test-ua").
		WithBaseURL(base).
		WithLimiter(rate.NewLimiter(rate.Inf, 0))
}

func TestConfigured(t *testing.T) {
	if (*Client)(nil).Configured() {
		t.Fatal("nil client should report unconfigured")
	}
	if New("", "", "", "").Configured() {
		t.Fatal("empty api key should be unconfigured")
	}
	if !New("key", "", "", "").Configured() {
		t.Fatal("with api key the client should be configured")
	}
}

func TestSearchBuildsQueryAndParsesResponse(t *testing.T) {
	var captured *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Clone(r.Context())
		// Pretend-response shaped like the real API.
		_, _ = io.WriteString(w, `{
			"data": [
				{"attributes": {
					"language": "en",
					"release": "Some.Movie.2020.1080p",
					"hearing_impaired": true,
					"hd": true,
					"from_trusted": true,
					"ratings": 8.1,
					"download_count": 1234,
					"uploader": {"name": "alice"},
					"files": [{"file_id": 42, "file_name": "sub.srt"}]
				}},
				{"attributes": {"files": []}}
			]
		}`)
	}))
	defer srv.Close()

	c := newTestClient("k1", "", "", srv.URL)
	results, err := c.Search(context.Background(), SearchOpts{
		Query:     "The Movie",
		Year:      2020,
		Season:    2,
		Episode:   5,
		IMDBID:    "tt1234567",
		TMDBID:    999,
		Languages: "en,es",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected results entry without files to be filtered, got %d", len(results))
	}
	got := results[0]
	want := SearchResult{
		FileID:          42,
		FileName:        "sub.srt",
		Language:        "en",
		Release:         "Some.Movie.2020.1080p",
		HearingImpaired: true,
		HD:              true,
		FromTrusted:     true,
		Rating:          8.1,
		DownloadCount:   1234,
		UploaderName:    "alice",
	}
	if got != want {
		t.Fatalf("result mismatch\n got: %+v\nwant: %+v", got, want)
	}

	if captured == nil {
		t.Fatal("server didn't receive a request")
	}
	if captured.Header.Get("Api-Key") != "k1" {
		t.Errorf("Api-Key header missing or wrong: %q", captured.Header.Get("Api-Key"))
	}
	if captured.Header.Get("User-Agent") != "test-ua" {
		t.Errorf("User-Agent not forwarded: %q", captured.Header.Get("User-Agent"))
	}
	q := captured.URL.Query()
	if q.Get("query") != "The Movie" {
		t.Errorf("query param wrong: %q", q.Get("query"))
	}
	if q.Get("year") != "2020" {
		t.Errorf("year param wrong: %q", q.Get("year"))
	}
	if q.Get("season_number") != "2" || q.Get("episode_number") != "5" {
		t.Errorf("season/episode params wrong: %q / %q", q.Get("season_number"), q.Get("episode_number"))
	}
	if q.Get("imdb_id") != "1234567" {
		t.Errorf("imdb_id should have tt stripped, got %q", q.Get("imdb_id"))
	}
	if q.Get("tmdb_id") != "999" {
		t.Errorf("tmdb_id param wrong: %q", q.Get("tmdb_id"))
	}
	if q.Get("languages") != "en,es" {
		t.Errorf("languages param wrong: %q", q.Get("languages"))
	}
}

func TestSearchRequiresAPIKey(t *testing.T) {
	c := New("", "", "", "")
	_, err := c.Search(context.Background(), SearchOpts{Query: "x"})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestSearch429TripsCircuit(t *testing.T) {
	calls := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestClient("k", "", "", srv.URL)
	if _, err := c.Search(context.Background(), SearchOpts{Query: "x"}); err == nil {
		t.Fatal("expected error on 429")
	}
	// Next call should short-circuit without hitting the server.
	if _, err := c.Search(context.Background(), SearchOpts{Query: "x"}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen on subsequent call, got %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("circuit should have blocked the second call; got %d server hits", calls)
	}
}

func TestDownloadSendsBearerAfterLogin(t *testing.T) {
	var (
		loginCalls    int32
		downloadAuths []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			atomic.AddInt32(&loginCalls, 1)
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"username":"bob"`) {
				t.Errorf("login body missing username: %s", body)
			}
			if r.Header.Get("Api-Key") != "k" {
				t.Errorf("login missing Api-Key header")
			}
			_, _ = io.WriteString(w, `{"token":"tok-abc"}`)
		case "/download":
			downloadAuths = append(downloadAuths, r.Header.Get("Authorization"))
			var body struct {
				FileID int `json:"file_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.FileID != 42 {
				t.Errorf("expected file_id 42, got %d", body.FileID)
			}
			_, _ = io.WriteString(w, `{"link":"https://cdn/file","file_name":"sub.srt","remaining":3}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := newTestClient("k", "bob", "hunter2", srv.URL)
	info, err := c.Download(context.Background(), 42)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if info.Link != "https://cdn/file" || info.FileName != "sub.srt" || info.Remaining != 3 {
		t.Fatalf("unexpected DownloadInfo: %+v", info)
	}

	// Second call reuses the cached token — no second login.
	if _, err := c.Download(context.Background(), 42); err != nil {
		t.Fatalf("second Download: %v", err)
	}
	if atomic.LoadInt32(&loginCalls) != 1 {
		t.Fatalf("token should be cached; got %d logins", loginCalls)
	}
	for i, a := range downloadAuths {
		if a != "Bearer tok-abc" {
			t.Errorf("download call %d sent wrong Authorization header: %q", i, a)
		}
	}
}

func TestDownload401ClearsTokenAndTripsCircuit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			_, _ = io.WriteString(w, `{"token":"tok"}`)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newTestClient("k", "u", "p", srv.URL)
	if _, err := c.Download(context.Background(), 1); err == nil {
		t.Fatal("expected 401 to bubble up as error")
	}
	// Token should have been cleared.
	c.mu.Lock()
	tok := c.token
	c.mu.Unlock()
	if tok != "" {
		t.Errorf("expected token to be cleared after 401, got %q", tok)
	}
	// Circuit should now be open.
	if c.circuitAllows() {
		t.Error("expected circuit to be open after 401")
	}
}

func TestFetchFileRespectsSizeLimit(t *testing.T) {
	// Serve a body larger than the 5 MiB limit; verify the client caps.
	big := strings.Repeat("A", 6*1024*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, big)
	}))
	defer srv.Close()

	c := newTestClient("k", "", "", srv.URL)
	body, err := c.FetchFile(context.Background(), srv.URL+"/sub.srt")
	if err != nil {
		t.Fatalf("FetchFile: %v", err)
	}
	if len(body) != 5*1024*1024 {
		t.Fatalf("expected body capped at 5MiB, got %d", len(body))
	}
}

func TestFetchFileSurfacesNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	c := newTestClient("k", "", "", srv.URL)
	if _, err := c.FetchFile(context.Background(), srv.URL+"/x"); err == nil {
		t.Fatal("expected error on 410 response")
	}
}
