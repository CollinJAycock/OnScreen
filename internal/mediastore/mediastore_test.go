package mediastore

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestLocal_Walk_YieldsFilesNotDirs(t *testing.T) {
	root := t.TempDir()
	// root/a.mkv, root/sub/b.mkv
	if err := os.WriteFile(filepath.Join(root, "a.mkv"), []byte("aa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "b.mkv"), []byte("bbbb"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := map[string]int64{}
	err := Local{}.Walk(context.Background(), root, func(o ObjectInfo) error {
		got[filepath.Base(o.Key)] = o.Size
		return nil
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("yielded %d files, want 2 (dirs must not be yielded): %v", len(got), got)
	}
	if got["a.mkv"] != 2 || got["b.mkv"] != 4 {
		t.Errorf("sizes wrong: %v", got)
	}
}

func TestLocal_Walk_StopsOnCallbackError(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"1", "2", "3"} {
		if err := os.WriteFile(filepath.Join(root, n+".mkv"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stop := errors.New("stop")
	calls := 0
	err := Local{}.Walk(context.Background(), root, func(ObjectInfo) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) {
		t.Errorf("got %v, want the callback's error propagated", err)
	}
	if calls != 1 {
		t.Errorf("callback ran %d times, want 1 (walk should stop on error)", calls)
	}
}

func TestLocal_Remap_ResolvesToLocalMount(t *testing.T) {
	// Multi-site DR: a replicated DB carries the primary's absolute path; the
	// standby's Local remaps it onto its own mount.
	dir := t.TempDir()
	real := filepath.Join(dir, "Movies", "x.mkv")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}

	l := Local{Remap: []PathMapping{{From: "/primary/media", To: dir}}}
	key := "/primary/media/Movies/x.mkv" // does not exist locally; only via remap

	fi, err := l.Stat(context.Background(), key)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Size != 4 {
		t.Errorf("size = %d, want 4", fi.Size)
	}

	f, err := l.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "body" {
		t.Errorf("read %q, want body", b)
	}
}

func TestLocal_Remap_NoMatchPassesThrough(t *testing.T) {
	// A key with no matching prefix is used as-is — the default behaviour, so a
	// remap configured for DR can't break unrelated paths.
	l := Local{Remap: []PathMapping{{From: "/primary", To: "/standby"}}}
	if _, err := l.Stat(context.Background(), filepath.Join(t.TempDir(), "missing.mkv")); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound (no remap match → key as-is)", err)
	}
}

func TestLocal_Put_WritesAndCreatesDirs(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "sub", "deeper", "poster.jpg") // parent dirs don't exist yet
	if err := (Local{}).Put(context.Background(), key, []byte("art-bytes")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Read it back through the store.
	f, err := Local{}.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("Open after Put: %v", err)
	}
	defer f.Close()
	got, _ := io.ReadAll(f)
	if string(got) != "art-bytes" {
		t.Errorf("read %q, want art-bytes", got)
	}
	// No leftover temp files in the directory.
	entries, _ := os.ReadDir(filepath.Dir(key))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".mediastore-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestLocal_Put_HonoursRemap(t *testing.T) {
	dir := t.TempDir()
	l := Local{Remap: []PathMapping{{From: "/primary/media", To: dir}}}
	if err := l.Put(context.Background(), "/primary/media/x.jpg", []byte("z")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Landed at the remapped location.
	if _, err := os.Stat(filepath.Join(dir, "x.jpg")); err != nil {
		t.Errorf("Put did not write to the remapped path: %v", err)
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
