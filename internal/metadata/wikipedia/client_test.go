package wikipedia

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetThumbnailURL_PrefersOriginalImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/page/summary/Brandon_Sanderson") {
			t.Errorf("path: got %q, want .../page/summary/Brandon_Sanderson", r.URL.Path)
		}
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "OnScreen/") {
			t.Errorf("User-Agent: want OnScreen/* prefix, got %q", got)
		}
		_, _ = w.Write([]byte(`{
			"type": "standard",
			"title": "Brandon Sanderson",
			"originalimage": {"source": "https://upload.wikimedia.org/full.jpg", "width": 2000, "height": 3000},
			"thumbnail":     {"source": "https://upload.wikimedia.org/thumb.jpg", "width": 320, "height": 480}
		}`))
	}))
	defer srv.Close()
	testBaseURL = srv.URL
	defer func() { testBaseURL = "" }()

	url, err := New().GetThumbnailURL(context.Background(), "Brandon Sanderson")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://upload.wikimedia.org/full.jpg" {
		t.Errorf("URL: got %q, want originalimage", url)
	}
}

func TestGetThumbnailURL_FallsBackToThumbnail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"type": "standard",
			"title": "X",
			"thumbnail": {"source": "https://upload.wikimedia.org/thumb.jpg"}
		}`))
	}))
	defer srv.Close()
	testBaseURL = srv.URL
	defer func() { testBaseURL = "" }()

	url, err := New().GetThumbnailURL(context.Background(), "X")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(url, "/thumb.jpg") {
		t.Errorf("expected thumbnail fallback when originalimage missing, got %q", url)
	}
}

func TestGetThumbnailURL_NotFound_ReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	testBaseURL = srv.URL
	defer func() { testBaseURL = "" }()

	url, err := New().GetThumbnailURL(context.Background(), "Some Obscure Author")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL on 404, got %q", url)
	}
}

func TestGetThumbnailURL_DisambiguationReturnsEmpty(t *testing.T) {
	// "John Smith" lands on a disambiguation page. We refuse to guess —
	// returning empty lets the operator decide via cover.jpg / Fix Match.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"type": "disambiguation",
			"title": "John Smith",
			"originalimage": {"source": "https://upload.wikimedia.org/disambig-icon.png"}
		}`))
	}))
	defer srv.Close()
	testBaseURL = srv.URL
	defer func() { testBaseURL = "" }()

	url, err := New().GetThumbnailURL(context.Background(), "John Smith")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("disambiguation must return empty (refusing to guess), got %q", url)
	}
}

func TestGetThumbnailURL_NoLeadImage_ReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"type":"standard","title":"X"}`))
	}))
	defer srv.Close()
	testBaseURL = srv.URL
	defer func() { testBaseURL = "" }()

	url, err := New().GetThumbnailURL(context.Background(), "X")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL when article has no lead image, got %q", url)
	}
}

func TestGetThumbnailURL_EmptyName_NoNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("must not call Wikipedia with empty name")
	}))
	defer srv.Close()
	testBaseURL = srv.URL
	defer func() { testBaseURL = "" }()

	url, err := New().GetThumbnailURL(context.Background(), "  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "" {
		t.Errorf("expected empty URL for blank name, got %q", url)
	}
}
