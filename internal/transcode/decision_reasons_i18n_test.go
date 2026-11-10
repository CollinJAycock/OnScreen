package transcode

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/i18n"
)

// everyProblem is the table's "every problem is listed" file: HDR10 HEVC 4K
// with 5.1 audio, for an H.264 1080p stereo client.
func everyProblem() (media.File, ClientCapabilities) {
	f := baseFile()
	f.VideoCodec = strPtr("hevc")
	f.ResolutionW, f.ResolutionH = intPtr(3840), intPtr(2160)
	f.HDRType = strPtr("hdr10")
	f.AudioCodec = strPtr("ac3")
	f.AudioStreams = []byte(`[{"index":1,"codec":"ac3","channels":6}]`)
	return f, ParseCapabilities("videoDecoder=h264,audioDecoder=aac:ac3,maxAudioChannels=2")
}

func msgs(rs []ReasonMsg) []ReasonMessage {
	out := make([]ReasonMessage, len(rs))
	for i, r := range rs {
		out[i] = r.Msg
	}
	return out
}

// The same reasons read in Spanish and Chinese, and English is unchanged.
func TestDecisionReasonMsgs_Languages(t *testing.T) {
	f, caps := everyProblem()
	ms := msgs(DecisionReasonMsgs(f, caps))
	want := map[i18n.Lang][]string{
		i18n.English: {
			"HDR10 not supported by client (tonemapped to SDR)",
			"HEVC not supported by client",
			"3840×2160 exceeds client max 1920×1080",
			"5.1 audio → client max stereo",
		},
		i18n.Spanish: {
			"HDR10 no compatible con el cliente (mapeo de tonos a SDR)",
			"HEVC no compatible con el cliente",
			"3840×2160 supera el máx. del cliente: 1920×1080",
			"Audio 5.1 → máx. del cliente: estéreo",
		},
		i18n.SimplifiedChinese: {
			"客户端不支持 HDR10（已色调映射为 SDR）",
			"客户端不支持 HEVC",
			"3840×2160 超出客户端上限（1920×1080）",
			"音频（5.1）→ 客户端上限（立体声）",
		},
	}
	for l, w := range want {
		if got := MessageTexts(ms, l); !reflect.DeepEqual(got, w) {
			t.Errorf("%s:\n got  %q\n want %q", l, got, w)
		}
	}
	if got := ReasonTexts(DecisionReasons(f, caps)); !reflect.DeepEqual(got, want[i18n.English]) {
		t.Errorf("DecisionReasons: got %q", got)
	}
}

// Every reason DecisionReasonMsgs can give has a translation in each
// language, with its values filled in and no catalog key showing.
func TestDecisionReasonMsgs_AllTranslated(t *testing.T) {
	type tc struct {
		file func() media.File
		caps string
	}
	with := func(edit func(*media.File)) func() media.File {
		return func() media.File { f := baseFile(); edit(&f); return f }
	}
	cases := []tc{
		{with(func(f *media.File) { f.IntegrityStatus = media.IntegrityDamaged }), "videoDecoder=h264,audioDecoder=aac"},
		{with(func(f *media.File) { f.VideoCodec, f.HDRType = strPtr("hevc"), strPtr("dolby_vision") }), "videoDecoder=h264:h265,audioDecoder=aac"},
		{with(func(f *media.File) { f.VideoBitDepth = intPtr(10) }), "videoDecoder=h264,audioDecoder=aac"},
		{with(func(f *media.File) { f.VideoCodec, f.VideoBitDepth = strPtr("hevc"), intPtr(12) }), "videoDecoder=h264:h265,audioDecoder=aac,maxBitDepth=10"},
		{with(func(f *media.File) {
			f.VideoProfile, f.VideoLevel = strPtr("High 10"), intPtr(51)
		}), "videoDecoder=h264,audioDecoder=aac,h264Profile=high,h264Level=4.1"},
		{with(func(f *media.File) {
			f.AudioCodec, f.Container = strPtr("truehd"), strPtr("mpegts")
		}), "videoDecoder=h264,audioDecoder=aac:truehd,protocols=mkv:ts"},
		{with(func(f *media.File) {
			f.VideoCodec, f.AudioCodec, f.Container = strPtr("vp9"), strPtr("opus"), strPtr("webm")
		}), "videoDecoder=h264:vp9,audioDecoder=aac,protocols=mkv:mp4"},
		{with(func(f *media.File) {
			f.AudioStreams = []byte(`[{"index":1,"codec":"aac","channels":4}]`)
		}), "videoDecoder=h264,audioDecoder=aac,maxAudioChannels=1"},
	}
	f, caps := everyProblem()
	all := DecisionReasonMsgs(f, caps)
	for _, c := range cases {
		all = append(all, DecisionReasonMsgs(c.file(), ParseCapabilities(c.caps))...)
	}
	seen := map[string]bool{}
	for _, r := range all {
		seen[r.Msg.Key] = true
		en := r.Msg.Text(i18n.English)
		for _, l := range i18n.Supported[1:] {
			got := r.Msg.Text(l)
			if got == en || strings.ContainsAny(got, "{}") || strings.Contains(got, "reason.") {
				t.Errorf("%s: %s = %q (English %q)", l, r.Msg.Key, got, en)
			}
		}
	}
	if len(seen) != 14 { // every key DecisionReasonMsgs uses
		t.Errorf("%d reason keys exercised, want all 14: %v", len(seen), seen)
	}
}

