// Package m3u reads and writes extended M3U playlists for playlist import
// and export.
//
// Everything here works on strings. A path or URL in an imported playlist is
// never opened, stat'ed or fetched: it is only a key to match against the
// paths the scanner already recorded, so a hostile playlist can't make the
// server touch its filesystem or the network.
package m3u

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxEntries caps how many entries one imported playlist may hold.
const MaxEntries = 10000

// ErrTooManyEntries is returned by Parse for a playlist over MaxEntries.
var ErrTooManyEntries = fmt.Errorf("m3u: more than %d entries", MaxEntries)

// ErrNotUTF8 is returned by Decode for bytes that aren't UTF-8 when Latin-1
// isn't allowed (an .m3u8 is UTF-8 by definition).
var ErrNotUTF8 = errors.New("m3u: playlist is not valid UTF-8")

// Entry is one media line of a playlist, with what its #EXTINF said.
type Entry struct {
	// Line is the 1-based line number of the path in the file.
	Line int
	// Path is the line as written, trimmed.
	Path string
	// Seconds is the #EXTINF duration; -1 when absent or unknown.
	Seconds int
	// Artist and Title come from the #EXTINF display text
	// ("Artist - Title"); Artist is empty when there was no " - ".
	Artist string
	Title  string
}

// Decode turns a playlist's bytes into text. A UTF-8 byte-order mark is
// dropped. Bytes that aren't valid UTF-8 are read as Latin-1 when
// latin1 is true (the historical encoding of .m3u), and refused otherwise.
func Decode(data []byte, latin1 bool) (string, error) {
	data = trimBOM(data)
	if utf8.Valid(data) {
		return string(data), nil
	}
	if !latin1 {
		return "", ErrNotUTF8
	}
	var b strings.Builder
	b.Grow(len(data) + len(data)/4)
	for _, c := range data {
		b.WriteRune(rune(c))
	}
	return b.String(), nil
}

func trimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

// Parse reads the entries of a (plain or extended) M3U playlist. Comment and
// directive lines other than #EXTINF are skipped; an #EXTINF applies to the
// next media line. Lines may end in \n, \r\n or \r.
func Parse(text string) ([]Entry, error) {
	var out []Entry
	pending := Entry{Seconds: -1}
	havePending := false
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	sc.Split(scanLines)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "#") {
			if rest, ok := cutPrefixFold(s, "#EXTINF:"); ok {
				pending = parseExtinf(rest)
				havePending = true
			}
			continue
		}
		e := Entry{Seconds: -1}
		if havePending {
			e = pending
		}
		e.Line = line
		e.Path = s
		if len(out) >= MaxEntries {
			return nil, ErrTooManyEntries
		}
		out = append(out, e)
		pending = Entry{Seconds: -1}
		havePending = false
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("m3u: read: %w", err)
	}
	return out, nil
}

