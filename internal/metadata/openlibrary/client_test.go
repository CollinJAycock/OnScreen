package openlibrary

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchBookCoverURL_HitsCoverEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("title"); got != "The Way of Kings" {
			t.Errorf("title query: want 'The Way of Kings', got %q", got)
		}
		if got := r.URL.Query().Get("author"); got != "Brandon Sanderson" {
			t.Errorf("author query: want 'Brandon Sanderson', got %q", got)
		}
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "OnScreen/") {
			t.Errorf("User-Agent: want OnScreen/* prefix, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"docs":[
			{"title":"The Way of Kings","author_name":["Brandon Sanderson"],"cover_i":7222246}
		]}`))
	}))
	defer srv.Close()
	testSearchURL = srv.URL
	defer func() { testSearchURL = "" }()

	c := NewWithClient(srv.Client())
	url, err := c.SearchBookCoverURL(context.Background(), "The Way of Kings", "Brandon Sanderson")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://covers.openlibrary.org/b/id/7222246-L.jpg"
	if url != want {
		t.Errorf("cover URL: got %q, want %q", url, want)
	}
}

func TestSearchBookCoverURL_FirstWithCoverWins(t *testing.T) {
	// Real OpenLibrary results often include "ghost" entries (catalog
	// rows with no cover scan); we want to skip those and pick the
	// first doc with a usable cover_i.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"docs":[
			{"title":"Ghost edition","author_name":["X"]},
			{"title":"Working edition","author_name":["X"],"cover_i":12345}
		]}`))
	}))
	defer srv.Close()
	testSearchURL = srv.URL
	defer func() { testSearchURL = "" }()

	url, err := NewWithClient(srv.Client()).SearchBookCoverURL(context.Background(), "Test", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(url, "/12345-L.jpg") {
		t.Errorf("expected the second doc's cover; got %q", url)
	}
}

func TestSearchBookCoverURL_NoDocs_ReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"docs":[]}`))
	}))
	defer srv.Close()
	testSearchURL = srv.URL
	defer func() { testSearchURL = "" }()

	url, err := NewWithClient(srv.Client()).SearchBookCoverURL(context.Background(), "Some Obscure Title", "Unknown Author")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL on no-docs, got %q", url)
	}
}

func TestSearchBookCoverURL_AllDocsCoverless(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"docs":[
			{"title":"A"},{"title":"B"},{"title":"C"}
		]}`))
	}))
	defer srv.Close()
	testSearchURL = srv.URL
	defer func() { testSearchURL = "" }()

	url, err := NewWithClient(srv.Client()).SearchBookCoverURL(context.Background(), "Title", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL when no doc has cover_i, got %q", url)
	}
}

func TestSearchBookCoverURL_EmptyTitle_NoNetwork(t *testing.T) {
	// Important: we must short-circuit before issuing a request when
	// title is empty. Otherwise the scanner would spam OpenLibrary on
	// every untitled file. Inject a server that fails the test if hit.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("must not call OpenLibrary with empty title")
	}))
	defer srv.Close()
	testSearchURL = srv.URL
	defer func() { testSearchURL = "" }()

	url, err := NewWithClient(srv.Client()).SearchBookCoverURL(context.Background(), "  ", "Author")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL for blank title, got %q", url)
	}
}