// Words used as values (a channel layout, a width alone) are terms, put in
// the reader's language; layouts in digits and resolutions are not.
func TestReasonValues(t *testing.T) {
	cases := []struct {
		v              any
		en, es, zhHans string
	}{
		{channelValue(2), "stereo", "estéreo", "立体声"},
		{channelValue(1), "mono", "mono", "单声道"},
		{channelValue(4), "4-channel", "4 canales", "4 声道"},
		{channelValue(8), "7.1", "7.1", "7.1"},
		{dimsValue(3840, 0), "3840px wide", "3840 px de ancho", "宽度 3840px"},
		{dimsValue(0, 2160), "2160p", "2160p", "2160p"},
		{dimsValue(3840, 2160), "3840×2160", "3840×2160", "3840×2160"},
	}
	for _, c := range cases {
		for l, want := range map[i18n.Lang]string{i18n.English: c.en, i18n.Spanish: c.es, i18n.SimplifiedChinese: c.zhHans} {
			if got := valueText(c.v, l); got != want {
				t.Errorf("%s: %v = %q, want %q", l, c.v, got, want)
			}
		}
	}
	// Numbers are plain digits in every language: never "3,840" or "3.840".
	m := NewReasonMessage("reason.qualityPicked", "height", 1440)
	if got := m.Text(i18n.Spanish); !strings.Contains(got, "1440p") {
		t.Errorf("es: %q", got)
	}
	if got := m.String(); got != "Quality set to 1440p by the viewer" {
		t.Errorf("en: %q", got)
	}
}

// A session stores the messages and their English. A reader gets them in
// their language; a session from before translations, or rewritten by a
// node from then, shows its English as it is.
func TestSessionReasons_StoredAndRead(t *testing.T) {
	f, caps := everyProblem()
	var s Session
	s.SetTranscodeReasons(msgs(DecisionReasonMsgs(f, caps)))
	if !reflect.DeepEqual(s.TranscodeReasons, ReasonTexts(DecisionReasons(f, caps))) {
		t.Fatalf("English: got %q", s.TranscodeReasons)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	// An older node reads transcode_reasons, as before.
	var old struct {
		TranscodeReasons []string `json:"transcode_reasons"`
	}
	if err := json.Unmarshal(raw, &old); err != nil || !reflect.DeepEqual(old.TranscodeReasons, s.TranscodeReasons) {
		t.Errorf("transcode_reasons for an older node: %q (%v)", old.TranscodeReasons, err)
	}
	var back Session
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if got := back.ReasonsIn(i18n.SimplifiedChinese); len(got) != 4 || got[1] != "客户端不支持 HEVC" {
		t.Errorf("zh-Hans: %q", got)
	}
	if got := back.ReasonsIn(i18n.English); !reflect.DeepEqual(got, s.TranscodeReasons) {
		t.Errorf("en: %q", got)
	}

	// Stored before translations (or rewritten without the messages).
	var legacy Session
	if err := json.Unmarshal([]byte(`{"id":"s1","transcode_reasons":["HEVC not supported by client"]}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if got := legacy.ReasonsIn(i18n.Spanish); !reflect.DeepEqual(got, []string{"HEVC not supported by client"}) {
		t.Errorf("legacy session: %q", got)
	}

	// A key a newer node knows and this build doesn't: its stored English.
	future := Session{
		TranscodeReasons:        []string{"HEVC not supported by client", "Something new"},
		TranscodeReasonMessages: []ReasonMessage{NewReasonMessage("reason.videoCodec", "codec", "HEVC"), {Key: "reason.notYetInvented"}},
	}
	if got := future.ReasonsIn(i18n.Spanish); !reflect.DeepEqual(got, []string{"HEVC no compatible con el cliente", "Something new"}) {
		t.Errorf("unknown key: %q", got)
	}
}
