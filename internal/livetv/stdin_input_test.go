package livetv

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/ffsafe"
)

type hintedStream struct {
	io.ReadCloser
	format string
}

func (h hintedStream) FFmpegInputFormat() string { return h.format }

// A tuner / IPTV stream piped to ffmpeg used to be autodetected under the
// file-family allowlist, so a piped DASH/HLS manifest could open local media.
// Pipe is now the only protocol, and the demuxer is either the one the
// upstream names or autodetection limited to live-stream demuxers (no
// manifest demuxer), so radio (MP3/AAC) and progressive streams still play.
func TestStdinInputArgs(t *testing.T) {
	plain := io.NopCloser(strings.NewReader(""))
	for _, tc := range []struct {
		name     string
		upstream Stream
		want     []string
	}{
		{"tuner (no hint): autodetect, live demuxers only", plain, ffsafe.StdinInputArgs("")},
		{"rtmp reader: flv", hintedStream{plain, "flv"}, []string{"-f", "flv", "-protocol_whitelist", "pipe"}},
		{"empty hint: same as none", hintedStream{plain, ""}, ffsafe.StdinInputArgs("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stdinInputArgs(tc.upstream); !slices.Equal(got, tc.want) {
				t.Errorf("stdinInputArgs = %v, want %v", got, tc.want)
			}
		})
	}
}
