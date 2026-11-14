package ffsafe

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const dashManifest = `<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" profiles="urn:mpeg:dash:profile:isoff-on-demand:2011" type="static" mediaPresentationDuration="PT1S"><Period><AdaptationSet mimeType="video/mp4"><Representation id="1" bandwidth="1000"><BaseURL>/media/other/secret.mkv</BaseURL></Representation></AdaptationSet></Period></MPD>`

// id3v2 is an ID3v2.3 tag with n bytes of (NUL) padding — what ffmpeg's probe
// skips before handing the buffer to the text probes.
func id3v2(n int) []byte {
	return append([]byte{'I', 'D', '3', 3, 0, 0, byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}, make([]byte, n)...)
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLooksLikeReferenceFile(t *testing.T) {
	ebml := []byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81, 0x01, 0x00} // EBML header: a NUL within 10 bytes
	mp4 := []byte{0, 0, 0, 0x20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}
	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		// The 2026-11 bypass: <MPD past the old 512-byte window.
		{"dash behind a 700-byte comment", []byte("<!--" + strings.Repeat("x", 700) + "-->\n" + dashManifest), true},
		{"dash with xml declaration", []byte(`<?xml version="1.0"?>` + "\n" + dashManifest), true},
		{"dash after an id3 tag", append(id3v2(64), []byte(dashManifest)...), true},
		{"dash 1.5 MiB into text", []byte(strings.Repeat(" ", 1536<<10) + dashManifest), true},
		{"hls", []byte("#EXTM3U\n#EXTINF:10,\n/media/other/secret.ts\n"), true},
		{"hls after bom and blank lines", []byte("\xef\xbb\xbf\n\n#EXTM3U\n"), true},
		{"hls after an id3 tag", append(id3v2(32), []byte("#EXTM3U\n")...), true},
		{"ffconcat", []byte("ffconcat version 1.0\nfile '/media/other/secret.mkv'\n"), true},
		{"sdp", []byte("v=0\no=- 0 0 IN IP4 127.0.0.1\n"), true},
		{"sdp connection line", []byte("s=x\nc=IN IP4 10.0.0.1\nm=video 5004 RTP/AVP 96\n"), true},
		{"imf", []byte(`<?xml version="1.0"?><CompositionPlaylist xmlns="http://www.smpte-ra.org/schemas/2067-3/2016"><ContentTitle>x</ContentTitle></CompositionPlaylist>`), true},

		// What ffmpeg's text probes can't see isn't refused: they stop at the
		// first NUL, which real media has within a few bytes.
		{"matroska with mpd text later", append(ebml, []byte(dashManifest)...), false},
		{"mp4 with mpd text later", append(mp4, []byte(dashManifest)...), false},
		{"id3 then mpeg audio", append(id3v2(16), 0xFF, 0xFB, 0x90, 0x64, 0x00, '<', 'M', 'P', 'D'), false},
		{"dash past the probe window", []byte(strings.Repeat(" ", referenceSniffWindow+1024) + dashManifest), false},
		{"subtitle text", []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), false},
		{"empty", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := LooksLikeReferenceFile(writeFile(t, "movie.mkv", tc.data)); got != tc.want {
				t.Errorf("LooksLikeReferenceFile = %v, want %v", got, tc.want)
			}
		})
	}
	if LooksLikeReferenceFile(filepath.Join(t.TempDir(), "missing.mkv")) {
		t.Error("a missing file is not a reference container")
	}
	if LooksLikeReferenceFile("http://10.0.0.5/media/stream/x?token=y") {
		t.Error("http sources are left to the post-probe format check")
	}
}

