package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const samplePlaylist = `#EXTM3U
#EXTINF:-1 tvg-id="wcbs" tvg-chno="5.1" tvg-logo="http://logos/wcbs.png" tvg-name="WCBS-DT",WCBS-DT
http://provider/stream/5.1
#EXTINF:-1 tvg-id="wabc" tvg-chno="7.1" tvg-name="WABC-DT",WABC-DT
http://provider/stream/7.1
#EXTINF:-1,Channel With No tvg-chno
http://provider/stream/anonymous
`

func TestParseM3U_BasicEntries(t *testing.T) {
	entries, err := parseM3U(strings.NewReader(samplePlaylist))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	if entries[0].Number != "5.1" || entries[0].Name != "WCBS-DT" || entries[0].Callsign != "wcbs" {
		t.Errorf("entry 0: %+v", entries[0])
	}
	if entries[0].LogoURL != "http://logos/wcbs.png" {
		t.Errorf("logo: got %q", entries[0].LogoURL)
	}
	if entries[2].Number != "3" {
		// Anonymous channel falls back to its 1-based sequence number.
		t.Errorf("anonymous channel number: got %q, want sequence 3", entries[2].Number)
	}
}

func TestParseM3U_SkipsExtensionDirectives(t *testing.T) {
	playlist := `#EXTM3U
#EXTGRP:News
#EXTVLCOPT:network-caching=1000
#EXTINF:-1 tvg-chno="5.1",WCBS
http://provider/stream/5.1
`
	entries, err := parseM3U(strings.NewReader(playlist))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("want 1 entry, got %d", len(entries))
	}
}

func TestParseM3U_NameFallsBackToTVGName(t *testing.T) {
	// EXTINF with no display-name after the comma — should use tvg-name.
	playlist := `#EXTM3U
#EXTINF:-1 tvg-chno="5.1" tvg-name="From-tvg-name",
http://provider/stream/5.1
`
	entries, _ := parseM3U(strings.NewReader(playlist))
	if entries[0].Name != "From-tvg-name" {
		t.Errorf("name: got %q, want From-tvg-name", entries[0].Name)
	}
}

func TestParseM3U_StreamWithoutExtinfSkipped(t *testing.T) {
	playlist := `#EXTM3U
http://orphan/stream
#EXTINF:-1 tvg-chno="5.1",WCBS
http://provider/stream/5.1
`
	entries, _ := parseM3U(strings.NewReader(playlist))
	if len(entries) != 1 {
		t.Errorf("orphan stream should be skipped; got %d entries", len(entries))
	}
}

func TestM3UFactory_RequiresSource(t *testing.T) {
	if _, err := M3UFactory("p", []byte("{}")); err == nil {
		t.Error("expected error when source missing")
	}
}

func TestM3UFactory_BadJSON(t *testing.T) {
	if _, err := M3UFactory("p", []byte("not json")); err == nil {
		t.Error("expected JSON parse error")
	}
}

func TestM3UDriver_Discover_FromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "playlist.m3u")
	if err := os.WriteFile(path, []byte(samplePlaylist), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	d := NewM3UDriver("p", M3UConfig{Source: path})
	chans, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(chans) != 3 {
		t.Errorf("got %d channels, want 3", len(chans))
	}
}

func TestM3UDriver_Discover_FromHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, samplePlaylist)
	}))
	defer srv.Close()
	d := NewM3UDriver("p", M3UConfig{Source: srv.URL})
	chans, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(chans) != 3 {
		t.Errorf("got %d channels, want 3", len(chans))
	}
}

func TestM3UDriver_OpenStream_OK(t *testing.T) {
	streamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "TS-PAYLOAD")
	}))
	defer streamSrv.Close()

	playlist := "#EXTM3U\n#EXTINF:-1 tvg-chno=\"5.1\",WCBS\n" + streamSrv.URL + "\n"
	plSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, playlist)
	}))
	defer plSrv.Close()

	d := NewM3UDriver("p", M3UConfig{Source: plSrv.URL})
	if _, err := d.Discover(context.Background()); err != nil {
		t.Fatalf("discover: %v", err)
	}
	stream, err := d.OpenStream(context.Background(), "5.1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer stream.Close()
	body, _ := io.ReadAll(stream)
	if string(body) != "TS-PAYLOAD" {
		t.Errorf("body: got %q", body)
	}
}

