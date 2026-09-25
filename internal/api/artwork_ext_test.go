package api

import (
	"strings"
	"testing"
)

// The /artwork/* route walks LIBRARY SCAN PATHS — the directories the media
// itself lives in. Constrained only to "under a scan path", it served the
// movie files themselves: /artwork/Movies/Film/Film.mkv streamed the full
// bytes with Range support and none of the playback gates (the rating map
// only knows artwork paths and its miss fails open; watch limits never look
// at this route). The allowlist is the guard for that class.
func TestArtworkExtAllowed(t *testing.T) {
	allowed := []string{
		"Movies/Inception (2010)/poster.jpg",
		"Shows/Severance/fanart.JPEG", // case-insensitive
		"Music/Album/cover.png",
		"Audiobooks/Book/folder.webp",
		"old-library/banner.tbn",
		"anim/thumb.gif", "x/y.bmp", "n/next-gen.avif",
	}
	for _, p := range allowed {
		if !artworkExtAllowed(p) {
			t.Errorf("legitimate artwork rejected: %s", p)
		}
	}

	blocked := []string{
		"Movies/Inception (2010)/Inception.mkv", // the finding: full media bytes
		"Movies/Film/Film.mp4",
		"Movies/Film/Film.en.srt", // subtitles aren't artwork either
		"Shows/S01E01.ts",
		"Movies/Film/movie.nfo", // metadata sidecars can embed API keys/paths
		"noextension",
		"trailing-dot.",
		"double.jpg.mkv", // extension is the FINAL one
	}
	for _, p := range blocked {
		if artworkExtAllowed(p) {
			t.Errorf("non-image served through the artwork route: %s — this re-opens "+
				"ungated media streaming", p)
		}
	}
}

// Every allowed extension declares an image/* type, so the server never falls
// back to sniffing the body (a .tbn holding HTML would be sniffed as text/html).
func TestArtworkContentTypeIsAlwaysImage(t *testing.T) {
	for ext, ct := range artworkImageExts {
		if !strings.HasPrefix(ct, "image/") {
			t.Errorf("%s declares %q, want an image/* type", ext, ct)
		}
	}
	if got := artworkContentType("old-library/banner.TBN"); got != "image/jpeg" {
		t.Errorf(".tbn content type = %q, want image/jpeg", got)
	}
	if got := artworkContentType("Movies/Film/Film.mkv"); got != "" {
		t.Errorf("non-image got content type %q", got)
	}
}
