package scanner

import "testing"

// TestParseMusicVideoPath_PlexLayout covers the canonical Plex
// convention: `<Library>/<Artist>/Music Videos/<file>.mkv`.
func TestParseMusicVideoPath_PlexLayout(t *testing.T) {
	artist, title := parseMusicVideoPath(
		"/media/Music/Prince/Music Videos/Purple Rain.mkv",
		[]string{"/media/Music"},
	)
	if artist != "Prince" {
		t.Errorf("artist = %q, want Prince", artist)
	}
	if title != "Purple Rain" {
		t.Errorf("title = %q, want Purple Rain", title)
	}
}

// TestParseMusicVideoPath_FlatInArtistDir handles the flat layout
// where video files sit directly under the artist folder mixed with
// audio tracks: `<Library>/<Artist>/<file>.mp4`.
func TestParseMusicVideoPath_FlatInArtistDir(t *testing.T) {
	artist, title := parseMusicVideoPath(
		"/media/Music/Beyoncé/Formation.mp4",
		[]string{"/media/Music"},
	)
	if artist != "Beyoncé" {
		t.Errorf("artist = %q, want Beyoncé", artist)
	}
	if title != "Formation" {
		t.Errorf("title = %q, want Formation", title)
	}
}

// TestParseMusicVideoPath_StripsArtistPrefix removes a leading
// "Artist - " from the filename-derived title so the UI doesn't
// render redundant artist info under the artist's own page.
func TestParseMusicVideoPath_StripsArtistPrefix(t *testing.T) {
	artist, title := parseMusicVideoPath(
		"/media/Music/Prince/Music Videos/Prince - When Doves Cry.mkv",
		[]string{"/media/Music"},
	)
	if artist != "Prince" {
		t.Errorf("artist = %q, want Prince", artist)
	}
	if title != "When Doves Cry" {
		t.Errorf("title = %q, want When Doves Cry", title)
	}
}

// TestParseMusicVideoPath_FilenameFallback covers a loose file at
// the library root — no folder gives us the artist, so we fall
// back to parsing "Artist - Title" from the filename.
func TestParseMusicVideoPath_FilenameFallback(t *testing.T) {
	artist, title := parseMusicVideoPath(
		"/media/Music/Queen - Bohemian Rhapsody.mkv",
		[]string{"/media/Music"},
	)
	if artist != "Queen" {
		t.Errorf("artist = %q, want Queen", artist)
	}
	if title != "Bohemian Rhapsody" {
		t.Errorf("title = %q, want Bohemian Rhapsody", title)
	}
}

// TestParseMusicVideoPath_Unknown is the defensive fallback when no
// folder or filename convention fits — shouldn't lose the file.
func TestParseMusicVideoPath_Unknown(t *testing.T) {
	artist, title := parseMusicVideoPath(
		"/media/Music/loose.mkv",
		[]string{"/media/Music"},
	)
	if artist != "Unknown Artist" {
		t.Errorf("artist = %q, want Unknown Artist", artist)
	}
	if title != "loose" {
		t.Errorf("title = %q, want loose", title)
	}
}

// TestIsVideoFile covers the extension set the music-video router
// branches on. Audio-only extensions must NOT match or the music-
// library scan would misroute FLAC files to processMusicVideo.
func TestIsVideoFile(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"video.mkv", true},
		{"video.mp4", true},
		{"video.webm", true},
		{"video.MOV", true}, // case-insensitive
		{"song.flac", false},
		{"song.mp3", false},
		{"image.jpg", false},
	}
	for _, c := range cases {
		if got := isVideoFile(c.path); got != c.want {
			t.Errorf("isVideoFile(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
