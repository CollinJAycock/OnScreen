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

// extractShowTitle cleans a raw prefix into a show title. If the prefix is
// empty or unhelpful (e.g. just episode number), it falls back to the parent
// folder name (skipping "Season N" folders).
func extractShowTitle(rawPrefix string, fullPath string) string {
	title := cleanShowTitle(rawPrefix)
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
		cleaned := cleanShowTitle(dir)
		if cleaned != "" {
			return cleaned
		}
	}

	return ""
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
