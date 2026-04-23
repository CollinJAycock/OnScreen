// Package scanner - musicparse reads audio metadata tags from music files.
// Supports ID3v1/v2 (MP3), MP4/M4A, FLAC/Vorbis, and OGG via the dhowden/tag library.
// When tags are missing or unreadable it falls back to folder-structure parsing
// using the common Artist/Album/01 - Track.ext convention.
package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// MusicTags holds the metadata extracted from an audio file.
type MusicTags struct {
	Artist      string
	AlbumArtist string
	Album       string
	Title       string
	Track       int // track number (0 if not set)
	TrackTotal  int // total tracks on the disc (0 if not set)
	Disc        int // disc number (0 if not set)
	DiscTotal   int // total discs in the set (0 if not set)
	Year        int // 0 if not set
	// OriginalYear is the "year this work was first released" as distinct
	// from Year, which on a reissue holds the reissue year. Populated from
	// ID3 TDOR / Vorbis ORIGINALDATE / MP4 @day variant.
	OriginalYear int
	// Genres holds the full genre list. Genre (legacy single-string) is kept
	// for backwards compatibility with existing callers and holds the first
	// entry.
	Genres   []string
	Genre    string
	Composer string
	Lyrics   string
	// Compilation is true for various-artists albums, soundtracks, etc.
	Compilation bool
	// ReleaseType matches the MusicBrainz vocabulary: Album, Single, EP,
	// Compilation, Live, Soundtrack, Remix, Broadcast, Demo.
	ReleaseType string
	AlbumArt    bool // true if embedded artwork exists
	// MusicBrainz cross-reference IDs. These are stored verbatim from tags;
	// `Track` here = recording MBID (MusicBrainz's canonical term for a
	// performance), not the track number.
	MBRecordingID    uuid.UUID
	MBReleaseID      uuid.UUID
	MBReleaseGroupID uuid.UUID
	MBArtistID       uuid.UUID
	MBAlbumArtistID  uuid.UUID
	// ReplayGain tags. Values in dB for gain, linear peak (0.0–1.0+). These
	// are pointers because 0 dB is a valid gain — nil means "tag absent".
	ReplayGainTrackGain *float64
	ReplayGainTrackPeak *float64
	ReplayGainAlbumGain *float64
	ReplayGainAlbumPeak *float64
}

// trackNumberRE matches a leading track number like "01 - ", "01. ", "01 ", "1-".
var trackNumberRE = regexp.MustCompile(`^(\d{1,3})\s*[-.\s]\s*`)

// cleanTag sanitizes a tag string for safe DB storage. ID3 tags can return
// Latin-1 or other non-UTF8 byte sequences that Postgres rejects; we scrub
// invalid UTF-8 and also drop NUL bytes, which Postgres disallows in text.
func cleanTag(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\x00", "")
	return strings.TrimSpace(s)
}

// ReadMusicTags reads audio metadata from filePath.
// It first tries to read embedded tags. If that fails or returns empty
// artist/title, it falls back to parsing the folder structure.
func ReadMusicTags(filePath string) (*MusicTags, error) {
	tags, tagErr := readEmbeddedTags(filePath)
	if tagErr != nil || tags.effectiveArtist() == "" || tags.Title == "" {
		// Fall back to folder-based parsing. Merge with any partial tag data.
		fb := parseMusicPath(filePath)
		if tags == nil {
			tags = fb
		} else {
			if tags.Artist == "" {
				tags.Artist = fb.Artist
			}
			if tags.AlbumArtist == "" {
				tags.AlbumArtist = fb.AlbumArtist
			}
			if tags.Album == "" {
				tags.Album = fb.Album
			}
			if tags.Title == "" {
				tags.Title = fb.Title
			}
			if tags.Track == 0 {
				tags.Track = fb.Track
			}
		}
	}
	// AlbumArtist defaults to Artist when the tag is missing — avoids a hole
	// in the artist/album hierarchy while still letting compilations override
	// it. This matches Picard's behaviour when writing back tags.
	if tags.AlbumArtist == "" {
		tags.AlbumArtist = tags.Artist
	}
	return tags, nil
}

// effectiveArtist returns the best artist string available, preferring the
// track-level Artist tag and falling back to AlbumArtist. Used to decide
// whether the tag-based read produced enough data to skip path parsing.
func (m *MusicTags) effectiveArtist() string {
	if m.Artist != "" {
		return m.Artist
	}
	return m.AlbumArtist
}

