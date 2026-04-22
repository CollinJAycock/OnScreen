package artwork

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// makeJPEG returns a tiny w×h JPEG (solid red) for use as test artwork.
func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func TestDownloadPoster_WritesFileAndNormalizesPath(t *testing.T) {
	body := makeJPEG(t, 10, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	m := New(t.TempDir())

	got, err := m.DownloadPoster(context.Background(), uuid.New(), srv.URL, dir)
	if err != nil {
		t.Fatalf("DownloadPoster: %v", err)
	}
	want := strings.ReplaceAll(filepath.Join(dir, "poster.jpg"), `\`, "/")
	if got != want {
		t.Errorf("returned path: got %q, want %q (forward slashes always)", got, want)
	}
	written, err := os.ReadFile(filepath.Join(dir, "poster.jpg"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(written, body) {
		t.Errorf("file content mismatch: got %d bytes, want %d", len(written), len(body))
	}
}

func TestDownload_SkipsRedownloadWhenFileExists(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(makeJPEG(t, 5, 5))
	}))
	defer srv.Close()

	dir := t.TempDir()
	m := New(t.TempDir())

	if _, err := m.DownloadPoster(context.Background(), uuid.New(), srv.URL, dir); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := m.DownloadPoster(context.Background(), uuid.New(), srv.URL, dir); err != nil {
		t.Fatalf("second: %v", err)
	}
	if hits != 1 {
		t.Errorf("HTTP hits: got %d, want 1 (cache hit on second call)", hits)
	}
}

func TestReplacePoster_OverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	posterPath := filepath.Join(dir, "poster.jpg")
	stale := []byte("stale-content")
	if err := os.WriteFile(posterPath, stale, 0o644); err != nil {
		t.Fatal(err)
	}

	fresh := makeJPEG(t, 8, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fresh)
	}))
	defer srv.Close()

	m := New(t.TempDir())
	if _, err := m.ReplacePoster(context.Background(), uuid.New(), srv.URL, dir); err != nil {
		t.Fatalf("ReplacePoster: %v", err)
	}
	got, _ := os.ReadFile(posterPath)
	if bytes.Equal(got, stale) {
		t.Errorf("file still has stale content; ReplacePoster should overwrite when force=true")
	}
	if !bytes.Equal(got, fresh) {
		t.Errorf("file content mismatch after replace")
	}
}

func TestDownload_FailsOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	m := New(t.TempDir())
	if _, err := m.DownloadPoster(context.Background(), uuid.New(), srv.URL, dir); err == nil {
		t.Errorf("expected error on 404, got nil")
	}
	// File must NOT have been created (atomic temp+rename means temp is cleaned).
	if _, err := os.Stat(filepath.Join(dir, "poster.jpg")); !os.IsNotExist(err) {
		t.Errorf("poster.jpg should not exist after failed download")
	}
	// Temp files should also be cleaned up.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".artwork-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestDownload_RejectsNonHTTPSchemes(t *testing.T) {
	// SSRF surface: file:// and other schemes must NOT be honoured. Go's
	// http.Client only dials registered transports, so file:// returns an
	// error rather than reading from disk — but we still want a regression
	// test because losing this default would silently re-introduce SSRF.
	dir := t.TempDir()
	tmp := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(tmp, []byte("secret"), 0o644)

	m := New(t.TempDir())
	url := "file://" + filepath.ToSlash(tmp)
	_, err := m.DownloadPoster(context.Background(), uuid.New(), url, dir)
	if err == nil {
		t.Fatal("expected error for file:// URL — SSRF guard regression")
	}
	if _, err := os.Stat(filepath.Join(dir, "poster.jpg")); !os.IsNotExist(err) {
		t.Errorf("poster.jpg should not exist after rejected scheme")
	}
}

func TestDownload_50MBCap(t *testing.T) {
	// Server claims to serve 60MB but io.LimitReader caps at 50MB. The
	// resulting file should be exactly 50MB, not the full upstream length.
	const cap = 50 * 1024 * 1024
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stream zeros to keep memory low; LimitReader will stop reading.
		w.Header().Set("Content-Type", "image/jpeg")
		buf := make([]byte, 64*1024)
		written := 0
		for written < cap+1024*1024 {
			n, _ := w.Write(buf)
			if n == 0 {
				return
			}
			written += n
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	m := New(t.TempDir())
	if _, err := m.DownloadPoster(context.Background(), uuid.New(), srv.URL, dir); err != nil {
		t.Fatalf("DownloadPoster: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "poster.jpg"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() > cap {
		t.Errorf("file size %d exceeds 50MB cap", info.Size())
	}
}

func TestDownloadArtistPoster_UsesIDInFilename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(makeJPEG(t, 4, 4))
	}))
	defer srv.Close()

	dir := t.TempDir()
	id := uuid.New()
	m := New(t.TempDir())
	got, err := m.DownloadArtistPoster(context.Background(), id, srv.URL, dir)
	if err != nil {
		t.Fatalf("DownloadArtistPoster: %v", err)
	}
	if !strings.Contains(got, id.String()+"-poster.jpg") {
		t.Errorf("artist poster filename: got %q, want suffix %q (collision-safe naming)", got, id.String()+"-poster.jpg")
	}
}

func TestResize_PreservesAspectRatio(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.jpg")
	if err := os.WriteFile(src, makeJPEG(t, 200, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(t.TempDir())

	var buf bytes.Buffer
	if err := m.Resize(context.Background(), &buf, src, 100, 100); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	got, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatalf("decode resized: %v", err)
	}
	b := got.Bounds()
	// Aspect 2:1 fit into 100x100 → 100×50.
	if b.Dx() != 100 || b.Dy() != 50 {
		t.Errorf("dimensions: got %dx%d, want 100x50 (preserve aspect)", b.Dx(), b.Dy())
	}
}

func TestResize_AcceptsPNGSource(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.png")
	if err := os.WriteFile(src, makePNG(t, 50, 50), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(t.TempDir())
	var buf bytes.Buffer
	if err := m.Resize(context.Background(), &buf, src, 25, 25); err != nil {
		t.Fatalf("Resize png: %v", err)
	}
	if buf.Len() == 0 {
		t.Errorf("empty output")
	}
}

func TestResize_WritesAndReusesCache(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.jpg")
	if err := os.WriteFile(src, makeJPEG(t, 100, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	m := New(cacheDir)

	// First call populates the cache.
	var buf bytes.Buffer
	if err := m.Resize(context.Background(), &buf, src, 50, 50); err != nil {
		t.Fatalf("Resize 1: %v", err)
	}
	first := buf.Bytes()

	entries, _ := os.ReadDir(cacheDir)
	if len(entries) != 1 {
		t.Fatalf("cache should have 1 entry, got %d", len(entries))
	}
	cachedPath := filepath.Join(cacheDir, entries[0].Name())

	// Tamper with the cached file so we can detect whether the second call
	// re-reads the cache or re-renders from source.
	marker := []byte("CACHED-NOT-RERENDERED")
	if err := os.WriteFile(cachedPath, marker, 0o644); err != nil {
		t.Fatal(err)
	}
	// Bump cache mtime well into the future so isCacheValid passes regardless
	// of fs mtime granularity.
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(cachedPath, future, future)

	var buf2 bytes.Buffer
	if err := m.Resize(context.Background(), &buf2, src, 50, 50); err != nil {
		t.Fatalf("Resize 2: %v", err)
	}
	if !bytes.Equal(buf2.Bytes(), marker) {
		t.Errorf("second call should serve cached bytes; got %d bytes (first was %d)", buf2.Len(), len(first))
	}
}

func TestResize_InvalidatesCacheWhenSourceNewer(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.jpg")
	if err := os.WriteFile(src, makeJPEG(t, 100, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	m := New(cacheDir)

	if err := m.Resize(context.Background(), &bytes.Buffer{}, src, 30, 30); err != nil {
		t.Fatalf("Resize 1: %v", err)
	}
	entries, _ := os.ReadDir(cacheDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 cache entry, got %d", len(entries))
	}
	cachedPath := filepath.Join(cacheDir, entries[0].Name())
	// Stamp cache in the past, source in the future → source is "newer".
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(cachedPath, past, past)
	_ = os.Chtimes(src, future, future)

	// Replace the cache with a sentinel — if Resize uses it, sentinel comes back.
	sentinel := []byte("STALE")
	_ = os.WriteFile(cachedPath, sentinel, 0o644)
	_ = os.Chtimes(cachedPath, past, past)

	var buf bytes.Buffer
	if err := m.Resize(context.Background(), &buf, src, 30, 30); err != nil {
		t.Fatalf("Resize 2: %v", err)
	}
	if bytes.Equal(buf.Bytes(), sentinel) {
		t.Errorf("stale cache served; should re-render when source is newer than cache")
	}
}

func TestResize_MissingSourceReturnsError(t *testing.T) {
	m := New(t.TempDir())
	err := m.Resize(context.Background(), &bytes.Buffer{}, filepath.Join(t.TempDir(), "nope.jpg"), 50, 50)
	if err == nil {
		t.Errorf("expected error for missing source")
	}
}

func TestCacheKeyFor_StableAndDimensionSensitive(t *testing.T) {
	a := cacheKeyFor("/a/b.jpg", 100, 100)
	b := cacheKeyFor("/a/b.jpg", 100, 100)
	if a != b {
		t.Errorf("cacheKeyFor not deterministic: %q vs %q", a, b)
	}
	if cacheKeyFor("/a/b.jpg", 100, 200) == a {
		t.Errorf("dimensions must affect cache key")
	}
	if cacheKeyFor("/a/c.jpg", 100, 100) == a {
		t.Errorf("path must affect cache key")
	}
}

func TestResize_UnconstrainedReturnsOriginalDimensions(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src.jpg")
	if err := os.WriteFile(src, makeJPEG(t, 80, 60), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(t.TempDir())
	var buf bytes.Buffer
	if err := m.Resize(context.Background(), &buf, src, 0, 0); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	got, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Bounds().Dx() != 80 || got.Bounds().Dy() != 60 {
		t.Errorf("unconstrained Resize changed dimensions: %dx%d", got.Bounds().Dx(), got.Bounds().Dy())
	}
}
