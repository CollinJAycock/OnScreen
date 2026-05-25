package preencode

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/mediastore"
	"github.com/onscreen/onscreen/internal/staticabr"
	"github.com/onscreen/onscreen/internal/transcode"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// readOnlyStore is a Store that is NOT a Putter.
type readOnlyStore struct{}

func (readOnlyStore) Open(context.Context, string) (io.ReadSeekCloser, error) {
	return nil, mediastore.ErrNotFound
}
func (readOnlyStore) Stat(context.Context, string) (mediastore.FileInfo, error) {
	return mediastore.FileInfo{}, mediastore.ErrNotFound
}
func (readOnlyStore) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func TestRungArgs_H264(t *testing.T) {
	e := New(nil, "", "ffmpeg", 6, 0, discardLogger())
	r := transcode.Rendition{Label: "720p", Height: 720, Width: 1280, BitrateKbps: 4000}
	args := strings.Join(e.rungArgs("/media/src.mkv", "h264", r, "/tmp/720p"), " ")

	for _, want := range []string{"libx264", "scale=-2:720", "4000k", "-f hls", "index.m3u8", "0:a:0?", "-hls_playlist_type vod"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q in: %s", want, args)
		}
	}
	if strings.Contains(args, "hvc1") {
		t.Error("h264 must not set the hvc1 tag")
	}
	if strings.Contains(args, "-reconnect") {
		t.Error("a local path input must not get reconnect flags")
	}
}

func TestRungArgs_HEVC_TagAndURLReconnect(t *testing.T) {
	e := New(nil, "", "ffmpeg", 6, 0, discardLogger())
	r := transcode.Rendition{Label: "1080p", Height: 1080, Width: 1920, BitrateKbps: 6000}
	args := strings.Join(e.rungArgs("https://cdn.example/src.mkv", "hevc", r, "/tmp/1080p"), " ")

	for _, want := range []string{"libx265", "-tag:v hvc1", "-reconnect 1"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q in: %s", want, args)
		}
	}
}

func TestEncode_ReadOnlyStoreErrors(t *testing.T) {
	// The Putter check happens before any ffmpeg work, so this needs no ffmpeg.
	e := New(readOnlyStore{}, "", "ffmpeg", 6, 0, discardLogger())
	err := e.Encode(context.Background(), staticabr.Source{
		FileID: uuid.New(), Width: 1920, Height: 1080, BitrateKbps: 8000, Codec: "h264",
	})
	if err == nil {
		t.Error("expected an error encoding to a read-only store")
	}
}

func TestKey_PrefixApplied(t *testing.T) {
	e := New(nil, "/var/cache/static", "ffmpeg", 6, 0, discardLogger())
	if got := e.key(staticabr.MasterKey(uuid.Nil)); !strings.HasPrefix(got, "/var/cache/static/static-abr/") {
		t.Errorf("key not prefixed: %s", got)
	}
	// Empty prefix (object storage) leaves the key bucket-relative.
	e2 := New(nil, "", "ffmpeg", 6, 0, discardLogger())
	if got := e2.key("static-abr/x/master.m3u8"); got != "static-abr/x/master.m3u8" {
		t.Errorf("empty prefix changed key: %s", got)
	}
}
