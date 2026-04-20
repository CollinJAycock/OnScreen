// Package scanner - musicparse reads audio metadata tags from music files.
// Supports ID3v1/v2 (MP3), MP4/M4A, FLAC/Vorbis, and OGG via the dhowden/tag library.
// When tags are missing or unreadable it falls back to folder-structure parsing
// using the common Artist/Album/01 - Track.ext convention.
package scanner

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// MusicTags holds the metadata extracted from an audio file.
type MusicTags struct {
	Artist   string
	Album    string
	Title    string
	Track    int // track number (0 if not set)
	Disc     int // disc number (0 if not set)
	Year     int // 0 if not set
	Genre    string
	AlbumArt bool // true if embedded artwork exists
}

// trackNumberRE matches a leading track number like "01 - ", "01. ", "01 ", "1-".
var trackNumberRE = regexp.MustCompile(`^(\d{1,3})\s*[-.\s]\s*`)

// ReadMusicTags reads audio metadata from filePath.
// It first tries to read embedded tags. If that fails or returns empty
// artist/title, it falls back to parsing the folder structure.
func ReadMusicTags(filePath string) (*MusicTags, error) {
	tags, tagErr := readEmbeddedTags(filePath)
	if tagErr != nil || tags.Artist == "" || tags.Title == "" {
		// Fall back to folder-based parsing. Merge with any partial tag data.
		fb := parseMusicPath(filePath)
		if tags == nil {
			tags = fb
		} else {
			if tags.Artist == "" {
				tags.Artist = fb.Artist
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
	return tags, nil
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

	trackNum, _ := m.Track()
	discNum, _ := m.Disc()

	mt := &MusicTags{
		Artist:   strings.TrimSpace(m.Artist()),
		Album:    strings.TrimSpace(m.Album()),
		Title:    strings.TrimSpace(m.Title()),
		Track:    trackNum,
		Disc:     discNum,
		Year:     m.Year(),
		Genre:    strings.TrimSpace(m.Genre()),
		AlbumArt: m.Picture() != nil,
	}

	// Use AlbumArtist if Artist is empty (common in compilations).
	if mt.Artist == "" {
		mt.Artist = strings.TrimSpace(m.AlbumArtist())
	}

	return mt, nil
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

	return mt
}
