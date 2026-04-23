package livetv

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Swap the production safehttp-guarded xmltvClient for one that allows
// loopback, so httptest servers (127.0.0.1) are reachable in tests.
// The SSRF guard is a production concern; tests exercise logic around
// it rather than the guard itself (which has its own test file).
func init() {
	xmltvClient = &http.Client{Timeout: 60 * time.Second}
}

const sampleXMLTV = `<?xml version="1.0" encoding="UTF-8"?>
<tv source-info-name="test">
  <channel id="WCBS.5.1.us">
    <display-name>WCBS-DT</display-name>
    <display-name>5.1</display-name>
    <icon src="http://logos/wcbs.png"/>
    <lcn>5.1</lcn>
  </channel>
  <channel id="WABC.7.1.us">
    <display-name>WABC-DT</display-name>
    <lcn>7.1</lcn>
  </channel>
  <programme start="20260423180000 -0400" stop="20260423190000 -0400" channel="WCBS.5.1.us">
    <title>60 Minutes</title>
    <sub-title>The Long Game</sub-title>
    <desc>News magazine episode.</desc>
    <category>News</category>
    <category>Magazine</category>
    <date>20260420</date>
    <episode-num system="xmltv_ns">4.11.0/1</episode-num>
    <rating system="VCHIP"><value>TV-PG</value></rating>
  </programme>
  <programme start="20260423190000 -0400" stop="20260423203000 -0400" channel="WCBS.5.1.us">
    <title>NCIS</title>
    <episode-num system="onscreen">S22E08</episode-num>
  </programme>
  <programme start="20260423180000" stop="20260423190000" channel="WABC.7.1.us">
    <title>World News Tonight</title>
  </programme>
  <programme start="invalid" stop="20260423190000 -0400" channel="WABC.7.1.us">
    <title>Should be skipped</title>
  </programme>
</tv>
`

func TestParseXMLTV_Channels(t *testing.T) {
	chans, _, _, err := ParseXMLTV(strings.NewReader(sampleXMLTV))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(chans) != 2 {
		t.Errorf("channel count: got %d, want 2", len(chans))
	}
	c := chans[0]
	if c.ID != "WCBS.5.1.us" {
		t.Errorf("id: got %q", c.ID)
	}
	if len(c.DisplayNames) != 2 || c.DisplayNames[0] != "WCBS-DT" {
		t.Errorf("display names: got %v", c.DisplayNames)
	}
	if c.IconURL != "http://logos/wcbs.png" {
		t.Errorf("icon: got %q", c.IconURL)
	}
	if c.LCN != "5.1" {
		t.Errorf("lcn: got %q", c.LCN)
	}
}

func TestParseXMLTV_Programs(t *testing.T) {
	_, programs, skipped, err := ParseXMLTV(strings.NewReader(sampleXMLTV))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(programs) != 3 {
		t.Errorf("program count: got %d, want 3 (valid only)", len(programs))
	}
	if skipped != 1 {
		t.Errorf("skipped: got %d, want 1 (the malformed start)", skipped)
	}

	p := programs[0]
	if p.Title != "60 Minutes" || p.Subtitle != "The Long Game" {
		t.Errorf("titles: %+v", p)
	}
	if len(p.Category) != 2 || p.Category[0] != "News" {
		t.Errorf("categories: %v", p.Category)
	}
	if p.Rating != "TV-PG" {
		t.Errorf("rating: got %q", p.Rating)
	}
	// xmltv_ns "4.11.0/1" → S5 E12 (zero-indexed, +1).
	if p.SeasonNum == nil || *p.SeasonNum != 5 || p.EpisodeNum == nil || *p.EpisodeNum != 12 {
		t.Errorf("episode: got S=%v E=%v", p.SeasonNum, p.EpisodeNum)
	}
	// Times round-trip to UTC.
	wantStart := time.Date(2026, 4, 23, 22, 0, 0, 0, time.UTC)
	if !p.StartsAt.Equal(wantStart) {
		t.Errorf("start: got %v, want %v", p.StartsAt, wantStart)
	}
	// original air date 20260420.
	if p.OriginalAirDate == nil || p.OriginalAirDate.Year() != 2026 || p.OriginalAirDate.Month() != 4 || p.OriginalAirDate.Day() != 20 {
		t.Errorf("original air date: %v", p.OriginalAirDate)
	}
}

func TestParseXMLTV_OnscreenEpisodeFormat(t *testing.T) {
	_, programs, _, err := ParseXMLTV(strings.NewReader(sampleXMLTV))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// programs[1] is the NCIS S22E08 entry.
	p := programs[1]
	if p.Title != "NCIS" {
		t.Fatalf("wrong program: %s", p.Title)
	}
	if p.SeasonNum == nil || *p.SeasonNum != 22 || p.EpisodeNum == nil || *p.EpisodeNum != 8 {
		t.Errorf("S22E08 not parsed; got S=%v E=%v", p.SeasonNum, p.EpisodeNum)
	}
}

func TestParseXMLTV_NakedTimestampTreatedAsUTC(t *testing.T) {
	_, programs, _, err := ParseXMLTV(strings.NewReader(sampleXMLTV))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// programs[2] is the World News Tonight with no TZ — should land at
	// 18:00 UTC on the same day.
	p := programs[2]
	if p.Title != "World News Tonight" {
		t.Fatalf("wrong program: %s", p.Title)
	}
	wantStart := time.Date(2026, 4, 23, 18, 0, 0, 0, time.UTC)
	if !p.StartsAt.Equal(wantStart) {
		t.Errorf("naked timestamp: got %v, want %v", p.StartsAt, wantStart)
	}
}

