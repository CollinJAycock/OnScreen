package api

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/artwork"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/valkey"
)

// newTestRouter builds the real router with just enough wired for the auth
// group and the /artwork route; everything else is nil and never reached.
func newTestRouter(t *testing.T, artDir string) (http.Handler, *valkey.RateLimiter, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tm, err := auth.NewTokenMaker(auth.DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	vc, err := valkey.New(context.Background(), "redis://"+mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vc.Close() })
	rl := valkey.NewRateLimiter(vc, logger, nil)

	h := &Handlers{
		Auth:        v1.NewAuthHandler(nil, logger),
		Logger:      logger,
		Auth_mw:     middleware.NewAuthenticator(tm),
		RateLimiter: rl,
		Artwork:     artwork.New(t.TempDir()),
		ArtworkRoots: func() []ArtworkRoot {
			return []ArtworkRoot{{LibraryID: uuid.New(), Paths: []string{artDir}}}
		},
	}
	tok, err := tm.IssueAccessToken(auth.Claims{UserID: uuid.New(), Username: "alice", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	return NewRouter(h), rl, tok
}

// Logout must not share AuthLimit's brute-force bucket: once a household's
// logins/refreshes spent it, a 429 on logout skipped the server-side revoke
// while the SPA showed "signed out".
func TestRouter_LogoutNotInAuthLimitBucket(t *testing.T) {
	router, rl, _ := newTestRouter(t, t.TempDir())
	const ip = "203.0.113.9"
	for i := 0; i < middleware.AuthLimit.Limit; i++ {
		if _, _, _, err := rl.Allow(context.Background(), "ratelimit:auth:"+ip, middleware.AuthLimit.Limit, middleware.AuthLimit.Window); err != nil {
			t.Fatal(err)
		}
	}

	post := func(path string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
		req.RemoteAddr = ip + ":40000"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	// Sanity: the bucket really is spent for the brute-forceable routes.
	if code := post("/api/v1/auth/login"); code != http.StatusTooManyRequests {
		t.Fatalf("login with a spent auth bucket = %d, want 429 (test setup is wrong)", code)
	}
	if code := post("/api/v1/auth/logout"); code != http.StatusNoContent {
		t.Fatalf("logout with a spent auth bucket = %d, want 204", code)
	}
}

func artworkGet(t *testing.T, router http.Handler, tok, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// A resize that fails before writing (undecodable image) used to go out as
// 200 + empty body + a week-long immutable Cache-Control.
func TestRouter_ArtworkResizeFailureIsUncached404(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "poster.tbn"), []byte("<html>not an image</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	router, _, tok := newTestRouter(t, dir)

	rec := artworkGet(t, router, tok, "/artwork/poster.tbn?w=200")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	if ct := rec.Header().Get("Content-Type"); strings.HasPrefix(ct, "image/") {
		t.Fatalf("error response labelled %q", ct)
	}
}

func TestRouter_ArtworkResizeSuccessKeepsLongCache(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 64)), nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "poster.jpg"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	router, _, tok := newTestRouter(t, dir)

	rec := artworkGet(t, router, tok, "/artwork/poster.jpg?w=200")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=604800, immutable" {
		t.Fatalf("Cache-Control = %q", cc)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("empty body")
	}
}

// Headers are committed with the first body byte; a failure after that leaves
// the response alone (its status line is already out).
func TestResizedArtworkWriter_FailAfterWriteIsNoop(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &resizedArtworkWriter{w: rec, cacheControl: "public, max-age=604800, immutable", vary: true}
	if _, err := rw.Write([]byte{0xff, 0xd8}); err != nil {
		t.Fatal(err)
	}
	rw.fail(httptest.NewRequest(http.MethodGet, "/artwork/x.jpg", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "public, max-age=604800, immutable" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if v := rec.Header().Values("Vary"); len(v) != 2 {
		t.Fatalf("Vary = %v, want Authorization + Cookie", v)
	}
}
