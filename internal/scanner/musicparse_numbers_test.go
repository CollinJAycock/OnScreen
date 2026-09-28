package scanner

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// writeTaggedFLAC writes the smallest file dhowden/tag reads as FLAC: the
// "fLaC" marker plus one (last) VORBIS_COMMENT metadata block.
func writeTaggedFLAC(t *testing.T, path string, comments ...string) {
	t.Helper()
	var body []byte
	le32 := func(n int) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, uint32(n)); return b }
	vendor := "onscreen-test"
	body = append(body, le32(len(vendor))...)
	body = append(body, vendor...)
	body = append(body, le32(len(comments))...)
	for _, c := range comments {
		body = append(body, le32(len(c))...)
		body = append(body, c...)
	}
	hdr := []byte{0x80 | 4, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}
	data := append(append([]byte("fLaC"), hdr...), body...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Vorbis "N/M" track and disc numbers ("02/12", "1/2") are common in the wild,
// but dhowden/tag parses them with a bare Atoi and returns 0, which left whole
// albums without a track order.
func TestReadMusicTags_VorbisNofMNumbers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Artist", "Album", "whatever.flac")
	writeTaggedFLAC(t, path,
		"TITLE=Second Movement", "ARTIST=Gapless Artist", "ALBUM=Gapless Album",
		"TRACKNUMBER=02/3", "DISCNUMBER=1/2")
	tags, err := ReadMusicTags(path)
	if err != nil {
		t.Fatalf("ReadMusicTags: %v", err)
	}
	if tags.Title != "Second Movement" {
		t.Fatalf("embedded tags weren't read (title %q)", tags.Title)
	}
	if tags.Track != 2 || tags.TrackTotal != 3 {
		t.Errorf("track: got %d/%d, want 2/3", tags.Track, tags.TrackTotal)
	}
	if tags.Disc != 1 || tags.DiscTotal != 2 {
		t.Errorf("disc: got %d/%d, want 1/2", tags.Disc, tags.DiscTotal)
	}
}

// A file with good artist/title tags but no track number takes the number
// from its "07 - Title" filename instead of having none.
func TestReadMusicTags_TrackFromFilenameWhenTagMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Artist", "Album", "07 - Song.flac")
	writeTaggedFLAC(t, path, "TITLE=Song", "ARTIST=Someone", "ALBUM=Album")
	tags, err := ReadMusicTags(path)
	if err != nil {
		t.Fatalf("ReadMusicTags: %v", err)
	}
	if tags.Track != 7 {
		t.Errorf("track: got %d, want 7 from the filename", tags.Track)
	}
}

func TestXOfN(t *testing.T) {
	for _, tc := range []struct {
		in            string
		total         int
		wantN, wantOf int
	}{
		{"02/12", 0, 2, 12},
		{" 3 / 9 ", 0, 3, 9},
		{"5", 11, 5, 11},
		{"4/", 8, 4, 8},
		{"", 3, 0, 3},
		{"x/3", 0, 0, 0},
		{"-1", 0, 0, 0},
	} {
		n, of := xOfN(tc.in, tc.total)
		if n != tc.wantN || of != tc.wantOf {
			t.Errorf("xOfN(%q, %d) = %d/%d, want %d/%d", tc.in, tc.total, n, of, tc.wantN, tc.wantOf)
		}
	}
}
