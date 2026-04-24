package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
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
		{"song.wav", true},
		{"song.aif", true},
		{"song.aiff", true},
		{"song.alac", true},
		{"song.wv", true},
		{"song.ape", true},
		{"song.tak", true},
		{"song.dsf", true},
		{"song.dff", true},
		{"movie.mkv", false},
		{"movie.mp4", false},
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

func TestParseMBID(t *testing.T) {
	t.Run("valid uuid", func(t *testing.T) {
		raw := "abcdef12-3456-7890-abcd-ef1234567890"
		got := parseMBID(raw)
		want, _ := uuid.Parse(raw)
		if got != want {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("empty returns nil uuid", func(t *testing.T) {
		if got := parseMBID(""); got != uuid.Nil {
			t.Errorf("got %v, want uuid.Nil", got)
		}
	})
	t.Run("garbage returns nil uuid", func(t *testing.T) {
		if got := parseMBID("not-a-uuid"); got != uuid.Nil {
			t.Errorf("got %v, want uuid.Nil", got)
		}
	})
	t.Run("byte slice (FLAC vorbis) parses", func(t *testing.T) {
		raw := []byte("abcdef12-3456-7890-abcd-ef1234567890")
		got := parseMBID(raw)
		want, _ := uuid.Parse(string(raw))
		if got != want {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestParseReplayGain(t *testing.T) {
	cases := []struct {
		in   string
		want *float64
	}{
		{"-7.89 dB", floatPtr(-7.89)},
		{"-7.89dB", floatPtr(-7.89)},
		{"+2.15 dB", floatPtr(2.15)},
		{"-7.89", floatPtr(-7.89)},
		{"  -3.0 dB  ", floatPtr(-3.0)},
		{"", nil},
		{"garbage", nil},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := parseReplayGain(tc.in)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("nil-ness mismatch: got %v, want %v", got, tc.want)
			}
			if got != nil && *got != *tc.want {
				t.Errorf("got %v, want %v", *got, *tc.want)
			}
		})
	}
}

func TestParseReplayGainPeak_AcceptsOverOne(t *testing.T) {
	// Peaks above 1.0 are legal (intersample peaks) and must NOT be clamped.
	got := parseReplayGainPeak("1.0247")
	if got == nil || *got != 1.0247 {
		t.Errorf("intersample peak 1.0247: got %v, want 1.0247", got)
	}
}

func TestParseTruthy(t *testing.T) {
	cases := []struct {
		in   interface{}
		want bool
	}{
		{nil, false},
		{true, true},
		{false, false},
		{1, true},
		{0, false},
		{int64(1), true},
		{uint8(1), true},
		{"1", true},
		{"true", true},
		{"yes", true},
		{"Y", true},
		{"", false},
		{"no", false},
	}
	for _, tc := range cases {
		if got := parseTruthy(tc.in); got != tc.want {
			t.Errorf("parseTruthy(%v): got %v, want %v", tc.in, got, tc.want)
		}
	}
}

func floatPtr(f float64) *float64 { return &f }

func TestParseReplayGain_EdgeCases(t *testing.T) {
	t.Run("zero gain", func(t *testing.T) {
		got := parseReplayGain("0.0 dB")
		if got == nil || *got != 0.0 {
			t.Errorf("got %v, want 0.0", got)
		}
	})
	t.Run("uppercase DB unit", func(t *testing.T) {
		// We strip "dB" / "db" but not "DB". Documents current behavior;
		// real-world taggers always use "dB" but if this changes the test will
		// flag it.
		got := parseReplayGain("-7.89 DB")
		if got != nil {
			t.Errorf("DB suffix not stripped: got %v, want nil", got)
		}
	})
	t.Run("scientific notation", func(t *testing.T) {
		got := parseReplayGain("-1.5e1 dB")
		if got == nil || *got != -15.0 {
			t.Errorf("got %v, want -15.0", got)
		}
	})
	t.Run("byte slice input", func(t *testing.T) {
		got := parseReplayGain([]byte("-7.89 dB"))
		if got == nil || *got != -7.89 {
			t.Errorf("got %v, want -7.89", got)
		}
	})
	t.Run("nil input", func(t *testing.T) {
		if got := parseReplayGain(nil); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestParseReplayGainPeak_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want *float64
	}{
		{"normal peak", "0.988734", floatPtr(0.988734)},
		{"intersample peak above 1.0", "1.0247", floatPtr(1.0247)},
		{"large peak preserved", "2.5", floatPtr(2.5)},
		{"zero peak", "0", floatPtr(0)},
		{"empty", "", nil},
		{"garbage", "loud", nil},
		{"nil", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseReplayGainPeak(tc.in)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("nil-ness mismatch: got %v, want %v", got, tc.want)
			}
			if got != nil && *got != *tc.want {
				t.Errorf("got %v, want %v", *got, *tc.want)
			}
		})
	}
}

func TestParseYear(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want int
	}{
		{"plain year", "2021", 2021},
		{"ISO date", "2021-03-17", 2021},
		{"ISO month", "2021-03", 2021},
		{"slash date", "2021/03/17", 2021},
		{"int year", 1973, 1973},
		{"int64 year", int64(1973), 1973},
		{"too short", "202", 0},
		{"empty", "", 0},
		{"garbage", "abcd-ef-gh", 0},
		{"out of range low", "0999", 0},
		{"out of range high not possible (5+ digits truncate)", "10000", 1000}, // documents that we slice [:4]
		{"nil", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseYear(tc.in); got != tc.want {
				t.Errorf("parseYear(%v): got %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestSplitGenres(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "Rock", []string{"Rock"}},
		{"slash", "Rock/Pop", []string{"Rock", "Pop"}},
		{"semicolon", "Rock; Pop", []string{"Rock", "Pop"}},
		{"comma", "Rock, Pop, Blues", []string{"Rock", "Pop", "Blues"}},
		{"NUL separator (ID3v2.4)", "Rock\x00Pop\x00Jazz", []string{"Rock", "Pop", "Jazz"}},
		{"mixed separators", "Rock/Pop; Blues, Jazz", []string{"Rock", "Pop", "Blues", "Jazz"}},
		{"surrounding whitespace", "  Rock  ;  Pop  ", []string{"Rock", "Pop"}},
		{"empty parts collapsed", "Rock//Pop", []string{"Rock", "Pop"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitGenres(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("len: got %d (%v), want %d (%v)", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d]: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestRawLookup_CaseFallback(t *testing.T) {
	t.Run("exact key match wins", func(t *testing.T) {
		raw := map[string]interface{}{
			"MUSICBRAINZ_TRACKID":    "exact",
			"MusicBrainz Track Id":   "mixed",
			"musicbrainz_trackid":    "lower",
		}
		got := rawLookup(raw, "MUSICBRAINZ_TRACKID")
		if got != "exact" {
			t.Errorf("got %v, want exact", got)
		}
	})
	t.Run("upper-case fallback", func(t *testing.T) {
		raw := map[string]interface{}{"MUSICBRAINZ_TRACKID": "found"}
		got := rawLookup(raw, "musicbrainz_trackid")
		if got != "found" {
			t.Errorf("got %v, want found", got)
		}
	})
	t.Run("lower-case fallback", func(t *testing.T) {
		raw := map[string]interface{}{"musicbrainz_trackid": "found"}
		got := rawLookup(raw, "MUSICBRAINZ_TRACKID")
		if got != "found" {
			t.Errorf("got %v, want found", got)
		}
	})
	t.Run("first key in list wins over later", func(t *testing.T) {
		raw := map[string]interface{}{
			"MusicBrainz Track Id": "id3",
			"MUSICBRAINZ_TRACKID":  "vorbis",
		}
		got := rawLookup(raw, "MUSICBRAINZ_TRACKID", "MusicBrainz Track Id")
		if got != "vorbis" {
			t.Errorf("got %v, want vorbis (first key)", got)
		}
	})
	t.Run("nil value treated as absent", func(t *testing.T) {
		raw := map[string]interface{}{
			"MUSICBRAINZ_TRACKID":  nil,
			"MusicBrainz Track Id": "found",
		}
		got := rawLookup(raw, "MUSICBRAINZ_TRACKID", "MusicBrainz Track Id")
		if got != "found" {
			t.Errorf("got %v, want found", got)
		}
	})
	t.Run("none found returns nil", func(t *testing.T) {
		got := rawLookup(map[string]interface{}{}, "X", "Y")
		if got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestEnrichFromRaw_VorbisStyle(t *testing.T) {
	// FLAC/Vorbis tags use UPPERCASE keys.
	mbRecording := "11111111-1111-1111-1111-111111111111"
	mbRelease := "22222222-2222-2222-2222-222222222222"
	mbReleaseGroup := "33333333-3333-3333-3333-333333333333"
	mbArtist := "44444444-4444-4444-4444-444444444444"
	mbAlbumArtist := "55555555-5555-5555-5555-555555555555"

	raw := map[string]interface{}{
		"MUSICBRAINZ_TRACKID":         mbRecording,
		"MUSICBRAINZ_ALBUMID":         mbRelease,
		"MUSICBRAINZ_RELEASEGROUPID":  mbReleaseGroup,
		"MUSICBRAINZ_ARTISTID":        mbArtist,
		"MUSICBRAINZ_ALBUMARTISTID":   mbAlbumArtist,
		"REPLAYGAIN_TRACK_GAIN":       "-7.89 dB",
		"REPLAYGAIN_TRACK_PEAK":       "0.988734",
		"REPLAYGAIN_ALBUM_GAIN":       "-8.12 dB",
		"REPLAYGAIN_ALBUM_PEAK":       "1.0247",
		"COMPILATION":                 "1",
		"RELEASETYPE":                 "Album",
		"ORIGINALDATE":                "1973-03-01",
	}
	m := &MusicTags{}
	m.enrichFromRaw(raw)

	if m.MBRecordingID.String() != mbRecording {
		t.Errorf("MBRecordingID: got %v, want %s", m.MBRecordingID, mbRecording)
	}
	if m.MBReleaseID.String() != mbRelease {
		t.Errorf("MBReleaseID: got %v, want %s", m.MBReleaseID, mbRelease)
	}
	if m.MBReleaseGroupID.String() != mbReleaseGroup {
		t.Errorf("MBReleaseGroupID: got %v, want %s", m.MBReleaseGroupID, mbReleaseGroup)
	}
	if m.MBArtistID.String() != mbArtist {
		t.Errorf("MBArtistID: got %v, want %s", m.MBArtistID, mbArtist)
	}
	if m.MBAlbumArtistID.String() != mbAlbumArtist {
		t.Errorf("MBAlbumArtistID: got %v, want %s", m.MBAlbumArtistID, mbAlbumArtist)
	}
	if m.ReplayGainTrackGain == nil || *m.ReplayGainTrackGain != -7.89 {
		t.Errorf("ReplayGainTrackGain: got %v, want -7.89", m.ReplayGainTrackGain)
	}
	if m.ReplayGainTrackPeak == nil || *m.ReplayGainTrackPeak != 0.988734 {
		t.Errorf("ReplayGainTrackPeak: got %v, want 0.988734", m.ReplayGainTrackPeak)
	}
	if m.ReplayGainAlbumGain == nil || *m.ReplayGainAlbumGain != -8.12 {
		t.Errorf("ReplayGainAlbumGain: got %v, want -8.12", m.ReplayGainAlbumGain)
	}
	if m.ReplayGainAlbumPeak == nil || *m.ReplayGainAlbumPeak != 1.0247 {
		t.Errorf("ReplayGainAlbumPeak: got %v, want 1.0247", m.ReplayGainAlbumPeak)
	}
	if !m.Compilation {
		t.Error("Compilation: got false, want true")
	}
	if m.ReleaseType != "album" {
		t.Errorf("ReleaseType: got %q, want %q", m.ReleaseType, "album")
	}
	if m.OriginalYear != 1973 {
		t.Errorf("OriginalYear: got %d, want 1973", m.OriginalYear)
	}
}

func TestEnrichFromRaw_ID3Style(t *testing.T) {
	// ID3 (MP3) tags use mixed-case "MusicBrainz Xxx Id" keys.
	mbRecording := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	raw := map[string]interface{}{
		"MusicBrainz Track Id":  mbRecording,
		"replaygain_track_gain": "-3.5 dB",
		"TCMP":                  "1",
		"TDOR":                  "1969",
	}
	m := &MusicTags{}
	m.enrichFromRaw(raw)

	if m.MBRecordingID.String() != mbRecording {
		t.Errorf("MBRecordingID: got %v, want %s", m.MBRecordingID, mbRecording)
	}
	if m.ReplayGainTrackGain == nil || *m.ReplayGainTrackGain != -3.5 {
		t.Errorf("ReplayGainTrackGain: got %v, want -3.5", m.ReplayGainTrackGain)
	}
	if !m.Compilation {
		t.Error("Compilation (TCMP): got false, want true")
	}
	if m.OriginalYear != 1969 {
		t.Errorf("OriginalYear (TDOR): got %d, want 1969", m.OriginalYear)
	}
}

func TestEnrichFromRaw_MP4FreeformStyle(t *testing.T) {
	// MP4 freeform tags use the ----:com.apple.iTunes:Xxx prefix.
	mbRecording := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	raw := map[string]interface{}{
		"----:com.apple.iTunes:MusicBrainz Track Id":  mbRecording,
		"----:com.apple.iTunes:replaygain_track_gain": "-2.0 dB",
		"----:com.apple.iTunes:MusicBrainz Album Type": "EP",
		"cpil": 1,
	}
	m := &MusicTags{}
	m.enrichFromRaw(raw)

	if m.MBRecordingID.String() != mbRecording {
		t.Errorf("MBRecordingID: got %v, want %s", m.MBRecordingID, mbRecording)
	}
	if m.ReplayGainTrackGain == nil || *m.ReplayGainTrackGain != -2.0 {
		t.Errorf("ReplayGainTrackGain: got %v, want -2.0", m.ReplayGainTrackGain)
	}
	if m.ReleaseType != "ep" {
		t.Errorf("ReleaseType: got %q, want %q", m.ReleaseType, "ep")
	}
	if !m.Compilation {
		t.Error("Compilation (cpil int): got false, want true")
	}
}

func TestEnrichFromRaw_AbsentTags(t *testing.T) {
	// Empty raw map — all fields should remain zero values, no panics.
	m := &MusicTags{}
	m.enrichFromRaw(map[string]interface{}{})

	if m.MBRecordingID != uuid.Nil {
		t.Errorf("MBRecordingID: got %v, want uuid.Nil", m.MBRecordingID)
	}
	if m.ReplayGainTrackGain != nil {
		t.Errorf("ReplayGainTrackGain: got %v, want nil", m.ReplayGainTrackGain)
	}
	if m.Compilation {
		t.Error("Compilation: got true, want false")
	}
	if m.ReleaseType != "" {
		t.Errorf("ReleaseType: got %q, want empty", m.ReleaseType)
	}
	if m.OriginalYear != 0 {
		t.Errorf("OriginalYear: got %d, want 0", m.OriginalYear)
	}
}

func TestStringifyRaw_ByteSlice(t *testing.T) {
	// dhowden/tag returns []byte for some MP4 freeform tags. Documents the
	// regression fix where parseMBID/parseReplayGain previously failed on []byte
	// because stringifyRaw fell through to %v formatting.
	got := stringifyRaw([]byte("hello"))
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
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

// ── Empty-album-title fallback ───────────────────────────────────────────────

func TestAlbumTitleOrFallback(t *testing.T) {
	cases := []struct {
		name        string
		tagged      string
		filePath    string
		artistTitle string
		want        string
	}{
		{
			name:        "tagged wins",
			tagged:      "Dark Side of the Moon",
			filePath:    "/music/Pink Floyd/X/track.flac",
			artistTitle: "Pink Floyd",
			want:        "Dark Side of the Moon",
		},
		{
			name:        "tagged whitespace-only treated as empty",
			tagged:      "   ",
			filePath:    "/music/Pink Floyd/Wish You Were Here/01.flac",
			artistTitle: "Pink Floyd",
			want:        "Wish You Were Here",
		},
		{
			name:        "empty tag, hierarchical layout uses parent dir",
			tagged:      "",
			filePath:    "/music/Allman Brothers Band/Win Lose Or Draw/01.flac",
			artistTitle: "The Allman Brothers Band",
			want:        "Win Lose Or Draw",
		},
		{
			name:        "empty tag, flat layout (parent == artist) falls back to Untitled Album",
			tagged:      "",
			filePath:    "/music/Pink Floyd/track.flac",
			artistTitle: "Pink Floyd",
			want:        "Untitled Album",
		},
		{
			name:        "empty tag, flat layout case-insensitive",
			tagged:      "",
			filePath:    "/music/THE ALLMAN BROTHERS BAND/track.flac",
			artistTitle: "The Allman Brothers Band",
			want:        "Untitled Album",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := albumTitleOrFallback(tc.tagged, tc.filePath, tc.artistTitle); got != tc.want {
				t.Errorf("albumTitleOrFallback(%q, %q, %q) = %q, want %q",
					tc.tagged, tc.filePath, tc.artistTitle, got, tc.want)
			}
		})
	}
}

// ── Collab artist parsing ────────────────────────────────────────────────────

func TestPrimaryAndSecondaryArtistName(t *testing.T) {
	cases := []struct {
		input            string
		wantPrimary      string
		wantSecondary    string
	}{
		// Two-name collabs across each separator type
		{"Elton John & Bonnie Raitt", "Elton John", "Bonnie Raitt"},
		{"Glen Campbell & Elton John", "Glen Campbell", "Elton John"},
		{"Beyonce, Jay-Z", "Beyonce", "Jay-Z"},
		{"Beyonce / Jay-Z", "Beyonce", "Jay-Z"},
		{"Jay-Z feat. Rihanna", "Jay-Z", "Rihanna"},
		{"Jay-Z featuring Rihanna", "Jay-Z", "Rihanna"},
		{"Eminem ft. Dido", "Eminem", "Dido"},
		{"David Bowie with Mick Jagger", "David Bowie", "Mick Jagger"},

		// Triple-name with hyphen separator (the Bo Diddley case)
		{"Bo Diddley - Muddy Waters - Little Walter", "Bo Diddley", "Little Walter"},

		// Single-name hyphens must NOT match (whitespace required around hyphen)
		{"Jay-Z", "", ""},
		{"Wu-Tang Clan", "", ""},

		// Bands with separators in their names (no canonical → callers leave alone)
		{"Simon & Garfunkel", "Simon", "Garfunkel"},

		// No collab markers
		{"The Beatles", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got := primaryArtistName(tc.input); got != tc.wantPrimary {
				t.Errorf("primaryArtistName(%q) = %q, want %q", tc.input, got, tc.wantPrimary)
			}
			if got := secondaryArtistName(tc.input); got != tc.wantSecondary {
				t.Errorf("secondaryArtistName(%q) = %q, want %q", tc.input, got, tc.wantSecondary)
			}
		})
	}
}
