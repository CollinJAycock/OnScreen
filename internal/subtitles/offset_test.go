package subtitles

import (
	"errors"
	"testing"
)

func TestShiftVTT_ZeroOffsetIsIdentity(t *testing.T) {
	in := []byte("WEBVTT\r\n\r\n00:00:01,000 --> 00:00:02.000\r\nHi\r\n")
	if got := ShiftVTT(in, 0); string(got) != string(in) {
		t.Fatalf("zero offset changed the document: %q", got)
	}
}

func TestShiftVTT_Later(t *testing.T) {
	in := "WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.500 align:start line:90%\nHello\n\n59:59.900 --> 1:00:00.000\nWorld\n"
	want := "WEBVTT\n\n1\n00:00:02.500 --> 00:00:04.000 align:start line:90%\nHello\n\n01:00:01.400 --> 01:00:01.500\nWorld\n"
	if got := string(ShiftVTT([]byte(in), 1500)); got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestShiftVTT_EarlierClampsAndDrops(t *testing.T) {
	in := "WEBVTT\n\nNOTE made by ffmpeg\n\n1\n00:00:00.500 --> 00:00:01.000\nGone\nsecond line\n\n2\n00:00:01.000 --> 00:00:03.000\nClamped\n\n00:00:05.000 --> 00:00:06.000\nShifted\n"
	want := "WEBVTT\n\nNOTE made by ffmpeg\n\n2\n00:00:00.000 --> 00:00:01.000\nClamped\n\n00:00:03.000 --> 00:00:04.000\nShifted\n"
	if got := string(ShiftVTT([]byte(in), -2000)); got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestShiftVTT_CueEndingExactlyAtZeroIsDropped(t *testing.T) {
	in := "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nA\n\n00:00:02.000 --> 00:00:03.000\nB\n"
	want := "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nB\n"
	if got := string(ShiftVTT([]byte(in), -2000)); got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestShiftVTT_MalformedTimingLeftAlone(t *testing.T) {
	in := "WEBVTT\n\nabc --> def\nText\n\n00:00:01.000 --> 00:00:02.000\nOK\n"
	want := "WEBVTT\n\nabc --> def\nText\n\n00:00:01.100 --> 00:00:02.100\nOK\n"
	if got := string(ShiftVTT([]byte(in), 100)); got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestShiftVTT_DroppedCueWithoutGapKeepsNextCue(t *testing.T) {
	// No blank line between the cues: the next timing line still survives.
	in := "WEBVTT\n\n00:00:00.100 --> 00:00:00.200\nGone\n00:00:05.000 --> 00:00:06.000\nKept\n"
	want := "WEBVTT\n\n00:00:04.000 --> 00:00:05.000\nKept\n"
	if got := string(ShiftVTT([]byte(in), -1000)); got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestParseVTTTime(t *testing.T) {
	cases := map[string]int64{
		"00:00:01.000":  1000,
		"01:02.5":       62500,
		"1:00:00,250":   3600250,
		"100:00:00.000": 360000000,
		"00:00:03":      3000,
	}
	for in, want := range cases {
		got, err := parseVTTTime(in)
		if err != nil || got != want {
			t.Errorf("parseVTTTime(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "12", "00:61:00.000", "aa:bb.ccc", "-1:00.000", "00:00:00.-12"} {
		if _, err := parseVTTTime(bad); err == nil {
			t.Errorf("parseVTTTime(%q) accepted", bad)
		}
	}
}

func TestValidateOffset(t *testing.T) {
	for _, ok := range []int{0, 1, -1, MaxOffsetMS, -MaxOffsetMS} {
		if err := ValidateOffset(ok); err != nil {
			t.Errorf("ValidateOffset(%d) = %v", ok, err)
		}
	}
	for _, bad := range []int{MaxOffsetMS + 1, -MaxOffsetMS - 1} {
		if err := ValidateOffset(bad); !errors.Is(err, ErrOffsetRange) {
			t.Errorf("ValidateOffset(%d) = %v, want ErrOffsetRange", bad, err)
		}
	}
}
