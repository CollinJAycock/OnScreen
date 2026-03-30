package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseMusicPath_FullHierarchy(t *testing.T) {
	// Simulate: /music/Pink Floyd/Dark Side of the Moon/03 - Time.flac
	path := filepath.Join("/music", "Pink Floyd", "Dark Side of the Moon", "03 - Time.flac")
	tags := parseMusicPath(path)

	if tags.Artist != "Pink Floyd" {
		t.Errorf("artist: got %q, want %q", tags.Artist, "Pink Floyd")
	}
	if tags.Album != "Dark Side of the Moon" {
		t.Errorf("album: got %q, want %q", tags.Album, "Dark Side of the Moon")
	}
	if tags.Title != "Time" {
		t.Errorf("title: got %q, want %q", tags.Title, "Time")
	}
	if tags.Track != 3 {
		t.Errorf("track: got %d, want 3", tags.Track)
	}
}

func TestParseMusicPath_DotSeparator(t *testing.T) {
	// "01. Song Name.mp3"
	path := filepath.Join("/music", "Artist", "Album", "01. Song Name.mp3")
	tags := parseMusicPath(path)

	if tags.Track != 1 {
		t.Errorf("track: got %d, want 1", tags.Track)
	}
	if tags.Title != "Song Name" {
		t.Errorf("title: got %q, want %q", tags.Title, "Song Name")
	}
}

func TestParseMusicPath_SpaceSeparator(t *testing.T) {
	// "12 Song Name.flac"
	path := filepath.Join("/music", "Artist", "Album", "12 Song Name.flac")
	tags := parseMusicPath(path)

	if tags.Track != 12 {
		t.Errorf("track: got %d, want 12", tags.Track)
	}
	if tags.Title != "Song Name" {
		t.Errorf("title: got %q, want %q", tags.Title, "Song Name")
	}
}

func TestParseMusicPath_NoTrackNumber(t *testing.T) {
	path := filepath.Join("/music", "Artist", "Album", "Song Title.mp3")
	tags := parseMusicPath(path)

	if tags.Track != 0 {
		t.Errorf("track: got %d, want 0", tags.Track)
	}
	if tags.Title != "Song Title" {
		t.Errorf("title: got %q, want %q", tags.Title, "Song Title")
	}
}

func TestParseMusicPath_ShallowPath(t *testing.T) {
	// File with only one parent directory — album is the parent, artist falls back.
	path := filepath.Join("/music", "song.flac")
	tags := parseMusicPath(path)

	if tags.Album != "music" {
		t.Errorf("album: got %q, want %q", tags.Album, "music")
	}
	// Artist comes from the grandparent; for "/music" the grandparent is "/".
	if tags.Artist == "" {
		t.Error("artist should not be empty")
	}
}

func TestParseMusicPath_DashSeparator(t *testing.T) {
	// "05-Track Title.m4a"
	path := filepath.Join("/music", "Artist", "Album", "05-Track Title.m4a")
	tags := parseMusicPath(path)

	if tags.Track != 5 {
		t.Errorf("track: got %d, want 5", tags.Track)
	}
	if tags.Title != "Track Title" {
		t.Errorf("title: got %q, want %q", tags.Title, "Track Title")
	}
}

func TestParseMusicPath_ThreeDigitTrackNumber(t *testing.T) {
	// "101 - Track.flac" (multi-disc sets sometimes use 101 for disc 1 track 01)
	path := filepath.Join("/music", "Artist", "Album", "101 - Track.flac")
	tags := parseMusicPath(path)

	if tags.Track != 101 {
		t.Errorf("track: got %d, want 101", tags.Track)
	}
	if tags.Title != "Track" {
		t.Errorf("title: got %q, want %q", tags.Title, "Track")
	}
}

func TestReadMusicTags_FallbackToPath(t *testing.T) {
	// Create a temp file that is not a valid audio file — tag reading will fail,
	// so ReadMusicTags should fall back to path-based parsing.
	dir := t.TempDir()
	artistDir := filepath.Join(dir, "The Beatles")
	albumDir := filepath.Join(artistDir, "Abbey Road")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(albumDir, "05 - Octopus's Garden.flac")
	if err := os.WriteFile(filePath, []byte("not a real audio file"), 0o644); err != nil {
		t.Fatal(err)
	}

	tags, err := ReadMusicTags(filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tags.Artist != "The Beatles" {
		t.Errorf("artist: got %q, want %q", tags.Artist, "The Beatles")
	}
	if tags.Album != "Abbey Road" {
		t.Errorf("album: got %q, want %q", tags.Album, "Abbey Road")
	}
	if tags.Title != "Octopus's Garden" {
		t.Errorf("title: got %q, want %q", tags.Title, "Octopus's Garden")
	}
	if tags.Track != 5 {
		t.Errorf("track: got %d, want 5", tags.Track)
	}
}

func TestReadMusicTags_NonexistentFile(t *testing.T) {
	// ReadMusicTags should still return valid tags from path parsing even when
	// the file does not exist (os.Open fails).
	path := filepath.Join("/music", "Artist", "Album", "01 - Track.mp3")
	tags, err := ReadMusicTags(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tags.Artist != "Artist" {
		t.Errorf("artist: got %q, want %q", tags.Artist, "Artist")
	}
	if tags.Album != "Album" {
		t.Errorf("album: got %q, want %q", tags.Album, "Album")
	}
	if tags.Title != "Track" {
		t.Errorf("title: got %q, want %q", tags.Title, "Track")
	}
	if tags.Track != 1 {
		t.Errorf("track: got %d, want 1", tags.Track)
	}
}

func TestSortTitle(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"The Beatles", "beatles"},
		{"A Perfect Circle", "perfect circle"},
		{"An Album", "album"},
		{"Pink Floyd", "pink floyd"},
		{"", ""},
		{"THE ALL CAPS", "all caps"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sortTitle(tt.input)
			if got != tt.want {
				t.Errorf("sortTitle(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsMusicFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"song.flac", true},
		{"song.mp3", true},
		{"song.m4a", true},
		{"song.aac", true},
		{"song.ogg", true},
		{"song.opus", true},
		{"song.FLAC", true},
		{"movie.mkv", false},
		{"movie.mp4", false},
		{"song.wav", false},
		{"file.txt", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isMusicFile(tt.path); got != tt.want {
				t.Errorf("isMusicFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestTrackNumberRE(t *testing.T) {
	tests := []struct {
		input    string
		wantNum  string
		wantRest string
	}{
		{"01 - Time", "01", "Time"},
		{"03. Song", "03", "Song"},
		{"12 Song Name", "12", "Song Name"},
		{"5-Track", "5", "Track"},
		{"101 - Track", "101", "Track"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			m := trackNumberRE.FindStringSubmatch(tt.input)
			if m == nil {
				t.Fatalf("no match for %q", tt.input)
			}
			if m[1] != tt.wantNum {
				t.Errorf("number: got %q, want %q", m[1], tt.wantNum)
			}
			rest := tt.input[len(m[0]):]
			if rest != tt.wantRest {
				t.Errorf("rest: got %q, want %q", rest, tt.wantRest)
			}
		})
	}
}
