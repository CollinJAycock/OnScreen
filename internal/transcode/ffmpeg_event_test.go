package transcode

import (
	"strings"
	"testing"
)

// A re-encode's playlist is EVENT too, like a remux's: without a playlist
// type it reads as live, and a player's live-start rule (a Chromecast's
// Default Media Receiver, or hls.js once a fast GPU encode has written
// several segments) starts near the edge instead of at the position the
// session was started for.
func TestBuildHLS_Transcode_EventPlaylist(t *testing.T) {
	for _, enc := range []Encoder{"libx264", "h264_nvenc", "hevc_nvenc"} {
		args := BuildHLS(BuildArgs{
			InputPath:     "/media/movie.mkv",
			Encoder:       enc,
			AudioCodec:    "aac",
			SessionDir:    "/tmp/sessions/x",
			SegmentPrefix: "seg",
			Width:         1920,
			Height:        1080,
			BitrateKbps:   8000,
			StartOffset:   1800,
		})
		if argStr := strings.Join(args, " "); !strings.Contains(argStr, "-hls_playlist_type event") {
			t.Errorf("%s re-encode should use an EVENT playlist: %s", enc, argStr)
		}
	}
}
