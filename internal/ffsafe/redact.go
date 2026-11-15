package ffsafe

import (
	"io"
	"strings"
	"sync"
)

// Redacted replaces the query string of every URL RedactURLs masks.
const Redacted = "REDACTED"

// RedactURLs masks the query string of every http(s) URL in s, keeping the
// scheme, host and path for debugging.
//
// ffmpeg names its input in its own messages ("<url>: Server returned 403
// Forbidden", "Error opening input file <url>."), and an ffmpeg input URL's
// query is a credential: a storage-less worker reads the primary's
// /media/stream URL with a 24-hour stream token, and an object-storage source
// is a presigned URL whose signature IS the query. The query runs to the next
// space, quote or angle bracket; a colon that ends it is ffmpeg's separator
// and is kept.
func RedactURLs(s string) string {
	if !strings.Contains(s, "http://") && !strings.Contains(s, "https://") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for {
		start := nextURL(s)
		if start < 0 {
			b.WriteString(s)
			return b.String()
		}
		end := start + strings.IndexAny(s[start:]+" ", " \t\r\n'\"<>`")
		url := s[start:end]
		b.WriteString(s[:start])
		if q := strings.IndexByte(url, '?'); q >= 0 {
			sep := ""
			if strings.HasSuffix(url, ":") {
				sep = ":"
			}
			b.WriteString(url[:q+1])
			b.WriteString(Redacted)
			b.WriteString(sep)
		} else {
			b.WriteString(url)
		}
		s = s[end:]
	}
}

// nextURL returns the index of the first "http://" or "https://" in s, or -1.
func nextURL(s string) int {
	i := strings.Index(s, "http://")
	if j := strings.Index(s, "https://"); j >= 0 && (i < 0 || j < i) {
		i = j
	}
	return i
}

// maxPendingLine bounds the partial line a RedactingWriter holds back. ffmpeg
// lines are short; one this long is written out as it is so far.
const maxPendingLine = 64 << 10

// RedactingWriter forwards whole lines to an underlying writer with
// RedactURLs applied. Set it as an ffmpeg command's Stderr in place of
// os.Stderr, and call Flush after the process has exited for a last line
// that had no newline. A line ends at '\n' or '\r' (ffmpeg's stats line).
type RedactingWriter struct {
	mu      sync.Mutex
	w       io.Writer
	pending []byte
}

// NewRedactingWriter returns a RedactingWriter that writes to w.
func NewRedactingWriter(w io.Writer) *RedactingWriter {
	return &RedactingWriter{w: w}
}

// Write buffers p and writes out every line it completes. It reports len(p)
// unless the underlying writer fails.
func (r *RedactingWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(r.pending, p...)
	cut := -1
	for i := len(r.pending) - 1; i >= 0; i-- {
		if c := r.pending[i]; c == '\n' || c == '\r' {
			cut = i
			break
		}
	}
	if cut < 0 && len(r.pending) < maxPendingLine {
		return len(p), nil
	}
	n := cut + 1
	if cut < 0 {
		n = len(r.pending)
	}
	if err := r.emit(r.pending[:n]); err != nil {
		return 0, err
	}
	r.pending = append(r.pending[:0], r.pending[n:]...)
	return len(p), nil
}

// Flush writes out a held-back partial line.
func (r *RedactingWriter) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) == 0 {
		return nil
	}
	err := r.emit(r.pending)
	r.pending = r.pending[:0]
	return err
}

func (r *RedactingWriter) emit(b []byte) error {
	_, err := io.WriteString(r.w, RedactURLs(string(b)))
	return err
}
