package transcode

import "testing"

// The transcode builders must confine ffmpeg's demuxer with -protocol_whitelist
// so a crafted "media" file (content the threat model treats as attacker-
// controlled) cannot make ffmpeg open http(s) URLs (SSRF) or unrelated inputs.
// -protocol_whitelist is an INPUT option: it governs only the -i that follows
// it, so these tests pin BOTH that it is present AND that it precedes -i. They
// also pin that a local input is denied the network stack while the HTTP source
// is granted it — the property that stops a local file pivoting to the network.

func TestBuildHLS_LocalInput_ProtocolWhitelistBeforeInput(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "/media/movie.mkv",
		Encoder:       EncoderSoftware,
		Width:         1920,
		Height:        1080,
		BitrateKbps:   8000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/onscreen/sessions/abc",
		SegmentPrefix: "seg",
	})

	wl := argIndex(args, "-protocol_whitelist")
	in := argIndex(args, "-i")
	if wl == -1 {
		t.Fatalf("expected -protocol_whitelist in args: %v", args)
	}
	if in == -1 || wl >= in {
		t.Fatalf("-protocol_whitelist must appear before -i (wl=%d, i=%d): %v", wl, in, args)
	}
	got := args[wl+1]
	if got != "file,pipe,crypto,data,subfile" {
		t.Errorf("local input whitelist = %q; must not include the http stack", got)
	}
}

func TestBuildHLS_HTTPSource_WhitelistAllowsHTTP(t *testing.T) {
	args := BuildHLS(BuildArgs{
		InputPath:     "http://10.0.0.5:7070/media/stream/abc?token=v4.local.x",
		Encoder:       EncoderSoftware,
		Width:         1280,
		Height:        720,
		BitrateKbps:   4000,
		AudioCodec:    "aac",
		SessionDir:    "/tmp/onscreen/sessions/abc",
		SegmentPrefix: "seg",
	})

	wl := argIndex(args, "-protocol_whitelist")
	in := argIndex(args, "-i")
	if wl == -1 || in == -1 || wl >= in {
		t.Fatalf("-protocol_whitelist must be present and precede -i (wl=%d, i=%d)", wl, in)
	}
	got := args[wl+1]
	// The fleet HTTP source is a URL the server itself minted, so it legitimately
	// needs the http stack — but only because the input is http(s).
	if got != "file,pipe,crypto,data,subfile,http,https,tcp,tls" {
		t.Errorf("http source whitelist = %q; want the network-inclusive set", got)
	}
}

func TestBuildDirectStream_ProtocolWhitelistBeforeInput(t *testing.T) {
	args := BuildDirectStream("/media/movie.mkv", "/tmp/onscreen/sessions/abc", 0)

	wl := argIndex(args, "-protocol_whitelist")
	in := argIndex(args, "-i")
	if wl == -1 {
		t.Fatalf("expected -protocol_whitelist in direct-stream args: %v", args)
	}
	if in == -1 || wl >= in {
		t.Fatalf("-protocol_whitelist must precede -i (wl=%d, i=%d): %v", wl, in, args)
	}
	if got := args[wl+1]; got != "file,pipe,crypto,data,subfile" {
		t.Errorf("local direct-stream whitelist = %q; must not include the http stack", got)
	}
}