// scanLines is bufio.ScanLines that also ends a line at a lone \r (classic
// Mac line endings, still written by some players).
func scanLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, c := range data {
		switch c {
		case '\n':
			return i + 1, data[:i], nil
		case '\r':
			if i+1 < len(data) {
				if data[i+1] == '\n' {
					return i + 2, data[:i], nil
				}
				return i + 1, data[:i], nil
			}
			if atEOF {
				return i + 1, data[:i], nil
			}
			// A \r at the end of the buffer may be half of \r\n: wait.
			return 0, nil, nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}

// parseExtinf reads "<seconds>[ attr="…"…],<display>".
func parseExtinf(rest string) Entry {
	e := Entry{Seconds: -1}
	head, display, ok := strings.Cut(rest, ",")
	if !ok {
		head, display = rest, ""
	}
	if f := strings.Fields(head); len(f) > 0 {
		if n, err := strconv.ParseFloat(f[0], 64); err == nil && n >= 0 {
			e.Seconds = int(n + 0.5)
		}
	}
	display = strings.TrimSpace(display)
	if artist, title, ok := strings.Cut(display, " - "); ok {
		e.Artist = strings.TrimSpace(artist)
		e.Title = strings.TrimSpace(title)
	} else {
		e.Title = display
	}
	return e
}

// Kind classifies an entry's path.
type Kind int

const (
	// KindRelative is a path relative to the playlist (or a library root).
	KindRelative Kind = iota
	// KindAbsolute is an absolute POSIX, Windows drive or UNC path.
	KindAbsolute
	// KindURL is a URL other than file:// (http, https, rtsp, …). Never
	// fetched; always reported unmatched.
	KindURL
)

// Classify reports what kind of path p is and returns it ready for matching:
// a file:// URL becomes its (unescaped) path.
func Classify(p string) (string, Kind) {
	if scheme, rest, ok := strings.Cut(p, "://"); ok && isScheme(scheme) {
		if !strings.EqualFold(scheme, "file") {
			return p, KindURL
		}
		// file:///mnt/a.flac, file://localhost/mnt/a.flac,
		// file:///C:/Music/a.flac. Anything with a host is a share.
		host, fp, _ := strings.Cut(rest, "/")
		if dec, err := url.PathUnescape(fp); err == nil {
			fp = dec
		}
		switch {
		case host != "" && !strings.EqualFold(host, "localhost"):
			p = `\\` + host + `\` + strings.ReplaceAll(fp, "/", `\`)
		case isDrivePath(fp):
			p = fp
		default:
			p = "/" + fp
		}
	}
	if IsAbs(p) {
		return p, KindAbsolute
	}
	return p, KindRelative
}

// isScheme reports whether s looks like a URL scheme (letters, digits, + - .,
// starting with a letter, at least two characters so "C://x" isn't one).
func isScheme(s string) bool {
	if len(s) < 2 {
		return false
	}
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

func isDrivePath(p string) bool {
	return len(p) >= 2 && p[1] == ':' &&
		(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}

// IsAbs reports whether p is absolute in POSIX or Windows terms.
func IsAbs(p string) bool {
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\\`) || isDrivePath(p)
}

// Components splits p on either slash, dropping empty and "." elements.
func Components(p string) []string {
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	out := parts[:0]
	for _, s := range parts {
		if s != "." {
			out = append(out, s)
		}
	}
	return out
}

// FoldedComponents is Components lower-cased, for the suffix match that has
// to bridge a Windows (case-insensitive) and a POSIX view of one library.
func FoldedComponents(p string) []string {
	c := Components(p)
	for i := range c {
		c[i] = strings.ToLower(c[i])
	}
	return c
}

// Join resolves rel against base in base's own separator style, applying
// ".." steps. The result is only a lookup key matched against recorded file
// paths under the caller's visibility; nothing opens it, so a rel that climbs
// out of base is harmless.
func Join(base, rel string) string {
	if base == "" {
		return ""
	}
	sep := "/"
	if strings.Contains(base, `\`) && !strings.Contains(base, "/") {
		sep = `\`
	}
	baseSlash := strings.ReplaceAll(base, `\`, "/")
	relSlash := strings.ReplaceAll(rel, `\`, "/")
	joined := path.Clean(baseSlash + "/" + relSlash)
	if strings.HasPrefix(baseSlash, "//") && !strings.HasPrefix(joined, "//") {
		joined = "/" + joined // path.Clean folds a UNC prefix's double slash
	}
	if sep == `\` {
		joined = strings.ReplaceAll(joined, "/", `\`)
	}
	return joined
}

// TrailingMatch counts how many trailing components a and b share (compared
// as given), up to max.
func TrailingMatch(a, b []string, max int) int {
	n := 0
	for n < max && n < len(a) && n < len(b) && a[len(a)-1-n] == b[len(b)-1-n] {
		n++
	}
	return n
}

// Writer emits an extended M3U playlist.
type Writer struct {
	w   io.Writer
	err error
}

// NewWriter writes the #EXTM3U header and returns a Writer.
func NewWriter(w io.Writer) *Writer {
	mw := &Writer{w: w}
	mw.printf("#EXTM3U\n")
	return mw
}

// Entry writes one #EXTINF line and its path. seconds < 0 writes -1
// (unknown); an empty artist writes just the title. Line breaks in any field
// are dropped so a title can't inject extra entries.
func (mw *Writer) Entry(seconds int, artist, title, p string) {
	p = oneLine(p)
	if p == "" {
		return
	}
	if seconds < 0 {
		seconds = -1
	}
	display := oneLine(title)
	if a := oneLine(artist); a != "" {
		display = a + " - " + display
	}
	mw.printf("#EXTINF:%d,%s\n%s\n", seconds, display, p)
}

// Err reports the first write error.
func (mw *Writer) Err() error { return mw.err }

func (mw *Writer) printf(format string, args ...any) {
	if mw.err != nil {
		return
	}
	_, mw.err = fmt.Fprintf(mw.w, format, args...)
}

func oneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s))
}
