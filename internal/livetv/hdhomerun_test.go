package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeHDHomeRun stands up an httptest.Server that mimics the three
// HDHomeRun HTTP endpoints the driver hits: /discover.json, /lineup.json,
// and /auto/v{number} (the streaming endpoint). Tests construct it, point
// the driver at .URL(), and assert behavior.
type fakeHDHomeRun struct {
	srv         *httptest.Server
	tunerCount  int
	lineup      []hdhomerunLineupEntry
	streamBody  string
	streamFails bool // if true, /auto returns 503
	notFound    bool // if true, /auto returns 404
}

func newFakeHDHomeRun(t *testing.T) *fakeHDHomeRun {
	t.Helper()
	f := &fakeHDHomeRun{tunerCount: 3, streamBody: "fake-mpeg-ts"}
	mux := http.NewServeMux()
	mux.HandleFunc("/discover.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(hdhomerunDiscover{
			FriendlyName: "TestHDHR",
			ModelNumber:  "HDHR5-4US",
			DeviceID:     "ABCD1234",
			TunerCount:   f.tunerCount,
		})
	})
	mux.HandleFunc("/lineup.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(f.lineup)
	})
	mux.HandleFunc("/auto/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case f.notFound:
			w.WriteHeader(http.StatusNotFound)
		case f.streamFails:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			io.WriteString(w, f.streamBody)
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestHDHomeRunFactory_RequiresHostURL(t *testing.T) {
	if _, err := HDHomeRunFactory("box", []byte("{}")); err == nil {
		t.Fatal("expected error when host_url missing")
	}
}

func TestHDHomeRunFactory_BadJSON(t *testing.T) {
	if _, err := HDHomeRunFactory("box", []byte("not json")); err == nil {
		t.Fatal("expected JSON parse error")
	}
}

func TestHDHomeRunFactory_OK(t *testing.T) {
	cfg, _ := json.Marshal(HDHomeRunConfig{HostURL: "http://10.0.0.50"})
	d, err := HDHomeRunFactory("box", cfg)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if d.Type() != TunerTypeHDHomeRun {
		t.Errorf("type: got %q", d.Type())
	}
}

func TestHDHomeRunDriver_DiscoverPopulatesTuneCount(t *testing.T) {
	fake := newFakeHDHomeRun(t)
	fake.tunerCount = 5
	fake.lineup = []hdhomerunLineupEntry{
		{GuideNumber: "5.1", GuideName: "WCBS-DT"},
		{GuideNumber: "7.1", GuideName: "WABC-DT"},
	}
	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: fake.srv.URL})

	chans, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(chans) != 2 {
		t.Errorf("channel count: got %d, want 2", len(chans))
	}
	if chans[0].Number != "5.1" || chans[0].Name != "WCBS-DT" {
		t.Errorf("first channel: %+v", chans[0])
	}
	if d.TuneCount() != 5 {
		t.Errorf("tune count: got %d, want 5 (from /discover.json)", d.TuneCount())
	}
}

func TestHDHomeRunDriver_DiscoverFiltersDRMChannels(t *testing.T) {
	fake := newFakeHDHomeRun(t)
	fake.lineup = []hdhomerunLineupEntry{
		{GuideNumber: "5.1", GuideName: "WCBS-DT"},
		{GuideNumber: "100", GuideName: "HBO Encrypted", DRM: 1}, // skip
		{GuideNumber: "7.1", GuideName: "WABC-DT"},
	}
	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: fake.srv.URL})

	chans, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(chans) != 2 {
		t.Errorf("DRM channels should be filtered; got %d, want 2", len(chans))
	}
	for _, c := range chans {
		if c.Number == "100" {
			t.Errorf("DRM channel leaked through: %+v", c)
		}
	}
}

func TestHDHomeRunDriver_TuneCountOverrideWins(t *testing.T) {
	fake := newFakeHDHomeRun(t)
	fake.tunerCount = 4
	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: fake.srv.URL, TuneCountOverride: 2})
	_, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if d.TuneCount() != 2 {
		t.Errorf("override should win; got %d, want 2", d.TuneCount())
	}
}

