package ocr

import (
	"strings"
	"testing"
)

func TestIsImageBased(t *testing.T) {
	cases := []struct {
		codec string
		want  bool
	}{
		{"hdmv_pgs_subtitle", true},
		{"PGSSUB", true},
		{" dvd_subtitle ", true},
		{"dvb_subtitle", true},
		{"xsub", true},
		{"subrip", false},
		{"ass", false},
		{"mov_text", false},
		{"webvtt", false},
		{"", false},
		{"unknown_codec", false},
	}
	for _, tc := range cases {
		if got := IsImageBased(tc.codec); got != tc.want {
			t.Errorf("IsImageBased(%q) = %v, want %v", tc.codec, got, tc.want)
		}
	}
}

func TestLangToTesseract(t *testing.T) {
	cases := map[string]string{
		"":        "eng",
		"en":      "eng",
		"eng":     "eng",
		"ES":      "spa",
		"spa":     "spa",
		"fr":      "fra",
		"fre":     "fra",
		"fra":     "fra",
		"de":      "deu",
		"ger":     "deu",
		"deu":     "deu",
		"it":      "ita",
		"ita":     "ita",
		"pt":      "por",
		"por":     "por",
		"ja":      "jpn",
		"jpn":     "jpn",
		"zh":      "chi_sim",
		"chi":     "chi_sim",
		"chi_sim": "chi_sim",
		"zho":     "chi_sim",
		"zh-tw":   "chi_tra",
		"chi_tra": "chi_tra",
		"ko":      "kor",
		"kor":     "kor",
		"ru":      "rus",
		"rus":     "rus",
		"ar":      "ara",
		"ara":     "ara",
		" EN ":    "eng", // whitespace tolerated
		"klingon": "eng", // unknown falls back to eng
	}
	for in, want := range cases {
		if got := LangToTesseract(in); got != want {
			t.Errorf("LangToTesseract(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanText(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello\n", "hello"},
		{"  hello  \n  world  \n", "hello\nworld"},
		{"\r\nfoo\r\n\r\nbar\r\n", "foo\nbar"},
		{"\n\n\n", ""},
		{"only one line", "only one line"},
	}
	for _, tc := range cases {
		got := cleanText(tc.in)
		if got != tc.want {
			t.Errorf("cleanText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestVTTTime(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "00:00:00.000"},
		{-50, "00:00:00.000"}, // clamps negative
		{1, "00:00:00.001"},
		{1234, "00:00:01.234"},
		{60_000, "00:01:00.000"},
		{3_600_000, "01:00:00.000"},
		{3_661_500, "01:01:01.500"},
	}
	for _, tc := range cases {
		got := vttTime(tc.ms)
		if got != tc.want {
			t.Errorf("vttTime(%d) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}

func TestCuesToVTT(t *testing.T) {
	cues := []Cue{
		{StartMS: 0, EndMS: 1500, Text: "First line"},
		{StartMS: 2000, EndMS: 3500, Text: "Second\ntwo lines"},
		{StartMS: 4000, EndMS: 4000, Text: "skip — zero duration"},
		{StartMS: 5000, EndMS: 6000, Text: ""}, // skip — empty text
		{StartMS: 7000, EndMS: 8000, Text: "Third"},
	}
	got := string(CuesToVTT(cues))

	if !strings.HasPrefix(got, "WEBVTT\n\n") {
		t.Fatalf("missing WEBVTT header: %q", got[:min(20, len(got))])
	}
	for _, want := range []string{
		"00:00:00.000 --> 00:00:01.500\nFirst line",
		"00:00:02.000 --> 00:00:03.500\nSecond\ntwo lines",
		"00:00:07.000 --> 00:00:08.000\nThird",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing cue %q\nfull output:\n%s", want, got)
		}
	}
	for _, bad := range []string{
		"skip — zero duration",
		"00:00:05.000",
	} {
		if strings.Contains(got, bad) {
			t.Errorf("output should not contain %q\n%s", bad, got)
		}
	}
}

func TestEngineAvailable_MissingBinary(t *testing.T) {
	// Point all three paths at something guaranteed not to exist on PATH.
	e := &Engine{
		FFmpegPath:    "definitely-not-a-real-binary-ffmpeg-xyz",
		FFprobePath:   "definitely-not-a-real-binary-ffprobe-xyz",
		TesseractPath: "definitely-not-a-real-binary-tesseract-xyz",
	}
	err := e.Available()
	if err == nil {
		t.Fatal("expected Available() to error when binaries are missing")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("expected 'not found on PATH' in error, got %q", err.Error())
	}
}

func TestEngineDefaults(t *testing.T) {
	e := &Engine{}
	if e.ffmpeg() != "ffmpeg" {
		t.Errorf("default ffmpeg = %q", e.ffmpeg())
	}
	if e.ffprobe() != "ffprobe" {
		t.Errorf("default ffprobe = %q", e.ffprobe())
	}
	if e.tesseract() != "tesseract" {
		t.Errorf("default tesseract = %q", e.tesseract())
	}
	if e.canvasW() != 1920 || e.canvasH() != 1080 {
		t.Errorf("default canvas = %dx%d, want 1920x1080", e.canvasW(), e.canvasH())
	}

	custom := &Engine{
		FFmpegPath:    "/opt/ffmpeg",
		FFprobePath:   "/opt/ffprobe",
		TesseractPath: "/opt/tesseract",
		CanvasW:       3840,
		CanvasH:       2160,
	}
	if custom.ffmpeg() != "/opt/ffmpeg" || custom.canvasW() != 3840 || custom.canvasH() != 2160 {
		t.Errorf("custom paths/canvas not honored: %+v", custom)
	}
}
