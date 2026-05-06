// Package scanner - tvparse extracts show title, season number, and episode
// number from TV media filenames. It handles common naming conventions used by
// Kodi, Sonarr, and scene release groups.
package scanner

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// tvEpisodeRE matches S##E## patterns (case-insensitive): S01E03, s1e3, S01E03E04.
var tvEpisodeRE = regexp.MustCompile(`(?i)[.\s_-]*S(\d{1,2})E(\d{1,3})`)

// tvCrossRE matches the 1x03 pattern: "1x03", "01x03".
var tvCrossRE = regexp.MustCompile(`(?i)[.\s_-]+(\d{1,2})x(\d{1,3})`)

// tvAnimeAbsoluteRE matches "title - NN" anime conventions where the
// number is an absolute episode index rather than a season/episode
// pair. Captures:
//
//   1. Title (greedy minimum so the trailing " - NN" part doesn't get
//      eaten into the title).
//   2. Episode number (1-4 digits — covers everything from a 12-ep
//      season to long-runners like One Piece in the 1000s).
//
// Optional non-capturing `[Group]` prefix strips fansub release-group
// tags ([SubsPlease], [Erai-raws], etc.). Trailing lookahead requires
// whitespace / bracket / dot / underscore / EOL so quality strings
// like `1080p` or `720p` (digit-letter, no separator) don't match —
// only standalone integer episode numbers.
//
// Intentionally requires the " - " separator. Bare "Show NN" patterns
// are too ambiguous (a year suffix like "Show 2024" shouldn't parse
// as episode 2024), so the dash is the explicit signal that what
// follows is an episode number.
var tvAnimeAbsoluteRE = regexp.MustCompile(`^(?:\[[^\]]+\]\s*)?(.+?)\s+-\s+(\d{1,4})(?:\s|\[|\.|_|$)`)

// seasonFolderRE matches "Season 1", "Season 01", "season1", "S01" in folder names.
var seasonFolderRE = regexp.MustCompile(`(?i)^(?:Season\s*(\d{1,2})|S(\d{1,2}))$`)

