package livetv

import (
	"bytes"
	"testing"
)

// FuzzParseM3U exercises parseM3U against adversarial input. The contract
// is "never panic, never hang" — admin-supplied M3U URLs feed straight
// into this parser, and a crash here would let a hostile playlist take
// down the live-TV worker.
//
// Run as: go test -fuzz=FuzzParseM3U -fuzztime=30s ./internal/livetv/
func FuzzParseM3U(f *testing.F) {
	// Seeds: a representative valid playlist and several known-tricky
	// inputs (long lines, no header, bare CRLF, malformed attrs).
	f.Add([]byte(`#EXTM3U
#EXTINF:-1 tvg-id="abc" tvg-chno="5.1" tvg-logo="http://x" tvg-name="Channel",WCBS-DT
http://stream.url/path
`))
	f.Add([]byte("")) // empty
	f.Add([]byte("#EXTM3U\n"))
	f.Add([]byte("#EXTINF:\n")) // EXTINF with no body
	f.Add([]byte("http://orphan.url\n")) // stream without EXTINF
	f.Add([]byte("#EXTINF:-1,Channel\nhttp://x\n#EXTINF:-1,\nhttp://y\n")) // empty name
	f.Add([]byte("#EXTINF:-1 tvg-id=\"\\\"\\\"\\\"\" ,\nhttp://x\n"))     // mangled quotes
	f.Add(bytes.Repeat([]byte("X"), 200_000))                              // a single huge line

	f.Fuzz(func(t *testing.T, data []byte) {
		// We don't care about the result — only that the parser returns
		// without panicking. A bufio.Scanner buffer overflow shows up
		// here as a panic; that's the regression we want to lock down.
		_, _ = parseM3U(bytes.NewReader(data))
	})
}