func TestM3UDriver_OpenStream_PassesUserAgent(t *testing.T) {
	var seenUA string
	streamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUA = r.Header.Get("User-Agent")
		io.WriteString(w, "ok")
	}))
	defer streamSrv.Close()

	playlist := "#EXTM3U\n#EXTINF:-1 tvg-chno=\"1\",X\n" + streamSrv.URL + "\n"
	plSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, playlist)
	}))
	defer plSrv.Close()

	d := NewM3UDriver("p", M3UConfig{Source: plSrv.URL, UserAgent: "FancyTV/1.0"})
	d.Discover(context.Background())
	s, err := d.OpenStream(context.Background(), "1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.Close()
	if seenUA != "FancyTV/1.0" {
		t.Errorf("User-Agent: got %q, want FancyTV/1.0", seenUA)
	}
}

func TestM3UDriver_OpenStream_UnknownChannel(t *testing.T) {
	d := NewM3UDriver("p", M3UConfig{Source: "/nonexistent"})
	_, err := d.OpenStream(context.Background(), "999")
	if !errors.Is(err, ErrChannelNotFound) {
		t.Errorf("got %v, want ErrChannelNotFound", err)
	}
}

func TestM3UDriver_OpenStream_503MapsToBusy(t *testing.T) {
	streamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer streamSrv.Close()
	playlist := "#EXTM3U\n#EXTINF:-1 tvg-chno=\"1\",X\n" + streamSrv.URL + "\n"
	plSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, playlist)
	}))
	defer plSrv.Close()

	d := NewM3UDriver("p", M3UConfig{Source: plSrv.URL})
	d.Discover(context.Background())
	_, err := d.OpenStream(context.Background(), "1")
	if !errors.Is(err, ErrAllTunersBusy) {
		t.Errorf("got %v, want ErrAllTunersBusy", err)
	}
}

func TestM3UDriver_TuneCount_DefaultsTo100(t *testing.T) {
	d := NewM3UDriver("p", M3UConfig{Source: "/x"})
	if d.TuneCount() != 100 {
		t.Errorf("default tune count: got %d, want 100", d.TuneCount())
	}
}

func TestM3UDriver_TuneCount_OverrideRespected(t *testing.T) {
	d := NewM3UDriver("p", M3UConfig{Source: "/x", MaxConcurrentStreams: 4})
	if d.TuneCount() != 4 {
		t.Errorf("got %d, want 4", d.TuneCount())
	}
}

func TestM3UDriver_Probe_OK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.m3u")
	os.WriteFile(path, []byte(samplePlaylist), 0o644)
	d := NewM3UDriver("p", M3UConfig{Source: path})
	if err := d.Probe(context.Background()); err != nil {
		t.Errorf("probe: %v", err)
	}
}

func TestM3UDriver_Probe_FileMissing(t *testing.T) {
	d := NewM3UDriver("p", M3UConfig{Source: "/no/such/file.m3u"})
	if err := d.Probe(context.Background()); err == nil {
		t.Error("expected probe to fail for missing file")
	}
}

// Drop a json-config round-trip through the registry to make sure the
// factory + driver match the production wiring path.
func TestRegistry_M3UEndToEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.m3u")
	os.WriteFile(path, []byte(samplePlaylist), 0o644)

	r := NewRegistry()
	r.Register(TunerTypeM3U, M3UFactory)
	cfg, _ := json.Marshal(M3UConfig{Source: path})
	d, err := r.Build(TunerTypeM3U, "p", cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	chans, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(chans) != 3 {
		t.Errorf("channels: got %d", len(chans))
	}
}
