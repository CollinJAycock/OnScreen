package podcast

import (
	"strings"
	"testing"
	"time"
)

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"Night & Day Radio":            "Night & Day Radio",
		"Episode 12: The Return":       "Episode 12 - The Return",
		"AC/DC: Live":                  "AC - DC - Live",
		`What? "Really" <yes> *maybe*`: "What Really yes maybe",
		"  trailing dots...  ":         "trailing dots",
		"...hidden":                    "hidden",
		"tab\there":                    "tab here",
		"":                             "Fallback",
		"???":                          "Fallback",
		"CON":                          "Fallback",
		"com1.txt":                     "Fallback",
		"Console Wars":                 "Console Wars",
	} {
		if got := SafeName(in, "Fallback"); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeName_Bounded(t *testing.T) {
	got := SafeName(strings.Repeat("ü", 200), "x")
	if len(got) > maxNameBytes {
		t.Errorf("len = %d, want ≤ %d", len(got), maxNameBytes)
	}
	if !strings.HasPrefix(got, "üü") || strings.ContainsRune(got, '�') {
		t.Errorf("name cut mid-rune: %q", got)
	}
}

func TestEpisodeFileName(t *testing.T) {
	pub := time.Date(2026, 10, 12, 23, 30, 0, 0, time.FixedZone("EST", -5*3600))
	if got := EpisodeFileName("Ep 1: Hi", &pub, ".mp3"); got != "2026-10-13 Ep 1 - Hi.mp3" {
		t.Errorf("dated = %q (date is the UTC day)", got)
	}
	if got := EpisodeFileName("Ep 1", nil, ".m4a"); got != "Ep 1.m4a" {
		t.Errorf("undated = %q", got)
	}
	if got := EpisodeFileName("", nil, ".mp3"); got != "Episode.mp3" {
		t.Errorf("untitled = %q", got)
	}
}

func TestDisambiguate(t *testing.T) {
	a := Disambiguate("Trailer.mp3", ".mp3", "guid-a")
	b := Disambiguate("Trailer.mp3", ".mp3", "guid-b")
	if a == b || !strings.HasPrefix(a, "Trailer (") || !strings.HasSuffix(a, ").mp3") {
		t.Errorf("Disambiguate = %q / %q", a, b)
	}
	if again := Disambiguate("Trailer.mp3", ".mp3", "guid-a"); again != a {
		t.Errorf("not stable: %q vs %q", again, a)
	}
	if folder := Disambiguate("Show", "", "https://a/feed"); !strings.HasPrefix(folder, "Show (") {
		t.Errorf("folder = %q", folder)
	}
}

func TestNormalizeFeedURL(t *testing.T) {
	for in, want := range map[string]string{
		" https://a.example/feed.xml ":   "https://a.example/feed.xml",
		"http://a.example/rss#frag":      "http://a.example/rss",
		"feed://a.example/rss":           "https://a.example/rss",
		"PCAST://a.example/rss":          "https://a.example/rss",
		"https://a.example/rss?format=x": "https://a.example/rss?format=x",
	} {
		if got, err := normalizeFeedURL(in); err != nil || got != want {
			t.Errorf("normalizeFeedURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "a.example/rss", "ftp://a/rss", "file:///etc/passwd", "https://user:pw@a/rss", "https://"} {
		if _, err := normalizeFeedURL(in); err != ErrInvalidFeedURL {
			t.Errorf("normalizeFeedURL(%q) = %v, want ErrInvalidFeedURL", in, err)
		}
	}
}
