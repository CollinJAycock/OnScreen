package transcode

import (
	"strings"
	"testing"
)

func TestIsHTTPURL(t *testing.T) {
	cases := map[string]bool{
		"http://10.0.0.5:7070/media/stream/abc?token=v4.local.x": true,
		"https://host/media/stream/abc":                          true,
		"/mnt/media/movie.mkv":                                   false,
		`C:\media\movie.mkv`:                                     false,
		"":                                                       false,
		"ftp://host/x":                                           false,
	}
	for in, want := range cases {
		if got := isHTTPURL(in); got != want {
			t.Errorf("isHTTPURL(%q) = %v, want %v", in, got, want)
		}
	}
}

// argIndex returns the position of the first occurrence of v in args, or -1.
func argIndex(args []string, v string) int {
	for i, a := range args {
		if a == v {
			return i
		}
	}
	return -1
}

func TestBuildHLS_HTTPSourceReconnectFlags(t *testing.T) {
	httpArgs := BuildHLS(BuildArgs{
		InputPath:     "http://10.0.0.5:7070/media/stream/abc?token=v4.local.x",
		Encoder:       EncoderSoftware,
		Width:         1280,
		Height:        720,
		BitrateKbps:   3000,
		SessionDir:    "/tmp/s",
		SegmentPrefix: "seg",
	})
	rc := argIndex(httpArgs, "-reconnect")
	if rc < 0 {
		t.Fatalf("expected -reconnect for HTTP source, args: %v", httpArgs)
	}
	if argIndex(httpArgs, "-multiple_requests") < 0 {
		t.Errorf("expected -multiple_requests for HTTP source")
	}
	// Reconnect options must precede the input they apply to.
	if iInput := argIndex(httpArgs, "-i"); iInput < 0 || rc > iInput {
		t.Errorf("-reconnect (%d) must precede -i (%d)", rc, iInput)
	}

	localArgs := BuildHLS(BuildArgs{
		InputPath:     "/mnt/media/movie.mkv",
		Encoder:       EncoderSoftware,
		Width:         1280,
		Height:        720,
		BitrateKbps:   3000,
		SessionDir:    "/tmp/s",
		SegmentPrefix: "seg",
	})
	if argIndex(localArgs, "-reconnect") >= 0 {
		t.Errorf("local input must not get -reconnect, args: %v", localArgs)
	}
}

func TestBuildDirectStream_HTTPSourceReconnectFlags(t *testing.T) {
	httpArgs := BuildDirectStream("http://10.0.0.5:7070/media/stream/abc?token=t", "/tmp/s", 0)
	if argIndex(httpArgs, "-reconnect") < 0 {
		t.Errorf("expected -reconnect for HTTP direct-stream source, args: %v", httpArgs)
	}
	localArgs := BuildDirectStream("/mnt/media/movie.mkv", "/tmp/s", 0)
	if argIndex(localArgs, "-reconnect") >= 0 {
		t.Errorf("local input must not get -reconnect, args: %v", localArgs)
	}
}

func TestRedactURLToken(t *testing.T) {
	in := "http://10.0.0.5:7070/media/stream/abc?token=v4.local.SECRETSECRET"
	got := redactURLToken(in)
	if strings.Contains(got, "SECRET") {
		t.Errorf("token leaked into redacted form: %q", got)
	}
	if !strings.HasPrefix(got, "http://10.0.0.5:7070/media/stream/abc?token=") {
		t.Errorf("redaction dropped the non-secret prefix: %q", got)
	}
	// No token query → returned unchanged.
	const noTok = "http://10.0.0.5:7070/media/stream/abc"
	if redactURLToken(noTok) != noTok {
		t.Errorf("expected no-op without a token param, got %q", redactURLToken(noTok))
	}
}
