package podcast

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode"
)

// maxNameBytes bounds a folder or file name (before the extension), well
// under the 255-byte limit of common filesystems.
const maxNameBytes = 150

// SafeName turns a feed or episode title into a file or folder name that's
// valid on Windows, macOS and Linux: path separators and reserved characters
// become spaces or dashes, control characters go, and trailing dots and
// spaces (which Windows drops) are trimmed. Empty results fall back to
// fallback.
func SafeName(title, fallback string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r == '/' || r == '\\' || r == '|':
			b.WriteString(" - ")
		case r == ':':
			b.WriteString(" -")
		case r == '<' || r == '>' || r == '"' || r == '?' || r == '*':
			// dropped
		case unicode.IsControl(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Join(strings.Fields(b.String()), " ")
	name = strings.ReplaceAll(name, "- -", "-")
	name = clip(name, maxNameBytes)
	name = strings.TrimRight(name, ". ")
	name = strings.TrimLeft(name, ". ")
	if name == "" || reservedWindowsName(name) {
		return fallback
	}
	return name
}

// reservedWindowsName reports device names Windows refuses as a file name
// (CON, NUL, COM1, …), with or without an extension.
func reservedWindowsName(name string) bool {
	base, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) &&
		base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}

// EpisodeFileName is the file an episode downloads to: its publish date (so
// a folder lists in release order) and title, plus ext.
//
//	2026-10-12 The Episode Title.mp3
func EpisodeFileName(title string, published *time.Time, ext string) string {
	name := SafeName(title, "Episode")
	if published != nil {
		name = published.UTC().Format("2006-01-02") + " " + name
	}
	return name + ext
}

// Disambiguate appends a short stable tag derived from key to name, before
// its extension: "Episode.mp3" → "Episode (3fa2c1d0).mp3". Used when two
// episodes or feeds would otherwise share a name.
func Disambiguate(name, ext, key string) string {
	sum := sha256.Sum256([]byte(key))
	return strings.TrimSuffix(name, ext) + " (" + hex.EncodeToString(sum[:4]) + ")" + ext
}
