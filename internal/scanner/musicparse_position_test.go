package scanner

import (
	"path/filepath"
	"testing"
)

// parseMusicPath is the no-I/O source of a track's position: the rescan heal
// trusts it before reading a file. "1-01 Title" (the iTunes / foobar2000
// multi-disc name) used to come back as track 1 of every disc-1 file.
func TestParseMusicPath_Position(t *testing.T) {
	for _, tc := range []struct {
		rel         string
		track, disc int
		title       string // "" = don't check
	}{
		{rel: "Artist/Album/07 - Song.flac", track: 7, title: "Song"},
		{rel: "Artist/Album/Song.flac", title: "Song"},
		{rel: "Artist/Album/1-01 Song.flac", track: 1, disc: 1, title: "Song"},
		{rel: "Artist/Album/2-05 - Song.m4a", track: 5, disc: 2, title: "Song"},
		{rel: "Artist/Album/1.03. Song.flac", track: 3, disc: 1, title: "Song"},
		{rel: "Artist/Album/2-11.flac", track: 11, disc: 2},
		// Leading numbers that aren't a disc-track pair.
		{rel: "Artist/Album/1-800-273-8255.mp3", track: 1},
		{rel: "Artist/Album/24-7.flac", track: 24},
		{rel: "Artist/Album/0-01 Song.flac"},
		// Disc folders.
		{rel: "Artist/Album/CD2/05 - Song.flac", track: 5, disc: 2, title: "Song"},
		{rel: "Artist/Album/Disc 3/05 - Song.flac", track: 5, disc: 3},
		{rel: "Artist/Album (Disc 3)/05 - Song.flac", track: 5, disc: 3},
		{rel: "Artist/Album [CD 2]/05 Song.flac", track: 5, disc: 2},
		{rel: "Artist/disk_04/05 Song.flac", track: 5, disc: 4},
		{rel: "Artist/Disco 2000/05 Song.flac", track: 5},
		{rel: "Artist/ABCD 1/05 Song.flac", track: 5},
		{rel: "Artist/CD 0/05 Song.flac", track: 5},
		// The filename's disc wins over the folder's.
		{rel: "Artist/CD1/2-05 Song.flac", track: 5, disc: 2},
	} {
		got := parseMusicPath(filepath.Join("music", filepath.FromSlash(tc.rel)))
		if got.Track != tc.track || got.Disc != tc.disc {
			t.Errorf("%s: disc %d track %d, want disc %d track %d", tc.rel, got.Disc, got.Track, tc.disc, tc.track)
		}
		if tc.title != "" && got.Title != tc.title {
			t.Errorf("%s: title %q, want %q", tc.rel, got.Title, tc.title)
		}
	}
}

// A multi-disc rip whose tags carry no disc number takes it from the path, so
// its tracks don't all claim disc 1's positions. A disc tag still wins.
func TestReadMusicTags_DiscFromPathWhenTagMissing(t *testing.T) {
	dir := t.TempDir()
	untagged := filepath.Join(dir, "Artist", "Box", "CD2", "05 - Song.flac")
	writeTaggedFLAC(t, untagged, "TITLE=Song", "ARTIST=Artist", "ALBUM=Box", "TRACKNUMBER=5")
	tags, err := ReadMusicTags(untagged)
	if err != nil {
		t.Fatal(err)
	}
	if tags.Disc != 2 || tags.Track != 5 {
		t.Errorf("disc %d track %d, want disc 2 track 5", tags.Disc, tags.Track)
	}

	tagged := filepath.Join(dir, "Artist", "Box2", "CD2", "05 - Song.flac")
	writeTaggedFLAC(t, tagged, "TITLE=Song", "ARTIST=Artist", "ALBUM=Box2", "TRACKNUMBER=5", "DISCNUMBER=1")
	if tags, err = ReadMusicTags(tagged); err != nil {
		t.Fatal(err)
	}
	if tags.Disc != 1 {
		t.Errorf("disc %d, want the tag's 1 over the folder's 2", tags.Disc)
	}
}
