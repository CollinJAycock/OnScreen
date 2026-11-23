package artwork

import (
	"bytes"
	"errors"
	"image"
	"io"
	"strings"
	"testing"
)

// A format whose decoder panics, standing in for imaging 1.6.2's crafted-TIFF
// panic (GHSA-q7pp-wcgr-pffx / CVE-2023-36308, no patched release): the panic
// must come back as an error, not take the caller's goroutine down.
func init() {
	image.RegisterFormat("onscreen-panic-test", "PANICIMG",
		func(io.Reader) (image.Image, error) { panic("index out of range [7] with length 3") },
		func(io.Reader) (image.Config, error) { return image.Config{}, errors.New("no config") },
	)
}

func TestDecodeResizeEncodeJPEG_DecoderPanicIsAnError(t *testing.T) {
	out, err := decodeResizeEncodeJPEG(bytes.NewReader([]byte("PANICIMG and then some bytes")), 300, 0, jpegQuality)
	if err == nil {
		t.Fatal("want an error from a decoder that panics, got nil")
	}
	if out != nil {
		t.Fatalf("want no output, got %d bytes", len(out))
	}
	if !strings.Contains(err.Error(), "index out of range") {
		t.Fatalf("want the panic's message in the error, got %q", err)
	}
}