// ParseTVFilename extracts the show title, season number, and episode number
// from a media file path. It handles:
//   - "Show Name S01E03.mkv" or "Show.Name.S01E03.mkv"
//   - "Show Name - S01E03 - Episode Title.mkv"
//   - "Show Name/Season 1/Show Name S01E03.mkv" (folder structure)
//   - "Show Name 1x03.mkv"
//
// Returns (showTitle, season, episode, ok). If parsing fails, ok is false.
func ParseTVFilename(path string) (showTitle string, season int, episode int, ok bool) {
	// Normalise path separators so we can split on "/" uniformly, including
	// backslashes in Windows-style paths received on non-Windows hosts.
	path = strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`)

	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]

	// Try S##E## first (most common and reliable).
	if m := tvEpisodeRE.FindStringSubmatchIndex(stem); m != nil {
		s, _ := strconv.Atoi(stem[m[2]:m[3]])
		e, _ := strconv.Atoi(stem[m[4]:m[5]])
		title := extractShowTitle(stem[:m[0]], path)
		if title != "" {
			return title, s, e, true
		}
	}

	// Try 1x03 pattern.
	if m := tvCrossRE.FindStringSubmatchIndex(stem); m != nil {
		s, _ := strconv.Atoi(stem[m[2]:m[3]])
		e, _ := strconv.Atoi(stem[m[4]:m[5]])
		title := extractShowTitle(stem[:m[0]], path)
		if title != "" {
			return title, s, e, true
		}
	}

	return "", 0, 0, false
}

// ParseAnimeAbsoluteFilename extracts a show title and absolute
// episode number from anime-style filenames using the "title - NN"
// convention common in fansub releases:
//
//   - "Show Name - 01.mkv"
//   - "Show Name - 1071 [1080p].mkv"
//   - "[SubsPlease] Show Name - 245 (HDR).mkv"
//   - "Show.Name - 12.mkv"
//
// Use as a fallback after [ParseTVFilename] returns ok=false.
//
// Returns (showTitle, episode, ok). The episode is an absolute
// number; the caller is expected to slot the file into a synthetic
// Season 1 (which is conventional for anime browsing — long-running
// series flat-list all episodes by absolute number).
//
// Returns ok=false when:
//   - the filename has no " - " separator before a digit run
//   - the digit run is zero (Episode 0 is reserved for the synthetic
//     "all the things that aren't episodes yet" placeholder elsewhere)
//   - the title prefix is empty after cleaning
func ParseAnimeAbsoluteFilename(path string) (showTitle string, episode int, ok bool) {
	path = strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]

	m := tvAnimeAbsoluteRE.FindStringSubmatchIndex(stem)
	if m == nil {
		return "", 0, false
	}
	e, err := strconv.Atoi(stem[m[4]:m[5]])
	if err != nil || e == 0 {
		return "", 0, false
	}
	title := extractShowTitle(stem[m[2]:m[3]], path)
	if title == "" {
		return "", 0, false
	}
	return title, e, true
}

// episodeKindRE matches anime / TV episode subtype keywords commonly
// used in fansub / scene release filenames. The captures map onto
// the media_items.kind column values (lowercased before storage).
//
// Word-boundary anchored on both sides so quality / source markers
// like `[1080p]` don't false-match. Case-insensitive: `OVA` /
// `Ova` / `ova` all hit. The scanner picks the first non-empty kind
// for a file, with order chosen so the more specific markers (OAD
// before OVA, since OAD is a kind of OVA but the user has been
// explicit) win.
var episodeKindRE = regexp.MustCompile(`(?i)\b(OAD|OVAs?|ONAs?|SPECIALS?|SP|PV|MV)\b`)

// specialsFolderRE matches folder names that flag every contained
// file as a special episode (Plex / Jellyfin convention). Used as a
// fallback when the filename itself has no kind keyword.
var specialsFolderRE = regexp.MustCompile(`(?i)^(specials?|extras?|ovas?|onas?)$`)

// DetectEpisodeKind returns the subtype keyword for an episode file
// path or "" for a regular episode.
//
//   - filename keyword wins first (most specific): OVA / ONA / SP /
//     SPECIAL / PV / MV / OAD anywhere in the stem
//   - folder fallback: a containing folder named "Specials" /
//     "Extras" / "OVAs" / "ONAs" tags every contained file
//   - season 0 fallback: TMDB / TheTVDB convention is that season
//     0 holds specials. Caller passes seasonNum=0 when the file's
//     resolved season is 0.
//
// All return values lowercased to match the canonical column values
// documented on migration 00075.
func DetectEpisodeKind(path string, seasonNum int) string {
	path = strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`)
	base := filepath.Base(path)
	stem := base
	if ext := filepath.Ext(base); ext != "" {
		stem = base[:len(base)-len(ext)]
	}

	// Filename keyword wins — most specific signal.
	if m := episodeKindRE.FindStringSubmatch(stem); m != nil {
		return canonicalEpisodeKind(m[1])
	}

	// Folder name fallback. Split the already-slash-normalised path
	// directly instead of routing through filepath.Dir, which would
	// re-introduce platform-native separators on Windows and break
	// the per-directory scan.
	parts := strings.Split(path, "/")
	// Skip the last segment (the filename); walk parents inward.
	for i := len(parts) - 2; i >= 0; i-- {
		dir := strings.TrimSpace(parts[i])
		if dir == "" {
			continue
		}
		if specialsFolderRE.MatchString(dir) {
			return canonicalEpisodeKind(dir)
		}
	}

	// TMDB / TheTVDB convention: season 0 is the specials season.
	if seasonNum == 0 {
		return "special"
	}
	return ""
}

// canonicalEpisodeKind normalises the various spellings the scanner
// might encounter (Specials / specials / SP / oad / OVAs / ovas)
// into the lowercase singular form stored in media_items.kind.
func canonicalEpisodeKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ova", "ovas":
		return "ova"
	case "ona", "onas":
		return "ona"
	case "special", "specials", "sp", "extra", "extras":
		return "special"
	case "pv", "mv":
		return "pv"
	case "oad":
		return "oad"
	case "movie":
		return "movie"
	}
	return ""
}

