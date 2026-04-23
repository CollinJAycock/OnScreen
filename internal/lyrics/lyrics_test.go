package lyrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLRCLIB_Lookup_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify query params forwarded correctly.
		q := r.URL.Query()
		if q.Get("artist_name") != "Radiohead" || q.Get("track_name") != "Let Down" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		if q.Get("duration") != "299" {
			t.Errorf("duration: got %q", q.Get("duration"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":1,"plainLyrics":"Transport, motorways and tramlines","syncedLyrics":"[00:00.00]Transport"}`))
	}))
	defer srv.Close()

	c := NewLRCLIBClient().WithBaseURL(srv.URL)
	r, err := c.Lookup(context.Background(), LookupParams{
		Artist: "Radiohead", Track: "Let Down", Album: "OK Computer", DurationS: 299,
	})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if r.Plain == "" || r.Synced == "" {
		t.Errorf("expected both plain + synced: %+v", r)
	}
}

func TestLRCLIB_Lookup_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewLRCLIBClient().WithBaseURL(srv.URL)
	_, err := c.Lookup(context.Background(), LookupParams{Artist: "X", Track: "Y"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestLRCLIB_Lookup_InstrumentalIsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":2,"instrumental":true,"plainLyrics":"","syncedLyrics":""}`))
	}))
	defer srv.Close()
	c := NewLRCLIBClient().WithBaseURL(srv.URL)
	// Instrumental tracks come back with a "hit" but no lyrics — surface
	// as NotFound so callers don't cache an empty string and keep
	// retrying the network hit.
	_, err := c.Lookup(context.Background(), LookupParams{Artist: "X", Track: "Y"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("instrumental should map to ErrNotFound; got %v", err)
	}
}

func TestLRCLIB_Lookup_RequiresArtistAndTrack(t *testing.T) {
	c := NewLRCLIBClient()
	_, err := c.Lookup(context.Background(), LookupParams{Artist: "", Track: "Z"})
	if err == nil {
		t.Error("expected error when artist missing")
	}
	_, err = c.Lookup(context.Background(), LookupParams{Artist: "X", Track: ""})
	if err == nil {
		t.Error("expected error when track missing")
	}
}

func TestLRCLIB_Lookup_ServerErrorBubbles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	c := NewLRCLIBClient().WithBaseURL(srv.URL)
	_, err := c.Lookup(context.Background(), LookupParams{Artist: "X", Track: "Y"})
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("5xx should bubble as generic error; got %v", err)
	}
}

func TestResult_IsEmpty(t *testing.T) {
	if !(Result{}).IsEmpty() {
		t.Error("zero value should be empty")
	}
	if (Result{Plain: "a"}).IsEmpty() || (Result{Synced: "a"}).IsEmpty() {
		t.Error("non-empty shouldn't report empty")
	}
}