// Every caller of Whitelist / InputProtocolArgs fails closed on a manifest,
// whether or not it ran a playback gate first.
func TestWhitelist_RefusesLocalReferenceFile(t *testing.T) {
	manifest := writeFile(t, "movie.mkv", []byte("<!--"+strings.Repeat("x", 700)+"-->"+dashManifest))
	media := writeFile(t, "real.mkv", []byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x86, 0x81, 0x01, 0x00})
	if got := Whitelist(manifest); got != refusedProtocols {
		t.Errorf("Whitelist(manifest) = %q, want %q", got, refusedProtocols)
	}
	if got := InputProtocolArgs(manifest); got[1] != refusedProtocols {
		t.Errorf("InputProtocolArgs(manifest) = %v", got)
	}
	if got := Whitelist(media); got != localProtocols {
		t.Errorf("Whitelist(media) = %q, want %q", got, localProtocols)
	}
}

func TestStdinInputArgs(t *testing.T) {
	for in, want := range map[string][]string{
		"":    {"-format_whitelist", liveStreamDemuxers, "-protocol_whitelist", "pipe"},
		"flv": {"-f", "flv", "-protocol_whitelist", "pipe"},
	} {
		if got := StdinInputArgs(in); !slices.Equal(got, want) {
			t.Errorf("StdinInputArgs(%q) = %v, want %v", in, got, want)
		}
	}
}

// Against a real ffprobe: the DASH-behind-a-comment file is read as dash with
// the file-family allowlist, and the fail-closed protocol list stops it.
func TestFFprobeHonoursRestrictions(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available")
	}
	p := writeFile(t, "movie.mkv", []byte("<!--"+strings.Repeat("x", 700)+"-->\n"+dashManifest))
	probe := func(args ...string) (string, error) {
		out, err := exec.Command("ffprobe", append(append([]string{"-v", "error"}, args...),
			"-show_entries", "format=format_name", "-of", "csv=p=0", p)...).CombinedOutput()
		return string(out), err
	}
	if out, _ := probe("-protocol_whitelist", localProtocols); !strings.Contains(out, "dash") {
		t.Skipf("this ffprobe doesn't select dash for the crafted file (%q); nothing to compare", out)
	}
	if out, err := probe(InputProtocolArgs(p)...); err == nil || !strings.Contains(out, "not on whitelist") {
		t.Errorf("fail-closed protocol list: err=%v out=%q, want a whitelist refusal", err, out)
	}
}

// Against a real ffmpeg: with no format hint, a piped manifest is refused
// while the radio and progressive streams autodetection plays today still
// open (forcing -f mpegts, the first version of this fix, refused MP3 and
// Matroska on a pipe).
func TestFFmpegStdinInputArgs(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	gen := func(name string, args ...string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		cmd := exec.Command("ffmpeg", append(append([]string{"-v", "error", "-y"}, args...), p)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("can't generate %s with this ffmpeg: %v %s", name, err, out)
		}
		return p
	}
	sine := []string{"-f", "lavfi", "-i", "sine=frequency=440:duration=1"}
	streams := map[string]string{
		"mpegts": gen("in.ts", append(append([]string{}, sine...), "-c:a", "aac", "-f", "mpegts")...),
		"mp3":    gen("in.mp3", append(append([]string{}, sine...), "-c:a", "libmp3lame")...),
		"mkv":    gen("in.mkv", append(append([]string{}, sine...), "-c:a", "aac")...),
	}
	open := func(src string) (string, error) {
		f, err := os.Open(src)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		args := append([]string{"-v", "error"}, StdinInputArgs("")...)
		cmd := exec.Command("ffmpeg", append(args, "-i", "pipe:0", "-f", "null", "-")...)
		cmd.Stdin = f
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	for name, p := range streams {
		if out, err := open(p); err != nil {
			t.Errorf("piped %s: %v %s", name, err, out)
		}
	}
	manifest := writeFile(t, "piped.mpd", []byte(dashManifest))
	if out, err := open(manifest); err == nil || !strings.Contains(out, "not on whitelist") {
		t.Errorf("piped manifest: err=%v out=%q, want a whitelist refusal", err, out)
	}
}