// extractShowTitle cleans a raw prefix into a show title. If the prefix is
// empty or unhelpful (e.g. just episode number), it falls back to the parent
// folder name (skipping "Season N" folders). Any TRaSH/Sonarr-style external
// id marker (`{tmdb-NNN}`, `{tvdb-NNN}`, `[tvdbid-NNN]`, `{imdb-tt...}`) is
// stripped from the folder name before cleaning, so the marker doesn't leak
// into the show title and torpedo title-based dedup matching.
func extractShowTitle(rawPrefix string, fullPath string) string {
	title := cleanShowTitle(StripFolderIDMarkers(rawPrefix))
	if title != "" {
		return title
	}

	// Fall back to folder structure: walk up parent directories looking for
	// the show name (skip Season folders).
	parts := strings.Split(filepath.ToSlash(fullPath), "/")
	// parts: [..., showFolder, seasonFolder, filename]
	for i := len(parts) - 2; i >= 0; i-- {
		dir := parts[i]
		if seasonFolderRE.MatchString(dir) {
			continue
		}
		cleaned := cleanShowTitle(StripFolderIDMarkers(dir))
		if cleaned != "" {
			return cleaned
		}
	}

	return ""
}

// fansubGroupRE matches a leading bracketed or parenthesised tag at
// the start of a string with optional surrounding whitespace —
// `[Group] `, `(Group) `, etc. The repeat-strip loop in
// stripLeadingFansubGroups handles consecutive prefixes like
// `[SubsPlease][Erai-raws] Show…` without backtracking.
var fansubGroupRE = regexp.MustCompile(`^\s*[\[(][^\])]*[\])]\s*`)

// stripLeadingFansubGroups removes one or more leading `[Group]` or
// `(Group)` tags from the input. Trailing bracket / paren clusters
// (quality / source markers) are intentionally left alone — those
// are handled by the downstream filename regex paths or end up in
// the right place anyway.
//
// Bare bracketed strings collapse to "" so cleanShowTitle returns
// empty and the caller falls through to the folder-fallback path.
func stripLeadingFansubGroups(s string) string {
	for {
		stripped := fansubGroupRE.ReplaceAllString(s, "")
		if stripped == s {
			return strings.TrimSpace(stripped)
		}
		s = stripped
	}
}

// cleanShowTitle normalises a raw string into a human-readable show title.
// Replaces dots and underscores with spaces, strips leading/trailing junk.
func cleanShowTitle(raw string) string {
	// Strip trailing separators and whitespace.
	raw = strings.TrimRight(raw, ".-_ ")
	raw = strings.TrimLeft(raw, ".-_ ")
	if raw == "" {
		return ""
	}

	// Strip leading fansub-group tags before normalisation. Anime
	// fansub releases conventionally prefix every filename with
	// `[Group]` (sometimes multiple consecutive groups for re-encodes
	// like `[SubsPlease][Erai-raws] Show…`), and some scene releases
	// use `(Group)`. Without stripping, "Solo Leveling" arrives in
	// the DB as "[jaaj] Solo Leveling" and the rest of the search /
	// dedup / display pipeline carries the group tag everywhere it
	// shouldn't appear.
	//
	// Only LEADING bracket runs are stripped — trailing brackets like
	// `Show [1080p].mkv` are quality / source markers handled by the
	// downstream regex paths and shouldn't be eaten here.
	raw = stripLeadingFansubGroups(raw)
	if raw == "" {
		return ""
	}

	// Replace dots and underscores with spaces.
	raw = strings.ReplaceAll(raw, ".", " ")
	raw = strings.ReplaceAll(raw, "_", " ")

	// Collapse multiple spaces and trim.
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}

	// Strip trailing " -" that can result from "Show Name - S01E03".
	title := strings.Join(fields, " ")
	title = strings.TrimRight(title, " -")
	title = strings.TrimSpace(title)

	return title
}
