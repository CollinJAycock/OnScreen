package arr

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient returns a Client wired against the given test server.
func newTestClient(srv *httptest.Server, apiKey string) *Client {
	c := New(srv.URL, apiKey)
	c.HTTPClient = srv.Client()
	return c
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	// Callers pass either form ("https://radarr.local" or
	// "https://radarr.local/"). Internally we always drop the slash so
	// path concat doesn't double up.
	c := New("https://radarr.local///", "k")
	if c.BaseURL != "https://radarr.local" {
		t.Errorf("BaseURL = %q, want trailing slashes stripped", c.BaseURL)
	}
}

func TestPing_SendsApiKeyHeaderAndDecodes(t *testing.T) {
	var gotAPIKey, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("X-Api-Key")
		gotAccept = r.Header.Get("Accept")
		if r.URL.Path != "/api/v3/system/status" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"appName":"Radarr","version":"5.4.6","instanceName":"radarr-prod"}`)
	}))
	defer srv.Close()

	status, err := newTestClient(srv, "secret-key").Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if gotAPIKey != "secret-key" {
		t.Errorf("X-Api-Key sent = %q, want \"secret-key\"", gotAPIKey)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if status.AppName != "Radarr" || status.Version != "5.4.6" {
		t.Errorf("decoded status = %+v", status)
	}
}

func TestDo_401MapsToErrUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := newTestClient(srv, "wrong").Ping(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("got %v, want ErrUnauthorized", err)
	}
}

func TestDo_404MapsToErrNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := newTestClient(srv, "k").Ping(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestDo_409MapsToErrConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	_, err := newTestClient(srv, "k").Ping(context.Background())
	if !errors.Is(err, ErrConflict) {
		t.Errorf("got %v, want ErrConflict", err)
	}
}

func TestDo_OtherErrorIncludesBodySnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `[{"propertyName":"qualityProfileId","errorMessage":"missing"}]`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv, "k").Ping(context.Background())
	if err == nil {
		t.Fatal("expected error on 400")
	}
	if !strings.Contains(err.Error(), "qualityProfileId") {
		t.Errorf("error %q should include the validation snippet so admins can debug", err.Error())
	}
}

func TestDo_400BodySnippetCappedAt4096(t *testing.T) {
	// Defensive: a hostile or buggy arr instance returning a megabyte of
	// HTML in an error body shouldn't blow up our log lines.
	huge := strings.Repeat("X", 100_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, huge)
	}))
	defer srv.Close()

	_, err := newTestClient(srv, "k").Ping(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if len(err.Error()) > 4096+200 { // 4096 cap + room for the wrapping prose
		t.Errorf("error message length %d exceeds expected cap", len(err.Error()))
	}
}

func TestQualityProfiles_DecodesArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/qualityprofile" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `[{"id":1,"name":"HD-720p"},{"id":4,"name":"Ultra-HD"}]`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").QualityProfiles(context.Background())
	if err != nil {
		t.Fatalf("QualityProfiles: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d profiles, want 2", len(got))
	}
	if got[0].ID != 1 || got[0].Name != "HD-720p" {
		t.Errorf("first profile = %+v", got[0])
	}
	if got[1].ID != 4 || got[1].Name != "Ultra-HD" {
		t.Errorf("second profile = %+v", got[1])
	}
}

func TestRootFolders_DecodesFreeSpace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"id":1,"path":"/movies","freeSpace":1099511627776}]`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").RootFolders(context.Background())
	if err != nil {
		t.Fatalf("RootFolders: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/movies" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Freespace != 1099511627776 {
		t.Errorf("freeSpace decoded as %d, want 1099511627776", got[0].Freespace)
	}
}

func TestTags_DecodesLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"id":7,"label":"from-onscreen"}]`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(got) != 1 || got[0].Label != "from-onscreen" {
		t.Fatalf("got %+v", got)
	}
}

func TestDo_TransportErrorBubblesUp(t *testing.T) {
	// Closed server → connection refused. The error wrapping should
	// preserve the method+path so the log line is debuggable.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close() // immediately close so connect fails

	_, err := newTestClient(srv, "k").Ping(context.Background())
	if err == nil {
		t.Fatal("expected transport error")
	}
	if !strings.Contains(err.Error(), "GET") || !strings.Contains(err.Error(), "/api/v3/system/status") {
		t.Errorf("error %q should include method + path for debuggability", err.Error())
	}
}

func TestDo_RespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// hang forever — only the canceled context should free the request.
		select {}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the call
	_, err := newTestClient(srv, "k").Ping(ctx)
	if err == nil {
		t.Fatal("expected context error")
	}
}
