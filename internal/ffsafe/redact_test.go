package ffsafe

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRedactURLs(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"no url", "frame=  120 fps= 60 q=28.0 size=1024kB", "frame=  120 fps= 60 q=28.0 size=1024kB"},
		{"url without query", "Error opening input file http://10.0.0.5:7070/media/stream/abc.",
			"Error opening input file http://10.0.0.5:7070/media/stream/abc."},
		{"stream token",
			"Error opening input file http://10.0.0.5:7070/media/stream/abc?token=v4.local.SECRET.",
			"Error opening input file http://10.0.0.5:7070/media/stream/abc?REDACTED"},
		{"colon separator kept",
			"http://10.0.0.5:7070/media/stream/abc?token=v4.local.SECRET: Server returned 403 Forbidden (access denied)",
			"http://10.0.0.5:7070/media/stream/abc?REDACTED: Server returned 403 Forbidden (access denied)"},
		{"quoted presigned url",
			"Impossible to open 'https://bucket.s3.example/k.mkv?X-Amz-Signature=SIG&X-Amz-Credential=AKIA%2Fx'",
			"Impossible to open 'https://bucket.s3.example/k.mkv?REDACTED'"},
		{"two urls on a line",
			"a http://h/x?token=one b https://h/y?sig=two c",
			"a http://h/x?REDACTED b https://h/y?REDACTED c"},
		{"line ending kept", "open http://h/x?token=one\r\n", "open http://h/x?REDACTED\r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactURLs(c.in)
			if got != c.want {
				t.Fatalf("RedactURLs(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// A URL split across writes, as a pipe read can split it, is masked whole.
func TestRedactingWriter_SplitWrites(t *testing.T) {
	var out bytes.Buffer
	w := NewRedactingWriter(&out)
	line := "Error opening input file http://10.0.0.5:7070/media/stream/abc?token=v4.local.SECRETTOKEN.\n"
	for i := 0; i < len(line); i += 7 {
		end := min(i+7, len(line))
		if _, err := w.Write([]byte(line[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := out.String(), "Error opening input file http://10.0.0.5:7070/media/stream/abc?REDACTED\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRedactingWriter_HoldsPartialLineUntilFlush(t *testing.T) {
	var out bytes.Buffer
	w := NewRedactingWriter(&out)
	_, _ = w.Write([]byte("frame= 1\rframe= 2\rlast http://h/x?token=SECRET"))
	if got := out.String(); got != "frame= 1\rframe= 2\r" {
		t.Fatalf("before flush: %q", got)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "frame= 1\rframe= 2\rlast http://h/x?REDACTED" {
		t.Fatalf("after flush: %q", got)
	}
	if err := w.Flush(); err != nil || out.Len() != len("frame= 1\rframe= 2\rlast http://h/x?REDACTED") {
		t.Fatalf("second flush wrote again: %q", out.String())
	}
}

func TestRedactingWriter_LongLineIsNotHeldForever(t *testing.T) {
	var out bytes.Buffer
	w := NewRedactingWriter(&out)
	_, _ = w.Write(bytes.Repeat([]byte("x"), maxPendingLine))
	if out.Len() != maxPendingLine {
		t.Fatalf("wrote %d bytes, want %d", out.Len(), maxPendingLine)
	}
}

// ffmpeg's own messages, not a guess at them: a failing URL input is named
// with its query in "Error opening input file <url>.".
func TestRedactingWriter_RealFFmpegError(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	const secret = "v4.local.SECRETTOKEN"
	in := "http://127.0.0.1:1/media/stream/abc?token=" + secret
	var raw, out bytes.Buffer
	w := NewRedactingWriter(&out)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "warning",
		"-protocol_whitelist", Whitelist(in), "-i", in, "-f", "null", "-")
	cmd.Stderr = io.MultiWriter(&raw, w)
	_ = cmd.Run()
	_ = w.Flush()
	if !strings.Contains(raw.String(), secret) {
		t.Skipf("this ffmpeg didn't print its input URL: %q", raw.String())
	}
	if strings.Contains(out.String(), secret) {
		t.Fatalf("token survived redaction:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "http://127.0.0.1:1/media/stream/abc?REDACTED") {
		t.Fatalf("redacted output lost the URL:\n%s", out.String())
	}
}
