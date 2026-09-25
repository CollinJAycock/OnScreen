package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

// internalService stands in for something the tuner must not be able to point
// the server at (a loopback admin port, the cloud metadata endpoint). It
// counts every request that reaches it.
func internalService(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, "SECRET-INTERNAL-RESPONSE")
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// offHostURL rewrites srv's URL to use the hostname "localhost" instead of the
// 127.0.0.1 the tuner is configured with: same machine, DIFFERENT hostname —
// the shape of a redirect to 127.0.0.1:<admin port> or 169.254.169.254 from a
// tuner configured by LAN address.
func offHostURL(t *testing.T, srv *httptest.Server, path string) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = "localhost:" + u.Port()
	u.Path = path
	return u.String()
}

// spoofedTuner serves a plausible discover + lineup for channel 5.1 (lineup
// URL on the tuner's own host, so the lineup check passes) and lets the test
// choose what /auto/ and /lineup.json do.
func spoofedTuner(t *testing.T, auto http.HandlerFunc, lineup http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/discover.json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(hdhomerunDiscover{TunerCount: 2})
	})
	mux.HandleFunc("/lineup.json", func(w http.ResponseWriter, r *http.Request) {
		if lineup != nil {
			lineup(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]hdhomerunLineupEntry{
			{GuideNumber: "5.1", GuideName: "WCBS", URL: srv.URL + "/auto/v5.1"},
		})
	})
	mux.HandleFunc("/auto/", auto)
	mux.HandleFunc("/stream/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "MPEG-TS")
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHDHomeRunDriver_OpenStream_RefusesOffHostRedirect(t *testing.T) {
	internal, hits := internalService(t)
	target := offHostURL(t, internal, "/latest/meta-data/")
	tuner := spoofedTuner(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}, nil)

	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: tuner.URL})
	stream, err := d.OpenStream(context.Background(), "5.1")
	if err == nil {
		b, _ := io.ReadAll(stream)
		_ = stream.Close()
		t.Fatalf("tune followed a redirect off the tuner host and streamed %q", b)
	}
	if !errors.Is(err, errHDHomeRunRedirect) {
		t.Errorf("want errHDHomeRunRedirect, got %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("internal service was fetched %d time(s) via the tuner's redirect", n)
	}
}

func TestHDHomeRunDriver_Discover_RefusesOffHostRedirect(t *testing.T) {
	internal, hits := internalService(t)
	target := offHostURL(t, internal, "/lineup.json")
	tuner := spoofedTuner(t, func(w http.ResponseWriter, _ *http.Request) {}, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	})

	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: tuner.URL})
	if _, err := d.Discover(context.Background()); !errors.Is(err, errHDHomeRunRedirect) {
		t.Errorf("want errHDHomeRunRedirect from a lineup redirected off-host, got %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("internal service was fetched %d time(s) via the tuner's redirect", n)
	}
}

// A redirect that stays on the tuner's own host is still followed — the guard
// is about where the server connects, not about redirects as such.
func TestHDHomeRunDriver_OpenStream_FollowsSameHostRedirect(t *testing.T) {
	tuner := spoofedTuner(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/stream/v5.1", http.StatusFound)
	}, nil)

	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: tuner.URL})
	stream, err := d.OpenStream(context.Background(), "5.1")
	if err != nil {
		t.Fatalf("same-host redirect refused: %v", err)
	}
	defer stream.Close()
	if b, _ := io.ReadAll(stream); string(b) != "MPEG-TS" {
		t.Errorf("body = %q, want MPEG-TS", b)
	}
}

// Hops are capped even when every one stays on the tuner host.
func TestHDHomeRunDriver_OpenStream_RedirectLoopIsCapped(t *testing.T) {
	var hops atomic.Int32
	tuner := spoofedTuner(t, func(w http.ResponseWriter, r *http.Request) {
		hops.Add(1)
		http.Redirect(w, r, r.URL.Path, http.StatusFound) // forever
	}, nil)

	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: tuner.URL})
	if _, err := d.OpenStream(context.Background(), "5.1"); !errors.Is(err, errHDHomeRunRedirect) {
		t.Errorf("want errHDHomeRunRedirect for a redirect loop, got %v", err)
	}
	if n := hops.Load(); n > maxHDHomeRunRedirects+1 {
		t.Errorf("followed %d requests, want at most %d", n, maxHDHomeRunRedirects+1)
	}
}