// readEmbeddedTags uses dhowden/tag to read metadata from the file.
func readEmbeddedTags(filePath string) (*MusicTags, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	m, err := readTagFrom(f)
	if err != nil {
		return nil, err
	}

	trackNum, trackTotal := m.Track()
	discNum, discTotal := m.Disc()

	mt := &MusicTags{
		Artist:      cleanTag(m.Artist()),
		AlbumArtist: cleanTag(m.AlbumArtist()),
		Album:       cleanTag(m.Album()),
		Title:       cleanTag(m.Title()),
		Track:       trackNum,
		TrackTotal:  trackTotal,
		Disc:        discNum,
		DiscTotal:   discTotal,
		Year:        m.Year(),
		Composer:    cleanTag(m.Composer()),
		Lyrics:      cleanTag(m.Lyrics()),
		AlbumArt:    m.Picture() != nil,
	}

	// Genre — the library returns a single string, but that string may be
	// a format-specific multi-value separator ("/", ";", or NUL-delimited).
	// Split so the DB gets the full list.
	primary := cleanTag(m.Genre())
	mt.Genres = splitGenres(primary)
	if len(mt.Genres) > 0 {
		mt.Genre = mt.Genres[0]
	}

	// Raw() holds everything that's not a first-class method: MusicBrainz
	// IDs, ReplayGain, original date, compilation flag, release type.
	raw := m.Raw()
	mt.enrichFromRaw(raw)

	return mt, nil
}

// enrichFromRaw pulls format-agnostic values out of dhowden/tag's Raw() map.
// Tag names are not consistent across containers, so we try the documented
// variants for each field (ID3 / Vorbis / MP4 / MP4 freeform) and take the
// first non-empty hit.
func (m *MusicTags) enrichFromRaw(raw map[string]interface{}) {
	// MusicBrainz IDs. Vorbis uses UPPERCASE; ID3 uses TXXX:MusicBrainz Xxx
	// Id (mixed case); MP4 uses ----:com.apple.iTunes:MusicBrainz Xxx Id.
	m.MBRecordingID = parseMBID(rawLookup(raw,
		"MUSICBRAINZ_TRACKID",
		"MusicBrainz Track Id",
		"----:com.apple.iTunes:MusicBrainz Track Id",
	))
	m.MBReleaseID = parseMBID(rawLookup(raw,
		"MUSICBRAINZ_ALBUMID",
		"MusicBrainz Album Id",
		"----:com.apple.iTunes:MusicBrainz Album Id",
	))
	m.MBReleaseGroupID = parseMBID(rawLookup(raw,
		"MUSICBRAINZ_RELEASEGROUPID",
		"MusicBrainz Release Group Id",
		"----:com.apple.iTunes:MusicBrainz Release Group Id",
	))
	m.MBArtistID = parseMBID(rawLookup(raw,
		"MUSICBRAINZ_ARTISTID",
		"MusicBrainz Artist Id",
		"----:com.apple.iTunes:MusicBrainz Artist Id",
	))
	m.MBAlbumArtistID = parseMBID(rawLookup(raw,
		"MUSICBRAINZ_ALBUMARTISTID",
		"MusicBrainz Album Artist Id",
		"----:com.apple.iTunes:MusicBrainz Album Artist Id",
	))

	// ReplayGain. Values are stored as strings like "-7.89 dB" / "0.988734".
	m.ReplayGainTrackGain = parseReplayGain(rawLookup(raw,
		"REPLAYGAIN_TRACK_GAIN",
		"replaygain_track_gain",
		"----:com.apple.iTunes:replaygain_track_gain",
	))
	m.ReplayGainTrackPeak = parseReplayGainPeak(rawLookup(raw,
		"REPLAYGAIN_TRACK_PEAK",
		"replaygain_track_peak",
		"----:com.apple.iTunes:replaygain_track_peak",
	))
	m.ReplayGainAlbumGain = parseReplayGain(rawLookup(raw,
		"REPLAYGAIN_ALBUM_GAIN",
		"replaygain_album_gain",
		"----:com.apple.iTunes:replaygain_album_gain",
	))
	m.ReplayGainAlbumPeak = parseReplayGainPeak(rawLookup(raw,
		"REPLAYGAIN_ALBUM_PEAK",
		"replaygain_album_peak",
		"----:com.apple.iTunes:replaygain_album_peak",
	))

	// Compilation flag. MP4: "cpil"; ID3: "TCMP" (iTunes compilation);
	// Vorbis: COMPILATION. Values are truthy strings or ints.
	m.Compilation = parseTruthy(rawLookup(raw, "COMPILATION", "TCMP", "cpil"))

	// Release type. MusicBrainz vocabulary.
	m.ReleaseType = strings.ToLower(cleanTag(stringifyRaw(rawLookup(raw,
		"RELEASETYPE",
		"MusicBrainz Album Type",
		"----:com.apple.iTunes:MusicBrainz Album Type",
	))))

	// Original release year. ID3 v2.4: TDOR; v2.3: TORY; Vorbis:
	// ORIGINALDATE / ORIGINALYEAR; MP4: ©day in some taggers.
	if y := parseYear(rawLookup(raw,
		"ORIGINALDATE",
		"ORIGINALYEAR",
		"TDOR",
		"TORY",
	)); y > 0 {
		m.OriginalYear = y
	}
}

// rawLookup returns the first non-nil value in raw matched by any of keys.
// dhowden/tag's raw maps are not case-consistent — for ID3 keys are mixed
// case ("TDOR"), for Vorbis they are lower-cased by the library in some
// versions. We check each key verbatim and with an upper-cased fallback to
// cover both.
func rawLookup(raw map[string]interface{}, keys ...string) interface{} {
	for _, k := range keys {
		if v, ok := raw[k]; ok && v != nil {
			return v
		}
		if v, ok := raw[strings.ToUpper(k)]; ok && v != nil {
			return v
		}
		if v, ok := raw[strings.ToLower(k)]; ok && v != nil {
			return v
		}
	}
	return nil
}

