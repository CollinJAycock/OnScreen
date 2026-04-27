package transcode

import (
	"encoding/xml"
	"strings"
	"testing"
)

// minimalParams returns a DASHParams with the bare minimum BuildMPD
// requires to succeed — tests then mutate and add to it.
func minimalParams() DASHParams {
	return DASHParams{
		SessionID:          "s-abc",
		Token:              "tok-xyz",
		SegmentDurationSec: 4,
		SegmentCount:       10,
		BandwidthBps:       8_000_000,
		VideoCodec:         "hvc1.1.6.L120.90",
	}
}

func TestBuildMPD_RequiresSessionAndToken(t *testing.T) {
	if _, err := BuildMPD(DASHParams{Token: "t"}); err == nil {
		t.Error("missing SessionID: expected error")
	}
	if _, err := BuildMPD(DASHParams{SessionID: "s"}); err == nil {
		t.Error("missing Token: expected error")
	}
}

func TestBuildMPD_StaticManifest_HasMatchingDuration(t *testing.T) {
	p := minimalParams()
	out, err := BuildMPD(p)
	if err != nil {
		t.Fatalf("BuildMPD: %v", err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "<?xml") {
		t.Error("MPD must begin with the XML declaration — many DASH parsers reject manifests that omit it")
	}
	// 10 segments × 4 s = 40 s — matters because shaka-player uses this
	// to set the seek bar length on type=static.
	if !strings.Contains(s, `mediaPresentationDuration="PT40S"`) {
		t.Errorf("missing or wrong mediaPresentationDuration; got:\n%s", s)
	}
	if !strings.Contains(s, `type="static"`) {
		t.Error("expected type=static when Live=false")
	}
	if strings.Contains(s, "minimumUpdatePeriod") {
		t.Error("static MPD must not advertise minimumUpdatePeriod (clients would re-poll an unchanging manifest)")
	}
}

func TestBuildMPD_LiveManifest_DropsDurationAddsUpdatePeriod(t *testing.T) {
	p := minimalParams()
	p.Live = true
	out, _ := BuildMPD(p)
	s := string(out)
	if !strings.Contains(s, `type="dynamic"`) {
		t.Error("live MPD must declare type=dynamic so DASH clients keep polling")
	}
	if strings.Contains(s, "mediaPresentationDuration=") {
		t.Error("live MPD must omit mediaPresentationDuration (the transcode is still producing segments)")
	}
	if !strings.Contains(s, "minimumUpdatePeriod") {
		t.Error("live MPD requires minimumUpdatePeriod so clients know how often to re-fetch")
	}
}

func TestBuildMPD_VideoOnly_OmitsAudioAdaptationSet(t *testing.T) {
	// AudioCodec="" is the signal that this is a video-only debug
	// session. The MPD should omit the audio AdaptationSet entirely
	// rather than emit one with no codec, which would crash some
	// clients.
	p := minimalParams()
	out, _ := BuildMPD(p)

	var parsed mpdRoot
	if err := xml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("re-parse mpd: %v", err)
	}
	if len(parsed.Period.AdaptationSets) != 1 {
		t.Errorf("got %d adaptation sets, want 1 (video only)", len(parsed.Period.AdaptationSets))
	}
	if parsed.Period.AdaptationSets[0].MimeType != "video/mp4" {
		t.Errorf("first set mime: got %q, want video/mp4", parsed.Period.AdaptationSets[0].MimeType)
	}
}

func TestBuildMPD_WithAudio_EmitsTwoAdaptationSets(t *testing.T) {
	p := minimalParams()
	p.AudioCodec = "mp4a.40.2"
	p.AudioBandwidthBps = 192_000
	out, _ := BuildMPD(p)

	var parsed mpdRoot
	if err := xml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("re-parse mpd: %v", err)
	}
	if len(parsed.Period.AdaptationSets) != 2 {
		t.Fatalf("got %d adaptation sets, want 2", len(parsed.Period.AdaptationSets))
	}
	audio := parsed.Period.AdaptationSets[1]
	if audio.MimeType != "audio/mp4" {
		t.Errorf("audio mime: got %q, want audio/mp4", audio.MimeType)
	}
	if got := audio.Representations[0].Bandwidth; got != 192_000 {
		t.Errorf("audio bandwidth: got %d, want 192000", got)
	}
}

func TestBuildMPD_TokenIsAppendedToSegmentURLs(t *testing.T) {
	// The MPD must be self-contained — segment fetches go directly to
	// the URLs in @initialization and @media without further client
	// rewriting. If the token weren't baked in, every segment fetch
	// would 401.
	p := minimalParams()
	out, _ := BuildMPD(p)
	s := string(out)
	if !strings.Contains(s, "init.mp4?token=tok-xyz") {
		t.Errorf("init segment URL missing token; got:\n%s", s)
	}
	if !strings.Contains(s, `seg$Number%05d$.m4s?token=tok-xyz`) {
		t.Errorf("media segment template missing token; got:\n%s", s)
	}
}

func TestBuildMPD_BaseURLEnsuresTrailingSlash(t *testing.T) {
	// SegmentTemplate URLs are resolved relative to BaseURL — without
	// a trailing slash the resolution loses the last path component
	// and segments 404.
	p := minimalParams()
	p.BaseURL = "/api/v1/transcode/sessions/s-abc/seg" // no slash
	out, _ := BuildMPD(p)
	if !strings.Contains(string(out), "<BaseURL>/api/v1/transcode/sessions/s-abc/seg/</BaseURL>") {
		t.Errorf("BaseURL must be normalised with trailing slash; got:\n%s", string(out))
	}
}

func TestBuildMPD_OmitsResolutionWhenZero(t *testing.T) {
	// Width/Height are advisory metadata. Emitting width="0" would
	// confuse clients into rendering a 0×0 surface and freezing the
	// player. omitempty must suppress them.
	p := minimalParams()
	out, _ := BuildMPD(p)
	if strings.Contains(string(out), `width="0"`) || strings.Contains(string(out), `height="0"`) {
		t.Errorf("zero width/height must be omitted, not emitted; got:\n%s", string(out))
	}
}

func TestBuildMPD_DefaultsCodecToAvc1(t *testing.T) {
	// Empty VideoCodec falls back to "avc1" rather than panicking —
	// shaka-player and exoplayer both probe the init segment when the
	// codec string is generic, so we still play. Better than failing
	// to emit a manifest at all.
	p := minimalParams()
	p.VideoCodec = ""
	out, err := BuildMPD(p)
	if err != nil {
		t.Fatalf("BuildMPD with empty codec: %v", err)
	}
	if !strings.Contains(string(out), `codecs="avc1"`) {
		t.Errorf("expected default codec 'avc1'; got:\n%s", string(out))
	}
}