func TestHDHomeRunDriver_OpenStream_OK(t *testing.T) {
	fake := newFakeHDHomeRun(t)
	fake.streamBody = "MPEG-TS-payload"
	// Seed the lineup so the lazy Discover inside OpenStream finds the
	// channel and caches its URL.
	fake.lineup = []hdhomerunLineupEntry{
		{GuideNumber: "5.1", GuideName: "WCBS-DT", URL: fake.srv.URL + "/auto/v5.1"},
	}
	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: fake.srv.URL})

	stream, err := d.OpenStream(context.Background(), "5.1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer stream.Close()
	body, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "MPEG-TS-payload" {
		t.Errorf("body: got %q", body)
	}
}

func TestHDHomeRunDriver_OpenStream_AllTunersBusy(t *testing.T) {
	fake := newFakeHDHomeRun(t)
	fake.streamFails = true
	fake.lineup = []hdhomerunLineupEntry{
		{GuideNumber: "5.1", GuideName: "WCBS-DT", URL: fake.srv.URL + "/auto/v5.1"},
	}
	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: fake.srv.URL})

	_, err := d.OpenStream(context.Background(), "5.1")
	if !errors.Is(err, ErrAllTunersBusy) {
		t.Errorf("503 should map to ErrAllTunersBusy; got %v", err)
	}
}

func TestHDHomeRunDriver_OpenStream_ChannelNotFound(t *testing.T) {
	// Seed the lineup with the channel so the lazy-discover path inside
	// OpenStream populates the URL map; the fake's /auto/ handler is what
	// returns 404 → that's the case we're verifying maps to ErrChannelNotFound.
	fake := newFakeHDHomeRun(t)
	fake.notFound = true
	fake.lineup = []hdhomerunLineupEntry{
		{GuideNumber: "999.9", GuideName: "Test", URL: fake.srv.URL + "/auto/v999.9"},
	}
	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: fake.srv.URL})

	_, err := d.OpenStream(context.Background(), "999.9")
	if !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("404 should map to ErrChannelNotFound; got %v", err)
	}
}

func TestHDHomeRunDriver_OpenStream_OtherErrorWrapped(t *testing.T) {
	// Need a fake that serves valid /discover.json + /lineup.json so the
	// lazy-discover path populates the URL map, then returns 500 when the
	// stream URL is hit. Reuse the standard fake and override the stream
	// handler via a Server-level wrapper.
	fake := newFakeHDHomeRun(t)
	fake.lineup = []hdhomerunLineupEntry{
		{GuideNumber: "5.1", GuideName: "WCBS", URL: fake.srv.URL + "/auto/v5.1"},
	}
	streamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer streamSrv.Close()
	// Replace the lineup URL so OpenStream targets the 500-returning server.
	fake.lineup[0].URL = streamSrv.URL + "/auto/v5.1"

	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: fake.srv.URL})
	_, err := d.OpenStream(context.Background(), "5.1")
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("expected wrapped 500 error, got %v", err)
	}
}

func TestHDHomeRunDriver_Probe_OK(t *testing.T) {
	fake := newFakeHDHomeRun(t)
	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: fake.srv.URL})
	if err := d.Probe(context.Background()); err != nil {
		t.Errorf("probe: %v", err)
	}
}

func TestHDHomeRunDriver_Probe_DeadDevice(t *testing.T) {
	d := NewHDHomeRunDriver("box", HDHomeRunConfig{HostURL: "http://127.0.0.1:1"})
	if err := d.Probe(context.Background()); err == nil {
		t.Error("expected probe to fail for unreachable host")
	}
}

func TestRegistry_BuildUnknownTypeFails(t *testing.T) {
	r := NewRegistry()
	if _, err := r.Build("nope", "x", nil); err == nil {
		t.Error("expected error for unregistered type")
	}
}

func TestRegistry_RegisterAndBuild(t *testing.T) {
	r := NewRegistry()
	r.Register(TunerTypeHDHomeRun, HDHomeRunFactory)
	cfg, _ := json.Marshal(HDHomeRunConfig{HostURL: "http://10.0.0.50"})
	d, err := r.Build(TunerTypeHDHomeRun, "box", cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if d.Type() != TunerTypeHDHomeRun {
		t.Errorf("type mismatch: %q", d.Type())
	}
}
