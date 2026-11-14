package livetv

import (
	"io"
	"slices"
	"strings"
	"testing"
)

type hintedStream struct {
	io.ReadCloser
	format string
}

func (h hintedStream) FFmpegInputFormat() string { return h.format }

// A tuner / IPTV stream piped to ffmpeg used to be autodetected under the
// file-family allowlist, so a piped DASH/HLS manifest could open local media.
// The demuxer is now fixed and pipe is the only protocol.
func TestStdinInputArgs(t *testing.T) {
	plain := io.NopCloser(strings.NewReader(""))
	for _, tc := range []struct {
		name     string
		upstream Stream
		want     []string
	}{
		{"tuner (no hint): mpegts", plain, []string{"-f", "mpegts", "-protocol_whitelist", "pipe"}},
		{"rtmp reader: flv", hintedStream{plain, "flv"}, []string{"-f", "flv", "-protocol_whitelist", "pipe"}},
		{"empty hint: mpegts", hintedStream{plain, ""}, []string{"-f", "mpegts", "-protocol_whitelist", "pipe"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stdinInputArgs(tc.upstream); !slices.Equal(got, tc.want) {
				t.Errorf("stdinInputArgs = %v, want %v", got, tc.want)
			}
		})
	}
}
