package v1

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/config"
	"github.com/onscreen/onscreen/internal/mediastore"
)

// sourceOffloadStore hands back a signed URL so buildSourceURL prefers it over
// the LAN stream-token path (the object-storage / CDN case).
type sourceOffloadStore struct{ url string }

func (o sourceOffloadStore) Open(context.Context, string) (io.ReadSeekCloser, error) {
	return nil, errors.New("Open must not be called by buildSourceURL")
}
func (o sourceOffloadStore) Stat(context.Context, string) (mediastore.FileInfo, error) {
	return mediastore.FileInfo{}, errors.New("Stat must not be called by buildSourceURL")
}
func (o sourceOffloadStore) SignedURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	return o.url, nil
}

func TestBuildSourceURL_PrefersStoreSignedURL(t *testing.T) {
	// When the store can offload, the worker should read source straight from the
	// signed bucket/CDN URL — taking source bytes off the app tier — regardless
	// of whether a LAN stream-token maker is wired.
	const signed = "https://cdn.example/src.mkv?sig=abc"
	h := &NativeTranscodeHandler{
		store: sourceOffloadStore{url: signed},
		cfg:   &config.Config{ListenAddr: ":7070"},
	}
	got := h.buildSourceURL(context.Background(), nil, uuid.New(), "/media/movie.mkv")
	if got != signed {
		t.Errorf("got %q, want signed URL %q", got, signed)
	}
}

func TestBuildSourceURL_LocalStoreFallsThrough(t *testing.T) {
	// Local can't offload (SignedURL returns ""), and with no token maker wired
	// the result is "" — byte-for-byte the pre-media-store behaviour for a single
	// or shared-storage install. This is the single-install safety guarantee.
	h := &NativeTranscodeHandler{
		store: mediastore.Local{},
		cfg:   &config.Config{ListenAddr: ":7070"},
		// tokens deliberately nil
	}
	if got := h.buildSourceURL(context.Background(), nil, uuid.New(), "/media/movie.mkv"); got != "" {
		t.Errorf("got %q, want \"\" (local fall-through, no token maker)", got)
	}
}

func TestBuildSourceURL_NilStoreDefaultsToLocal(t *testing.T) {
	// A nil store must behave like Local (the opt-in default), not panic — so an
	// install that never calls WithMediaStore is unaffected.
	h := &NativeTranscodeHandler{
		cfg: &config.Config{ListenAddr: ":7070"},
	}
	if got := h.buildSourceURL(context.Background(), nil, uuid.New(), "/media/movie.mkv"); got != "" {
		t.Errorf("got %q, want \"\" (nil store → Local, no token maker)", got)
	}
}
