package scanner

import (
	"regexp"
	"strconv"
	"strings"
)

// FolderIDs holds the external IDs Sonarr/Radarr/TRaSH-style folder
// names embed. Any field is zero/empty when the corresponding marker
// isn't present.
//
// The TRaSH guides recommend `{Series TitleYear}` as the bare folder
// shape, with optional ID-suffixed variants for Plex / Emby / Jellyfin:
//
//	The Office (2005) {imdb-tt0386676}
//	The Office (2005) {tvdb-73244}
//	The Office (2005) [tvdbid-73244]
//	Frieren {tmdb-209867}
//
// Same idea on Radarr's movie folders. Recognising these markers lets
// the scanner match a folder to the canonical row by ID even when the
// title parsing would otherwise produce a fresh stub (e.g. release
// group prefixes, foreign-language titles, abbreviated suffixes like
// `IE` vs `(Ireland)`). It also prevents future duplicate-row creation
// because the ID is the authoritative match key.
type FolderIDs struct {
	TMDBID int
	TVDBID int
	IMDBID string // "tt..." form, kept verbatim
}

// Each pattern captures the numeric / IMDb portion in group 1.
var (
	reTMDBBraces = regexp.MustCompile(`(?i)\{\s*tmdb-?\s*(\d+)\s*\}`)
	reTVDBBraces = regexp.MustCompile(`(?i)\{\s*tvdb-?\s*(\d+)\s*\}`)
	reIMDBBraces = regexp.MustCompile(`(?i)\{\s*imdb-?\s*(tt\d+)\s*\}`)

	// Bracketed (Jellyfin / Emby) variants — also match the
	// `tvdbid-` / `tmdbid-` / `imdbid-` long forms.
	reTMDBBrackets = regexp.MustCompile(`(?i)\[\s*tmdb(?:id)?-?\s*(\d+)\s*\]`)
	reTVDBBrackets = regexp.MustCompile(`(?i)\[\s*tvdb(?:id)?-?\s*(\d+)\s*\]`)
	reIMDBBrackets = regexp.MustCompile(`(?i)\[\s*imdb(?:id)?-?\s*(tt\d+)\s*\]`)

	// Master pattern for stripping every recognised ID marker from a
	// folder name — produces a clean title for downstream parsing.
	// Matches a leading separator (space / dot / underscore / dash)
	// so the strip doesn't leave double spaces.
	reAnyIDMarker = regexp.MustCompile(`(?i)[\s._-]*[\{\[]\s*(?:tmdb|tvdb|imdb)(?:id)?-?\s*(?:tt)?\d+\s*[\}\]]`)
)

// ParseFolderIDs extracts external-id markers from a folder name. The
// folder name is the *base* of the show / movie root directory — not
// a full path — so callers should run filepath.Base first when
// they have an absolute path.
func ParseFolderIDs(folderName string) FolderIDs {
	var ids FolderIDs

	if m := reTMDBBraces.FindStringSubmatch(folderName); m != nil {
		ids.TMDBID = atoiSafe(m[1])
	} else if m := reTMDBBrackets.FindStringSubmatch(folderName); m != nil {
		ids.TMDBID = atoiSafe(m[1])
	}

	if m := reTVDBBraces.FindStringSubmatch(folderName); m != nil {
		ids.TVDBID = atoiSafe(m[1])
	} else if m := reTVDBBrackets.FindStringSubmatch(folderName); m != nil {
		ids.TVDBID = atoiSafe(m[1])
	}

	if m := reIMDBBraces.FindStringSubmatch(folderName); m != nil {
		ids.IMDBID = strings.ToLower(m[1])
	} else if m := reIMDBBrackets.FindStringSubmatch(folderName); m != nil {
		ids.IMDBID = strings.ToLower(m[1])
	}

	return ids
}

// StripFolderIDMarkers removes every recognised ID marker from a
// folder name and trims whitespace. Used by the title parser so
// "Frieren {tmdb-209867}" becomes "Frieren" before downstream
// normalisation, instead of leaking the marker into the show title.
func StripFolderIDMarkers(folderName string) string {
	cleaned := reAnyIDMarker.ReplaceAllString(folderName, "")
	return strings.TrimSpace(cleaned)
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