// stringifyRaw coerces a raw tag value (which may be string, []string, int,
// bool, or format-specific types) to a single string. Returns "" for nil or
// unrecognised types.
func stringifyRaw(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case []string:
		if len(t) > 0 {
			return t[0]
		}
		return ""
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		if t {
			return "1"
		}
		return "0"
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprintf("%v", t)
	}
}

// parseMBID parses a MusicBrainz ID (UUID) from a raw tag value. Returns the
// zero uuid.UUID if parsing fails — callers treat uuid.Nil as "absent".
func parseMBID(v interface{}) uuid.UUID {
	s := cleanTag(stringifyRaw(v))
	if s == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// parseReplayGain parses a ReplayGain gain value like "-7.89 dB" or "-7.89"
// or "+2.15 dB". Returns nil when the tag is absent or unparseable — the
// caller keeps ReplayGain NULL in DB so clients know no gain was computed.
func parseReplayGain(v interface{}) *float64 {
	s := cleanTag(stringifyRaw(v))
	if s == "" {
		return nil
	}
	// Strip trailing " dB" / "dB" (case-insensitive).
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(s), " dB"), " db"))
	s = strings.TrimSuffix(strings.TrimSuffix(s, "dB"), "db")
	s = strings.TrimSpace(s)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

// parseReplayGainPeak parses a linear peak like "0.988734" or "1.024". Peaks
// over 1.0 are legal (intersample peaks) and must not be clamped.
func parseReplayGainPeak(v interface{}) *float64 {
	s := cleanTag(stringifyRaw(v))
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

// parseTruthy reads a compilation-style flag. Accepts "1", "true", "yes",
// and anything non-empty-numeric for MP4 "cpil" which stores a raw int.
func parseTruthy(v interface{}) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case int:
		return t != 0
	case int64:
		return t != 0
	case uint8:
		return t != 0
	}
	s := strings.ToLower(cleanTag(stringifyRaw(v)))
	switch s {
	case "1", "true", "yes", "y":
		return true
	}
	return false
}

// parseYear extracts a 4-digit year from a tag that may be "2021", "2021-03",
// "2021-03-17", or "2021/03/17". Returns 0 when no plausible year is found.
func parseYear(v interface{}) int {
	s := cleanTag(stringifyRaw(v))
	if len(s) < 4 {
		return 0
	}
	year, err := strconv.Atoi(s[:4])
	if err != nil || year < 1000 || year > 9999 {
		return 0
	}
	return year
}

// splitGenres turns a single Genre() return into a list. dhowden/tag already
// joins multi-value ID3 frames into one string; we split on common
// separators so the stored list mirrors the tag. Input is already cleaned.
func splitGenres(s string) []string {
	if s == "" {
		return nil
	}
	// Order matters: "/" first (common in Vorbis when tagger writes one
	// field with slash-separated list), then ";" (ID3v2.3 convention), then
	// "\x00" (ID3v2.4 NUL separator, surviving into the string if cleanTag
	// missed it), then ",".
	seps := []string{"/", ";", "\x00", ","}
	parts := []string{s}
	for _, sep := range seps {
		var next []string
		for _, p := range parts {
			for _, q := range strings.Split(p, sep) {
				q = strings.TrimSpace(q)
				if q != "" {
					next = append(next, q)
				}
			}
		}
		parts = next
	}
	return parts
}

// parseMusicPath derives metadata from the file's path using the common
// directory convention: .../Artist/Album/01 - Track Title.ext
// If fewer than 2 parent directories exist, it uses "Unknown Artist" and
// "Unknown Album" as fallbacks.
func parseMusicPath(filePath string) *MusicTags {
	mt := &MusicTags{}

	base := filepath.Base(filePath)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]

	// Try to extract track number from filename.
	if m := trackNumberRE.FindStringSubmatch(stem); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			mt.Track = n
		}
		stem = stem[len(m[0]):]
	}

	// The remaining stem is the track title.
	mt.Title = strings.TrimSpace(stem)
	if mt.Title == "" {
		mt.Title = "Unknown"
	}

	// Walk up directory hierarchy: parent = album dir, grandparent = artist dir.
	dir := filepath.Dir(filePath)
	albumDir := filepath.Base(dir)
	artistDir := filepath.Base(filepath.Dir(dir))

	if albumDir != "." && albumDir != string(filepath.Separator) {
		mt.Album = albumDir
	} else {
		mt.Album = "Unknown Album"
	}

	if artistDir != "." && artistDir != string(filepath.Separator) {
		mt.Artist = artistDir
	} else {
		mt.Artist = "Unknown Artist"
	}
	// Path-derived AlbumArtist mirrors Artist — the folder hierarchy can't
	// distinguish them, so we let the tag-read path override if present.
	mt.AlbumArtist = mt.Artist

	return mt
}

