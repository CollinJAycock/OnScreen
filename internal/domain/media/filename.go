package media

import (
	"path/filepath"
	"strings"
	"unicode"
)

// sanitizeFilenameStem turns a user-typed title into a filesystem-safe
// filename stem (no extension). Strips characters that would break on
// Windows or Unix (`<>:"/\|?*` and ASCII control codes), collapses
// runs of whitespace, trims trailing dots/spaces (forbidden on
// Windows), and rejects the Windows reserved device names (CON, PRN,
// AUX, NUL, COM1-9, LPT1-9). Returns "untitled" when the input is
// empty after cleanup so the caller never lands on a zero-byte name.
//
// Length-capped at 200 bytes to leave headroom inside the typical
// 255-byte path-component limit (Windows MAX_PATH segments + most
// Linux filesystems). Truncation respects UTF-8 boundaries.
func sanitizeFilenameStem(title string) string {
	if title == "" {
		return "untitled"
	}

	var b strings.Builder
	b.Grow(len(title))
	for _, r := range title {
		switch {
		case unicode.IsSpace(r):
			// Tabs / newlines / etc. become regular spaces; the
			// later FieldsFunc collapses runs.
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7F:
			// Other ASCII control chars — strip silently.
			continue
		case r == '<' || r == '>' || r == ':' || r == '"' ||
			r == '/' || r == '\\' || r == '|' || r == '?' || r == '*':
			// Replace forbidden chars with a space rather than
			// dropping outright — keeps word separation in titles
			// that overuse colons ("Brand: New").
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}

	// Collapse internal whitespace runs so a string of stripped chars
	// doesn't leave a triple-space gap.
	cleaned := strings.Join(strings.FieldsFunc(b.String(), unicode.IsSpace), " ")
	cleaned = strings.TrimRight(cleaned, ". ")
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "untitled"
	}

	// Reserved Windows device names — case-insensitive, with or
	// without an extension. Prefix with underscore so they become
	// valid filenames everywhere.
	upper := strings.ToUpper(cleaned)
	switch upper {
	case "CON", "PRN", "AUX", "NUL":
		cleaned = "_" + cleaned
	}
	if len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) {
		if upper[3] >= '1' && upper[3] <= '9' {
			cleaned = "_" + cleaned
		}
	}

	if len(cleaned) > 200 {
		// Trim back to a UTF-8 boundary at or before byte 200 so we
		// never bisect a multibyte rune.
		i := 200
		for i > 0 && cleaned[i]&0xC0 == 0x80 {
			i--
		}
		cleaned = strings.TrimRight(cleaned[:i], ". ")
	}

	return cleaned
}

// uniqueRenamePath picks a destination path under dir that won't
// collide with any existing file. Tries the bare name first; on
// collision, suffixes " (2)", " (3)", … until a free slot is found.
// Caller passes a probe func that returns true when a path is taken
// (typically wraps os.Stat) — keeps this helper free of OS imports
// for testability.
func uniqueRenamePath(dir, stem, ext string, taken func(path string) bool) string {
	target := filepath.Join(dir, stem+ext)
	if !taken(target) {
		return target
	}
	for i := 2; i < 1000; i++ {
		candidate := filepath.Join(dir, stem+" ("+itoa(i)+")"+ext)
		if !taken(candidate) {
			return candidate
		}
	}
	// Pathological — 1000 collisions in one folder. Append a UUID-ish
	// fallback so the rename still succeeds; caller will see a weird
	// suffix but the operation completes.
	return filepath.Join(dir, stem+" (collision)"+ext)
}

// itoa is a thin wrapper kept inline so this file stays
// std-imports-only. strconv would work too; using a hand-rolled tiny
// version avoids one more import for a 1-3 digit number.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
