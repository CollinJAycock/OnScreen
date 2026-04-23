package transcode

import (
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// helpers
func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func baseFile() media.File {
	return media.File{
		ID:          uuid.New(),
		MediaItemID: uuid.New(),
		FilePath:    "/media/movie.mkv",
		VideoCodec:  strPtr("h264"),
		AudioCodec:  strPtr("aac"),
		Container:   strPtr("mkv"),
		ResolutionW: intPtr(1920),
		ResolutionH: intPtr(1080),
	}
}

var defaultServerCaps = ServerCaps{
	MaxBitrateKbps: 40000,
	MaxWidth:       3840,
	MaxHeight:      2160,
}

func TestDecide_DirectPlay(t *testing.T) {
	file := baseFile()
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac:ac3,protocols=mkv:mp4")
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectPlay {
		t.Errorf("want DirectPlay, got %s", got)
	}
}

func TestDecide_DirectStream_ContainerMismatch(t *testing.T) {
	file := baseFile()
	// Client supports h264+aac but not mkv — should DirectStream (remux).
	caps := ParseCapabilities("videoDecoder=h264,audioDecoder=aac,protocols=ts:mp4")
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectStream {
		t.Errorf("want DirectStream, got %s", got)
	}
}

func TestDecide_Transcode_UnsupportedVideo(t *testing.T) {
	file := baseFile()
	*file.VideoCodec = "hevc"
	// Client only supports h264 — must transcode.
	caps := ParseCapabilities("videoDecoder=h264,audioDecoder=aac,protocols=mkv:mp4:ts")
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionTranscode {
		t.Errorf("want Transcode, got %s", got)
	}
}

func TestDecide_Transcode_HDR10_ClientNoHDR(t *testing.T) {
	file := baseFile()
	file.HDRType = strPtr("hdr10")
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mkv:mp4")
	caps.SupportsHDR = false
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionTranscode {
		t.Errorf("want Transcode for HDR without client support, got %s", got)
	}
}

func TestDecide_DirectPlay_HDR10_ClientSupportsHDR(t *testing.T) {
	file := baseFile()
	file.HDRType = strPtr("hdr10")
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mkv:mp4")
	caps.SupportsHDR = true
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectPlay {
		t.Errorf("want DirectPlay for HDR with client HDR support, got %s", got)
	}
}

func TestDecide_Transcode_DolbyVision_ClientNoDV(t *testing.T) {
	file := baseFile()
	file.HDRType = strPtr("dolby_vision")
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mkv:mp4")
	caps.SupportsHDR = true // HDR10 support doesn't cover DV
	caps.SupportsDV = false
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionTranscode {
		t.Errorf("want Transcode for DV without DV support, got %s", got)
	}
}

func TestDecide_DirectPlay_DolbyVision_ClientSupportsDV(t *testing.T) {
	file := baseFile()
	file.HDRType = strPtr("dolby_vision")
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mkv:mp4")
	caps.SupportsDV = true
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectPlay {
		t.Errorf("want DirectPlay for DV with DV support, got %s", got)
	}
}

func TestDecide_Transcode_ResolutionExceedsClient(t *testing.T) {
	file := baseFile()
	*file.ResolutionW = 3840
	*file.ResolutionH = 2160
	// Client caps only 1080p.
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mkv:mp4")
	caps.MaxWidth = 1920
	caps.MaxHeight = 1080
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionTranscode {
		t.Errorf("want Transcode when 4K exceeds 1080p client, got %s", got)
	}
}

func TestDecide_AudioOnly_DirectPlay(t *testing.T) {
	// Audio-only file with no video stream — should DirectPlay when client
	// supports the audio codec + container.
	file := media.File{
		ID:          uuid.New(),
		MediaItemID: uuid.New(),
		FilePath:    "/media/song.flac",
		AudioCodec:  strPtr("flac"),
		Container:   strPtr("flac"),
	}
	caps := ParseCapabilities("audioDecoder=flac:mp3,protocols=flac:mp3")
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectPlay {
		t.Errorf("want DirectPlay for audio-only flac, got %s", got)
	}
}

func TestDecide_AudioOnly_DirectStream_ContainerMismatch(t *testing.T) {
	// FLAC audio with unsupported container — DirectStream (remux).
	file := media.File{
		ID:          uuid.New(),
		MediaItemID: uuid.New(),
		FilePath:    "/media/song.dsf",
		AudioCodec:  strPtr("flac"),
		Container:   strPtr("dsf"),
	}
	caps := ParseCapabilities("audioDecoder=flac:mp3,protocols=flac:mp3")
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectStream {
		t.Errorf("want DirectStream for FLAC in unsupported container, got %s", got)
	}
}

func TestDecide_NilCodecs(t *testing.T) {
	// File with no codec info — should not panic.
	file := media.File{
		ID:          uuid.New(),
		MediaItemID: uuid.New(),
		FilePath:    "/media/unknown.bin",
	}
	caps := ParseCapabilities("videoDecoder=h264,audioDecoder=aac,protocols=mkv")
	got := Decide(file, caps, defaultServerCaps)
	// With no codecs, clientSupportsVideo is false → Transcode.
	if got != DecisionTranscode {
		t.Errorf("want Transcode for nil codecs, got %s", got)
	}
}

func TestDecision_String(t *testing.T) {
	cases := []struct {
		d    Decision
		want string
	}{
		{DecisionDirectPlay, "directPlay"},
		{DecisionDirectStream, "directStream"},
		{DecisionTranscode, "transcode"},
	}
	for _, tc := range cases {
		if got := tc.d.String(); got != tc.want {
			t.Errorf("Decision(%d).String(): want %q, got %q", tc.d, tc.want, got)
		}
	}
}

func TestCanonicalCodecs(t *testing.T) {
	videoTests := []struct{ in, want string }{
		{"h264", "h264"},
		{"avc", "h264"},
		{"avc1", "h264"},
		{"hevc", "h265"},
		{"hvc1", "h265"},
		{"H265", "h265"},
		{"av1", "av1"},
		{"vp9", "vp9"},
		{"mpeg4", "mpeg4"},
		{"unknown_codec", "unknown_codec"},
	}
	for _, tc := range videoTests {
		if got := canonicalVideoCodec(tc.in); got != tc.want {
			t.Errorf("canonicalVideoCodec(%q): want %q, got %q", tc.in, tc.want, got)
		}
	}

	audioTests := []struct{ in, want string }{
		{"aac", "aac"},
		{"ac3", "ac3"},
		{"ac-3", "ac3"},
		{"eac3", "eac3"},
		{"e-ac-3", "eac3"},
		{"eac-3", "eac3"},
		{"dts-hd", "dts"},
		{"dtshd", "dts"},
		{"dts", "dts"},
		{"mp2", "mp3"},
		{"mp3", "mp3"},
		{"truehd", "truehd"},
		{"flac", "flac"},
		{"vorbis", "vorbis"},
		{"opus", "opus"},
		{"unknown_audio", "unknown_audio"},
	}
	for _, tc := range audioTests {
		if got := canonicalAudioCodec(tc.in); got != tc.want {
			t.Errorf("canonicalAudioCodec(%q): want %q, got %q", tc.in, tc.want, got)
		}
	}

	containerTests := []struct{ in, want string }{
		{"matroska", "mkv"},
		{"mkv", "mkv"},
		{"mp4", "mp4"},
		{"m4v", "mp4"},
		{"isom", "mp4"},
		{"mov", "mp4"},
		{"avi", "avi"},
		{"ts", "ts"},
		{"mpeg-ts", "ts"},
	}
	for _, tc := range containerTests {
		if got := canonicalContainer(tc.in); got != tc.want {
			t.Errorf("canonicalContainer(%q): want %q, got %q", tc.in, tc.want, got)
		}
	}
}

func TestClientSupportsHDR(t *testing.T) {
	capsHDR := ClientCapabilities{SupportsHDR: true}
	capsDV := ClientCapabilities{SupportsDV: true}
	capsNone := ClientCapabilities{}

	cases := []struct {
		caps    ClientCapabilities
		hdrType string
		want    bool
	}{
		{capsHDR, "hdr10", true},
		{capsHDR, "hdr10plus", true},
		{capsHDR, "hlg", true},
		{capsNone, "hdr10", false},
		{capsDV, "dolby_vision", true},
		{capsHDR, "dolby_vision", false}, // HDR10 support ≠ DV support
		{capsNone, "dolby_vision", false},
		{capsNone, "unknown_hdr", false},
	}
	for _, tc := range cases {
		if got := clientSupportsHDR(tc.caps, tc.hdrType); got != tc.want {
			t.Errorf("clientSupportsHDR(%q): want %v, got %v", tc.hdrType, tc.want, got)
		}
	}
}

// ── HEVC-capable client decision tests ─────────────────────────────────────

func TestDecide_DirectPlay_HEVC_MP4_SDR(t *testing.T) {
	file := baseFile()
	*file.VideoCodec = "hevc"
	*file.Container = "mp4"
	*file.AudioCodec = "aac"
	// Client supports HEVC + container.
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mp4:ts")
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectPlay {
		t.Errorf("want DirectPlay for HEVC SDR MP4 on HEVC client, got %s", got)
	}
}

func TestDecide_DirectStream_HEVC_MKV_SDR(t *testing.T) {
	file := baseFile()
	*file.VideoCodec = "hevc"
	*file.Container = "mkv"
	*file.AudioCodec = "aac"
	// Client supports h265 + aac but not MKV container → remux.
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mp4:ts")
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectStream {
		t.Errorf("want DirectStream for HEVC SDR MKV on HEVC client, got %s", got)
	}
}

func TestDecide_Transcode_HEVC_MKV_DTS(t *testing.T) {
	// Real-world case: Alien (1979) — HEVC MKV with DTS audio.
	// Client supports HEVC but not DTS → must transcode audio.
	file := baseFile()
	*file.VideoCodec = "hevc"
	*file.Container = "mkv"
	*file.AudioCodec = "dts"
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac:mp3:opus:flac,protocols=mp4:ts")
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionTranscode {
		t.Errorf("want Transcode for HEVC MKV + DTS (audio unsupported), got %s", got)
	}
}

func TestDecide_Transcode_HEVC_HDR10_ClientNoHDR(t *testing.T) {
	// Real-world case: GoodFellas on SDR display — HEVC HDR10, needs tonemapping.
	file := baseFile()
	*file.VideoCodec = "hevc"
	*file.Container = "mkv"
	*file.AudioCodec = "dts"
	*file.ResolutionW = 3840
	*file.ResolutionH = 2160
	file.HDRType = strPtr("hdr10")
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac:mp3:opus:flac,protocols=mp4:ts")
	caps.SupportsHDR = false
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionTranscode {
		t.Errorf("want Transcode for HEVC HDR10 on SDR display, got %s", got)
	}
}

func TestDecide_DirectStream_HEVC_HDR10_ClientHDR(t *testing.T) {
	// Real-world case: GoodFellas on HDR display — HEVC HDR10, client supports HEVC+HDR.
	// MKV container not supported → DirectStream (remux).
	file := baseFile()
	*file.VideoCodec = "hevc"
	*file.Container = "mkv"
	*file.AudioCodec = "aac"
	*file.ResolutionW = 3840
	*file.ResolutionH = 2160
	file.HDRType = strPtr("hdr10")
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mp4:ts,maxWidth=3840,maxHeight=2160")
	caps.SupportsHDR = true
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectStream {
		t.Errorf("want DirectStream for HEVC HDR10 on HDR display, got %s", got)
	}
}

func TestDecide_DirectPlay_HEVC_HDR10_MP4_ClientHDR(t *testing.T) {
	// HEVC HDR10 in MP4 on HDR display with HEVC support → DirectPlay.
	file := baseFile()
	*file.VideoCodec = "hevc"
	*file.Container = "mp4"
	*file.AudioCodec = "aac"
	file.HDRType = strPtr("hdr10")
	caps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac,protocols=mp4:ts")
	caps.SupportsHDR = true
	got := Decide(file, caps, defaultServerCaps)
	if got != DecisionDirectPlay {
		t.Errorf("want DirectPlay for HEVC HDR10 MP4 on HDR display, got %s", got)
	}
}

// TestDecide_CodecMatrix covers every codec/container combination present in the
// actual media library (derived from SELECT DISTINCT container, video_codec, audio_codec
// FROM media_files WHERE status='active'). Each case asserts whether the server
// should DirectPlay, DirectStream, or Transcode when the web player declares the
// capabilities of a typical modern browser (h264 video, aac/mp3 audio, mp4/ts containers).
func TestDecide_CodecMatrix(t *testing.T) {
	// Capabilities of the OnScreen web player after our codec fixes:
	// - Video: h264 only for remux; AV1/VP9 require full transcode
	// - Audio: aac, mp3, opus, flac (no AC-3, E-AC-3, DTS, TrueHD)
	// - Containers: mp4, ts (via HLS.js)
	webPlayerCaps := ParseCapabilities("videoDecoder=h264,audioDecoder=aac:mp3:opus:flac,protocols=mp4:ts")

	cases := []struct {
		name      string
		container string
		video     string
		audio     string
		want      Decision
		reason    string
	}{
		// ── AVI files (Good Eats older seasons) ────────────────────────────────
		{"avi/msmpeg4v2/mp3", "avi", "msmpeg4v2", "mp3", DecisionTranscode,
			"msmpeg4v2 not browser-playable, AVI not supported"},
		{"avi/msmpeg4v3/mp3", "avi", "msmpeg4v3", "mp3", DecisionTranscode,
			"msmpeg4v3 not browser-playable"},
		{"avi/mpeg4/mp3", "avi", "mpeg4", "mp3", DecisionTranscode,
			"mpeg4-part2 (Xvid/DivX) not browser-playable"},

		// ── MOV/MP4 files ──────────────────────────────────────────────────────
		{"mov/h264/aac", "mp4", "h264", "aac", DecisionDirectPlay,
			"faststart MP4 with h264/aac is direct play"},
		{"mov/hevc/aac", "mp4", "hevc", "aac", DecisionTranscode,
			"web player doesn't declare HEVC support → transcode"},
		{"mov/hevc/eac3", "mp4", "hevc", "eac3", DecisionTranscode,
			"HEVC video + E-AC-3 audio, both unsupported"},

		// ── MKV files ──────────────────────────────────────────────────────────
		{"mkv/h264/aac", "mkv", "h264", "aac", DecisionDirectStream,
			"h264/aac supported but MKV container not → remux to MPEG-TS"},
		{"mkv/h264/ac3", "mkv", "h264", "ac3", DecisionTranscode,
			"h264 OK but AC-3 audio not browser-supported → transcode audio"},
		{"mkv/h264/eac3", "mkv", "h264", "eac3", DecisionTranscode,
			"E-AC-3 (Dolby Digital Plus) not browser-supported"},
		{"mkv/h264/dts", "mkv", "h264", "dts", DecisionTranscode,
			"DTS not browser-supported"},
		{"mkv/h264/truehd", "mkv", "h264", "truehd", DecisionTranscode,
			"TrueHD not browser-supported"},
		{"mkv/h264/opus", "mkv", "h264", "opus", DecisionDirectStream,
			"h264/opus both supported, remux MKV→MPEG-TS"},
		{"mkv/hevc/eac3", "mkv", "hevc", "eac3", DecisionTranscode,
			"HEVC + E-AC-3, both require transcode"},
		{"mkv/hevc/ac3", "mkv", "hevc", "ac3", DecisionTranscode,
			"HEVC + AC-3, both require transcode"},
		{"mkv/hevc/aac", "mkv", "hevc", "aac", DecisionTranscode,
			"HEVC not declared by web player → transcode video"},
		{"mkv/hevc/dts", "mkv", "hevc", "dts", DecisionTranscode,
			"HEVC + DTS, both require transcode"},
		{"mkv/hevc/truehd", "mkv", "hevc", "truehd", DecisionTranscode,
			"HEVC + TrueHD, both require transcode"},
		{"mkv/av1/opus", "mkv", "av1", "opus", DecisionTranscode,
			"AV1 not declared by web player (MPEG-TS can't carry AV1) → transcode"},
		{"mkv/av1/dts", "mkv", "av1", "dts", DecisionTranscode,
			"AV1 + DTS, both require transcode"},
		{"mkv/av1/aac", "mkv", "av1", "aac", DecisionTranscode,
			"AV1 not in web player caps → transcode"},
		{"mkv/h264/flac", "mkv", "h264", "flac", DecisionDirectStream,
			"h264/flac both supported, remux MKV→MPEG-TS"},

		// ── MPEG-TS files ──────────────────────────────────────────────────────
		{"ts/mpeg2video/ac3", "ts", "mpeg2video", "ac3", DecisionTranscode,
			"MPEG-2 video not browser-playable"},
		{"ts/h264/dts", "ts", "h264", "dts", DecisionTranscode,
			"h264 OK but DTS audio not supported → transcode audio"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := media.File{
				ID:          uuid.New(),
				MediaItemID: uuid.New(),
				FilePath:    "/media/test." + tc.container,
				VideoCodec:  strPtr(tc.video),
				AudioCodec:  strPtr(tc.audio),
				Container:   strPtr(tc.container),
				ResolutionW: intPtr(1920),
				ResolutionH: intPtr(1080),
			}
			got := Decide(file, webPlayerCaps, defaultServerCaps)
			if got != tc.want {
				t.Errorf("%s: want %s, got %s — %s", tc.name, tc.want, got, tc.reason)
			}
		})
	}
}