func TestParseXMLTV_MalformedDocReturnsError(t *testing.T) {
	if _, _, _, err := ParseXMLTV(strings.NewReader("not xml")); err == nil {
		t.Error("expected parse error")
	}
}

func TestSourceProgramID_Stable(t *testing.T) {
	p := XMLTVProgram{
		ChannelID: "WCBS.5.1.us",
		StartsAt:  time.Date(2026, 4, 23, 22, 0, 0, 0, time.UTC),
	}
	want := "WCBS.5.1.us@20260423T220000Z"
	if got := p.SourceProgramID(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseXMLTVNs_Variants(t *testing.T) {
	cases := []struct {
		in     string
		wantS  int32
		wantE  int32
		hasSE  bool
	}{
		{"0.0.0/1", 1, 1, true},
		{"4.11.", 5, 12, true},
		{"21.7.0/26", 22, 8, true},
		{". .", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, c := range cases {
		s, e, ok := parseXMLTVNs(c.in)
		if !c.hasSE {
			if ok && s != nil && e != nil {
				t.Errorf("%q: expected no S/E, got S=%v E=%v", c.in, s, e)
			}
			continue
		}
		if !ok || s == nil || e == nil {
			t.Errorf("%q: expected S=%d E=%d, got ok=%v S=%v E=%v", c.in, c.wantS, c.wantE, ok, s, e)
			continue
		}
		if *s != c.wantS || *e != c.wantE {
			t.Errorf("%q: got S=%d E=%d, want S=%d E=%d", c.in, *s, *e, c.wantS, c.wantE)
		}
	}
}

func TestParseSEFormat_Variants(t *testing.T) {
	cases := []struct{ in string; s, e int32; ok bool }{
		{"S05E12", 5, 12, true},
		{"s22e08", 22, 8, true},
		{"5x12", 5, 12, true},
		{"E12", 0, 0, false},     // missing S
		{"abc", 0, 0, false},
	}
	for _, c := range cases {
		s, e, ok := parseSEFormat(c.in)
		if ok != c.ok {
			t.Errorf("%q: ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if *s != c.s || *e != c.e {
			t.Errorf("%q: got S=%d E=%d, want S=%d E=%d", c.in, *s, *e, c.s, c.e)
		}
	}
}

// ── FetchXMLTV: gzip auto-detect ─────────────────────────────────────────────

// gzipBytes returns sampleXMLTV compressed with gzip — same content the
// caller would see when fetching epgshare01-style .xml.gz URLs.
func gzipBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write([]byte(sampleXMLTV)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func TestFetchXMLTV_PlainXMLOverHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, sampleXMLTV)
	}))
	defer srv.Close()

	body, err := FetchXMLTV(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	defer body.Close()
	chans, _, _, err := ParseXMLTV(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(chans) != 2 {
		t.Errorf("got %d channels, want 2", len(chans))
	}
}

// TestFetchXMLTV_GzippedHTTP exercises the epgshare01-style case where
// the URL ends in .xml.gz and the server returns raw gzipped bytes WITHOUT
// a Content-Encoding header. Before the gzip auto-detect, this fed raw
// 0x1F 0x8B bytes into the XML decoder and exploded with "illegal
// character code U+001F".
func TestFetchXMLTV_GzippedHTTP(t *testing.T) {
	gz := gzipBytes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Deliberately set octet-stream — that's what static-file servers
		// do for .gz suffixes; it's the case Go's transport WON'T
		// auto-decompress for us.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(gz)
	}))
	defer srv.Close()

	body, err := FetchXMLTV(context.Background(), srv.URL+"/epg.xml.gz")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	defer body.Close()
	chans, programs, _, err := ParseXMLTV(body)
	if err != nil {
		t.Fatalf("parse gzipped: %v", err)
	}
	if len(chans) != 2 || len(programs) != 3 {
		t.Errorf("got %d channels / %d programs, want 2 / 3", len(chans), len(programs))
	}
}

func TestFetchXMLTV_GzippedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "epg.xml.gz")
	if err := os.WriteFile(path, gzipBytes(t), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, err := FetchXMLTV(context.Background(), path)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	defer body.Close()
	chans, _, _, err := ParseXMLTV(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(chans) != 2 {
		t.Errorf("got %d channels, want 2", len(chans))
	}
}

func TestFetchXMLTV_PlainFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "epg.xml")
	if err := os.WriteFile(path, []byte(sampleXMLTV), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, err := FetchXMLTV(context.Background(), path)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	defer body.Close()
	chans, _, _, err := ParseXMLTV(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(chans) != 2 {
		t.Errorf("got %d channels, want 2", len(chans))
	}
}

func TestFetchXMLTV_HTTP404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := FetchXMLTV(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error for 404")
	}
}

// Sanity test for the helpers themselves.
func TestMaybeGunzip_PassesThroughPlain(t *testing.T) {
	rc := io.NopCloser(strings.NewReader("plain text"))
	out, err := maybeGunzip(rc)
	if err != nil {
		t.Fatalf("maybeGunzip: %v", err)
	}
	defer out.Close()
	body, _ := io.ReadAll(out)
	if string(body) != "plain text" {
		t.Errorf("got %q", body)
	}
}

func TestMaybeGunzip_DecompressesGzip(t *testing.T) {
	rc := io.NopCloser(bytes.NewReader(gzipBytes(t)))
	out, err := maybeGunzip(rc)
	if err != nil {
		t.Fatalf("maybeGunzip: %v", err)
	}
	defer out.Close()
	body, _ := io.ReadAll(out)
	if !strings.Contains(string(body), "<title>60 Minutes</title>") {
		t.Errorf("decompressed body unexpected: %s", body[:min(200, len(body))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Touch the time package so it stays in the imports if every other use
// goes away — the package is part of the public API of this file.
var _ = time.Now
