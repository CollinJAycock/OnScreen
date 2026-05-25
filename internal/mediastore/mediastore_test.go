package mediastore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLocal_OpenStat_RoundTrip(t *testing.T) {
	const body = "hello-bytes"
	p := writeTemp(t, body)
	var s Local

	fi, err := s.Stat(context.Background(), p)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Size != int64(len(body)) {
		t.Errorf("Size = %d, want %d", fi.Size, len(body))
	}
	if fi.ModTime.IsZero() {
		t.Error("ModTime is zero")
	}

	f, err := s.Open(context.Background(), p)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if string(got) != body {
		t.Errorf("read %q, want %q", got, body)
	}
}

func TestLocal_MissingKeyMapsToErrNotFound(t *testing.T) {
	var s Local
	missing := filepath.Join(t.TempDir(), "nope.mp4")

	if _, err := s.Open(context.Background(), missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("Open missing: got %v, want ErrNotFound", err)
	}
	if _, err := s.Stat(context.Background(), missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stat missing: got %v, want ErrNotFound", err)
	}
}

func TestLocal_SignedURL_EmptyMeansNoOffload(t *testing.T) {
	// The non-breaking hinge: Local can't offload, so Serve must stream through
	// the app. An empty string with a nil error is the contract callers branch on.
	u, err := Local{}.SignedURL(context.Background(), "/any/key", time.Minute)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}
	if u != "" {
		t.Errorf("got %q, want empty (local FS can't offload)", u)
	}
}

func TestServe_StreamsFullBody(t *testing.T) {
	const body = "the-whole-file"
	p := writeTemp(t, body)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/media/stream/x", nil)
	if err := Serve(rec, req, Local{}, p, "clip.mp4", time.Minute); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	got, _ := io.ReadAll(res.Body)
	if string(got) != body {
		t.Errorf("body = %q, want %q", got, body)
	}
	if ct := res.Header.Get("Content-Type"); ct == "" {
		t.Error("Content-Type not set (sniffing from name broken?)")
	}
}

func TestServe_HonoursRange(t *testing.T) {
	// Range support is the reason Serve uses ServeContent over a plain copy —
	// the desktop engine and browsers resume/seek with byte ranges.
	const body = "0123456789"
	p := writeTemp(t, body)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/media/stream/x", nil)
	req.Header.Set("Range", "bytes=2-5")
	if err := Serve(rec, req, Local{}, p, "clip.mp4", time.Minute); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	res := rec.Result()
	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", res.StatusCode)
	}
	got, _ := io.ReadAll(res.Body)
	if string(got) != "2345" {
		t.Errorf("ranged body = %q, want %q", got, "2345")
	}
}

func TestServe_MissingKeyReturnsErrNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/media/stream/x", nil)
	err := Serve(rec, req, Local{}, filepath.Join(t.TempDir(), "gone.mp4"), "gone.mp4", time.Minute)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound (so the handler can 404)", err)
	}
}

// offloadStore hands back a signed URL so Serve redirects instead of streaming.
type offloadStore struct{ url string }

func (o offloadStore) Open(context.Context, string) (io.ReadSeekCloser, error) {
	return nil, errors.New("Open must not be called when offloading")
}
func (o offloadStore) Stat(context.Context, string) (FileInfo, error) {
	return FileInfo{}, errors.New("Stat must not be called when offloading")
}
func (o offloadStore) SignedURL(context.Context, string, time.Duration) (string, error) {
	return o.url, nil
}

func TestServe_OffloadsViaRedirect(t *testing.T) {
	// When a backend can offload, Serve must 302 to the signed URL and NOT touch
	// Open/Stat (the byte path stays off the app tier).
	const signed = "https://cdn.example/clip.mp4?sig=abc"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/media/stream/x", nil)
	if err := Serve(rec, req, offloadStore{url: signed}, "key", "clip.mp4", time.Minute); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	res := rec.Result()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != signed {
		t.Errorf("Location = %q, want %q", loc, signed)
	}
}
