package v1

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/trickplay"
)

// writeBIFFixture lays down what a finished generation leaves on disk: one
// 2x2 sprite sheet of 64x36 thumbs and the VTT indexing 3 of its cells.
func writeBIFFixture(t *testing.T, dir string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 128, 72))
	for y := 0; y < 72; y++ {
		for x := 0; x < 128; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 2), uint8(y * 3), 90, 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sprite_000.jpg"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := trickplay.Spec{IntervalSec: 10, ThumbWidth: 64, ThumbHeight: 36, GridCols: 2, GridRows: 2}
	vtt, err := spec.WriteVTT(30, []string{"sprite_000.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.vtt"), []byte(vtt), 0o644); err != nil {
		t.Fatal(err)
	}
}

var bifMagicPrefix = []byte{0x89, 0x42, 0x49, 0x46, 0x0d, 0x0a, 0x1a, 0x0a}

func TestTrickplay_ServeBIF_NilService(t *testing.T) {
	h := NewTrickplayHandler(nil, &fakeTrickplayMedia{}, silentLogger())
	rec := httptest.NewRecorder()
	h.ServeBIF(rec, tpReq(http.MethodGet, "/", nil, uuid.New().String(), ""))
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestTrickplay_ServeBIF_BadUUID(t *testing.T) {
	h := NewTrickplayHandler(&fakeTrickplayService{itemDir: t.TempDir()}, &fakeTrickplayMedia{item: tpItem()}, silentLogger())
	rec := httptest.NewRecorder()
	h.ServeBIF(rec, tpReq(http.MethodGet, "/", &auth.Claims{UserID: uuid.New()}, "nope", ""))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

// No sprites yet (or a generation in progress, which writes the VTT last).
func TestTrickplay_ServeBIF_NotGeneratedIs404(t *testing.T) {
	h := NewTrickplayHandler(&fakeTrickplayService{itemDir: t.TempDir()}, &fakeTrickplayMedia{}, silentLogger())
	rec := httptest.NewRecorder()
	h.ServeBIF(rec, tpReq(http.MethodGet, "/", nil, uuid.New().String(), ""))
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestTrickplay_ServeBIF_BuildsAndServes(t *testing.T) {
	dir := t.TempDir()
	writeBIFFixture(t, dir)
	h := NewTrickplayHandler(&fakeTrickplayService{itemDir: dir}, &fakeTrickplayMedia{}, silentLogger())
	rec := httptest.NewRecorder()
	h.ServeBIF(rec, tpReq(http.MethodGet, "/", nil, uuid.New().String(), ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, bifMagicPrefix) {
		t.Fatalf("body is not a BIF: % x", body[:min(len(body), 16)])
	}
	if n := binary.LittleEndian.Uint32(body[12:]); n != 3 {
		t.Errorf("images = %d, want 3", n)
	}
	if m := binary.LittleEndian.Uint32(body[16:]); m != 10000 {
		t.Errorf("multiplier = %d, want 10000", m)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=86400, must-revalidate" {
		t.Errorf("Cache-Control = %q", cc)
	}
	cached, err := os.ReadFile(filepath.Join(dir, trickplay.BIFFileName))
	if err != nil {
		t.Fatalf("bif not cached: %v", err)
	}
	if !bytes.Equal(cached, body) {
		t.Error("served bytes differ from the cached file")
	}
}

// Library ACL and rating ceiling apply before anything is built.
func TestTrickplay_ServeBIF_Gates(t *testing.T) {
	cases := []struct {
		name   string
		allow  bool
		rating string
		claims *auth.Claims
	}{
		{"no library grant", false, "PG", &auth.Claims{UserID: uuid.New()}},
		{"over the rating ceiling", true, "R", &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG-13"}},
		{"no claims", true, "PG", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeBIFFixture(t, dir)
			item := tpItem()
			item.ContentRating = &c.rating
			h := NewTrickplayHandler(&fakeTrickplayService{itemDir: dir}, &fakeTrickplayMedia{item: item}, silentLogger()).
				WithLibraryAccess(&fakeTrickplayAccess{allow: c.allow})
			rec := httptest.NewRecorder()
			h.ServeBIF(rec, tpReq(http.MethodGet, "/", c.claims, item.ID.String(), ""))
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
				t.Fatalf("got %d, want 404/403", rec.Code)
			}
			if _, err := os.Stat(filepath.Join(dir, trickplay.BIFFileName)); err == nil {
				t.Error("a refused request built the bif")
			}
		})
	}

	// The granted, under-ceiling caller gets it.
	dir := t.TempDir()
	writeBIFFixture(t, dir)
	item := tpItem()
	pg := "PG"
	item.ContentRating = &pg
	h := NewTrickplayHandler(&fakeTrickplayService{itemDir: dir}, &fakeTrickplayMedia{item: item}, silentLogger()).
		WithLibraryAccess(&fakeTrickplayAccess{allow: true})
	rec := httptest.NewRecorder()
	h.ServeBIF(rec, tpReq(http.MethodGet, "/", &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG-13"}, item.ID.String(), ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("granted caller got %d, want 200", rec.Code)
	}
}

// Concurrent first requests share one build and all get the same bytes.
func TestTrickplay_ServeBIF_ConcurrentFirstRequests(t *testing.T) {
	dir := t.TempDir()
	writeBIFFixture(t, dir)
	h := NewTrickplayHandler(&fakeTrickplayService{itemDir: dir}, &fakeTrickplayMedia{}, silentLogger())
	id := uuid.New().String()

	const n = 8
	bodies := make([][]byte, n)
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeBIF(rec, tpReq(http.MethodGet, "/", nil, id, ""))
			codes[i], bodies[i] = rec.Code, rec.Body.Bytes()
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if codes[i] != http.StatusOK {
			t.Fatalf("request %d: %d", i, codes[i])
		}
		if !bytes.Equal(bodies[i], bodies[0]) {
			t.Fatalf("request %d got different bytes", i)
		}
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".index-*"))
	if len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

// A damaged sprite is a server-side fault, not "not generated".
func TestTrickplay_ServeBIF_CorruptSpriteIs500(t *testing.T) {
	dir := t.TempDir()
	writeBIFFixture(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "sprite_000.jpg"), []byte("not a jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := NewTrickplayHandler(&fakeTrickplayService{itemDir: dir}, &fakeTrickplayMedia{}, silentLogger())
	rec := httptest.NewRecorder()
	h.ServeBIF(rec, tpReq(http.MethodGet, "/", nil, uuid.New().String(), ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rec.Code)
	}
}