// TestDecide_CodecMatrix_HEVCClient covers the same library combinations when
// the client browser supports HEVC (Chrome 107+, Edge, Safari with MSE).
func TestDecide_CodecMatrix_HEVCClient(t *testing.T) {
	hevcCaps := ParseCapabilities("videoDecoder=h264:h265,audioDecoder=aac:mp3:opus:flac,protocols=mp4:ts")

	cases := []struct {
		name      string
		container string
		video     string
		audio     string
		want      Decision
		reason    string
	}{
		// ── AVI files — video codec still unsupported ──────────────────────
		{"avi/msmpeg4v2/mp3", "avi", "msmpeg4v2", "mp3", DecisionTranscode,
			"msmpeg4v2 not browser-playable even with HEVC support"},

		// ── MOV/MP4 files ──────────────────────────────────────────────────
		{"mp4/h264/aac", "mp4", "h264", "aac", DecisionDirectPlay,
			"unchanged — h264/aac/mp4 always direct play"},
		{"mp4/hevc/aac", "mp4", "hevc", "aac", DecisionDirectPlay,
			"HEVC client can direct play HEVC MP4"},
		{"mp4/hevc/eac3", "mp4", "hevc", "eac3", DecisionTranscode,
			"HEVC OK but E-AC-3 audio not browser-supported → transcode"},

		// ── MKV files ──────────────────────────────────────────────────────
		{"mkv/h264/aac", "mkv", "h264", "aac", DecisionDirectStream,
			"h264/aac supported, MKV not → remux"},
		{"mkv/hevc/aac", "mkv", "hevc", "aac", DecisionDirectStream,
			"HEVC client: HEVC/aac supported, MKV not → remux"},
		{"mkv/hevc/eac3", "mkv", "hevc", "eac3", DecisionTranscode,
			"HEVC OK but E-AC-3 not supported → transcode audio"},
		{"mkv/hevc/ac3", "mkv", "hevc", "ac3", DecisionTranscode,
			"HEVC OK but AC-3 not supported → transcode audio"},
		{"mkv/hevc/dts", "mkv", "hevc", "dts", DecisionTranscode,
			"HEVC OK but DTS not supported → transcode audio"},
		{"mkv/hevc/truehd", "mkv", "hevc", "truehd", DecisionTranscode,
			"HEVC OK but TrueHD not supported → transcode audio"},
		{"mkv/h264/dts", "mkv", "h264", "dts", DecisionTranscode,
			"DTS still not supported even with HEVC client"},
		{"mkv/h264/opus", "mkv", "h264", "opus", DecisionDirectStream,
			"unchanged — h264/opus remux"},
		{"mkv/av1/opus", "mkv", "av1", "opus", DecisionTranscode,
			"AV1 still not declared → transcode"},

		// ── MPEG-TS files ──────────────────────────────────────────────────
		{"ts/mpeg2video/ac3", "ts", "mpeg2video", "ac3", DecisionTranscode,
			"MPEG-2 video not browser-playable"},
		{"ts/h264/dts", "ts", "h264", "dts", DecisionTranscode,
			"DTS audio not supported"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := media.File{
				ID:          uuid.New(),
				MediaItemID: uuid.New(),
				FilePath:    "/media/test." + tc.container,
				VideoCodec:  strPtr(tc.video),
				AudioCodec:  strPtr(tc.audio),
				Container:   strPtr(tc.container),
				ResolutionW: intPtr(1920),
				ResolutionH: intPtr(1080),
			}
			got := Decide(file, hevcCaps, defaultServerCaps)
			if got != tc.want {
				t.Errorf("%s: want %s, got %s — %s", tc.name, tc.want, got, tc.reason)
			}
		})
	}
}
