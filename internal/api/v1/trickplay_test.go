package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/trickplay"
)

// ── fakes ────────────────────────────────────────────────────────────────────

type fakeTrickplayService struct {
	statusSpec  trickplay.Spec
	statusKind  string
	statusCount int
	statusOK    bool
	statusErr   error

	generated atomic.Int32
	generate  func(ctx context.Context, id uuid.UUID) error

	itemDir string
}

func (f *fakeTrickplayService) Status(_ context.Context, _ uuid.UUID) (trickplay.Spec, string, int, bool, error) {
	return f.statusSpec, f.statusKind, f.statusCount, f.statusOK, f.statusErr
}
func (f *fakeTrickplayService) Generate(ctx context.Context, id uuid.UUID) error {
	f.generated.Add(1)
	if f.generate != nil {
		return f.generate(ctx, id)
	}
	return nil
}
func (f *fakeTrickplayService) ItemDir(_ uuid.UUID) string { return f.itemDir }

type fakeTrickplayMedia struct {
	item *media.Item
	err  error
}

func (f *fakeTrickplayMedia) GetItem(_ context.Context, _ uuid.UUID) (*media.Item, error) {
	return f.item, f.err
}

type fakeTrickplayAccess struct {
	allow  bool
	err    error
	called bool
}

func (f *fakeTrickplayAccess) CanAccessLibrary(_ context.Context, _, _ uuid.UUID, _ bool) (bool, error) {
	f.called = true
	return f.allow, f.err
}
func (f *fakeTrickplayAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, _ bool) (map[uuid.UUID]struct{}, error) {
	return nil, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func tpReq(method, url string, claims *auth.Claims, idParam, fileParam string) *http.Request {
	req := httptest.NewRequest(method, url, nil)
	if claims != nil {
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	}
	rctx := chi.NewRouteContext()
	if idParam != "" {
		rctx.URLParams.Add("id", idParam)
	}
	if fileParam != "" {
		rctx.URLParams.Add("file", fileParam)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func tpItem() *media.Item {
	return &media.Item{ID: uuid.New(), LibraryID: uuid.New()}
}

func silentLogger() *slog.Logger { return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1})) }

// ── Status ───────────────────────────────────────────────────────────────────

func TestTrickplay_Status_NilServiceReturns404(t *testing.T) {
	h := NewTrickplayHandler(nil, &fakeTrickplayMedia{}, silentLogger())
	req := tpReq(http.MethodGet, "/", &auth.Claims{UserID: uuid.New()}, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Status(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestTrickplay_Status_BadUUID(t *testing.T) {
	svc := &fakeTrickplayService{}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{item: tpItem()}, silentLogger())
	req := tpReq(http.MethodGet, "/", &auth.Claims{UserID: uuid.New()}, "not-a-uuid", "")
	rec := httptest.NewRecorder()
	h.Status(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestTrickplay_Status_NotFoundItem(t *testing.T) {
	svc := &fakeTrickplayService{}
	mlk := &fakeTrickplayMedia{err: media.ErrNotFound}
	h := NewTrickplayHandler(svc, mlk, silentLogger())
	req := tpReq(http.MethodGet, "/", &auth.Claims{UserID: uuid.New()}, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Status(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestTrickplay_Status_NotStarted_NoRow(t *testing.T) {
	svc := &fakeTrickplayService{statusOK: false}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{item: tpItem()}, silentLogger())
	req := tpReq(http.MethodGet, "/", &auth.Claims{UserID: uuid.New()}, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Status(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out TrickplayStatusJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "not_started" {
		t.Errorf("status = %q, want not_started", out.Status)
	}
	if out.SpriteCount != 0 || out.IntervalSec != 0 {
		t.Errorf("non-zero spec returned for absent row: %+v", out)
	}
}

func TestTrickplay_Status_DoneIncludesSpec(t *testing.T) {
	svc := &fakeTrickplayService{
		statusKind:  "done",
		statusCount: 6,
		statusOK:    true,
		statusSpec:  trickplay.Spec{IntervalSec: 10, ThumbWidth: 320, ThumbHeight: 180, GridCols: 10, GridRows: 10},
	}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{item: tpItem()}, silentLogger())
	req := tpReq(http.MethodGet, "/", &auth.Claims{UserID: uuid.New()}, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Status(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	var out TrickplayStatusJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "done" || out.SpriteCount != 6 ||
		out.IntervalSec != 10 || out.ThumbWidth != 320 || out.ThumbHeight != 180 {
		t.Errorf("unexpected response: %+v", out)
	}
}

func TestTrickplay_Status_PropagatesServiceError(t *testing.T) {
	svc := &fakeTrickplayService{statusErr: errors.New("db boom")}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{item: tpItem()}, silentLogger())
	req := tpReq(http.MethodGet, "/", &auth.Claims{UserID: uuid.New()}, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Status(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rec.Code)
	}
}

func TestTrickplay_Status_AccessDeniedReturns404(t *testing.T) {
	svc := &fakeTrickplayService{statusOK: true, statusKind: "done"}
	access := &fakeTrickplayAccess{allow: false}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{item: tpItem()}, silentLogger()).
		WithLibraryAccess(access)
	req := tpReq(http.MethodGet, "/", &auth.Claims{UserID: uuid.New()}, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Status(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404 (deny obfuscated as not-found)", rec.Code)
	}
	if !access.called {
		t.Error("access checker was not invoked")
	}
}

func TestTrickplay_Status_AccessRequiresClaims(t *testing.T) {
	svc := &fakeTrickplayService{}
	access := &fakeTrickplayAccess{allow: true}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{item: tpItem()}, silentLogger()).
		WithLibraryAccess(access)
	// No claims on request — should be forbidden when access checker is configured.
	req := tpReq(http.MethodGet, "/", nil, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Status(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rec.Code)
	}
}

// ── Generate ─────────────────────────────────────────────────────────────────

func TestTrickplay_Generate_RequiresAdmin(t *testing.T) {
	svc := &fakeTrickplayService{}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{item: tpItem()}, silentLogger())
	req := tpReq(http.MethodPost, "/", &auth.Claims{UserID: uuid.New(), IsAdmin: false}, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Generate(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rec.Code)
	}
	if svc.generated.Load() != 0 {
		t.Error("Generate must not be invoked for non-admin")
	}
}

func TestTrickplay_Generate_NoClaimsForbidden(t *testing.T) {
	svc := &fakeTrickplayService{}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{item: tpItem()}, silentLogger())
	req := tpReq(http.MethodPost, "/", nil, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Generate(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("got %d, want 403", rec.Code)
	}
}

func TestTrickplay_Generate_NilServiceReturns404(t *testing.T) {
	h := NewTrickplayHandler(nil, &fakeTrickplayMedia{}, silentLogger())
	req := tpReq(http.MethodPost, "/", &auth.Claims{IsAdmin: true}, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Generate(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestTrickplay_Generate_AdminReturns202AndFiresGoroutine(t *testing.T) {
	done := make(chan struct{}, 1)
	svc := &fakeTrickplayService{
		generate: func(_ context.Context, _ uuid.UUID) error {
			done <- struct{}{}
			return nil
		},
	}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{item: tpItem()}, silentLogger())
	req := tpReq(http.MethodPost, "/", &auth.Claims{UserID: uuid.New(), IsAdmin: true}, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Generate(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var out TrickplayStatusJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "pending" {
		t.Errorf("status = %q, want pending", out.Status)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Generate goroutine did not fire within 1s")
	}
	if svc.generated.Load() != 1 {
		t.Errorf("generated count = %d, want 1", svc.generated.Load())
	}
}

func TestTrickplay_Generate_DetachedFromRequestContext(t *testing.T) {
	// Cancelling the request context must NOT cancel the generation — the
	// handler runs Generate on context.Background() in a goroutine.
	started := make(chan struct{})
	release := make(chan struct{})
	got := make(chan error, 1)
	svc := &fakeTrickplayService{
		generate: func(ctx context.Context, _ uuid.UUID) error {
			close(started)
			<-release
			got <- ctx.Err()
			return nil
		},
	}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{item: tpItem()}, silentLogger())

	ctx, cancel := context.WithCancel(context.Background())
	req := tpReq(http.MethodPost, "/", &auth.Claims{UserID: uuid.New(), IsAdmin: true}, uuid.New().String(), "")
	req = req.WithContext(middleware.WithClaims(ctx, &auth.Claims{UserID: uuid.New(), IsAdmin: true}))
	// Re-attach chi route params after replacing context.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuid.New().String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.Generate(rec, req)

	<-started
	cancel()
	close(release)
	select {
	case err := <-got:
		if err != nil {
			t.Errorf("Generate ctx was cancelled when request ctx died: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Generate did not return within 1s")
	}
}

// ── ServeFile ────────────────────────────────────────────────────────────────

func TestTrickplay_ServeFile_NilService(t *testing.T) {
	h := NewTrickplayHandler(nil, &fakeTrickplayMedia{}, silentLogger())
	req := tpReq(http.MethodGet, "/", nil, uuid.New().String(), "index.vtt")
	rec := httptest.NewRecorder()
	h.ServeFile(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestTrickplay_ServeFile_BadUUID(t *testing.T) {
	// ServeFile now goes through requireItemAccess (same path Status and
	// Generate use), which surfaces a 400 for malformed UUIDs to match
	// the rest of the trickplay handlers.
	svc := &fakeTrickplayService{itemDir: t.TempDir()}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{}, silentLogger())
	req := tpReq(http.MethodGet, "/", &auth.Claims{UserID: uuid.New()}, "not-a-uuid", "index.vtt")
	rec := httptest.NewRecorder()
	h.ServeFile(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestTrickplay_ServeFile_RejectsBadFilenames(t *testing.T) {
	svc := &fakeTrickplayService{itemDir: t.TempDir()}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{}, silentLogger())
	bad := []string{
		"../etc/passwd",
		"sprite_1.jpg",         // need 3-digit zero-padded
		"sprite_0001.jpg",      // 4 digits, not 3
		"index.vtt.bak",
		"INDEX.VTT",            // case-sensitive whitelist
		"sprite_abc.jpg",
		"random.txt",
		"",
	}
	for _, name := range bad {
		req := tpReq(http.MethodGet, "/", nil, uuid.New().String(), name)
		rec := httptest.NewRecorder()
		h.ServeFile(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("filename %q: got %d, want 404", name, rec.Code)
		}
	}
}

func TestTrickplay_ServeFile_MissingOnDisk(t *testing.T) {
	svc := &fakeTrickplayService{itemDir: t.TempDir()} // empty dir
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{}, silentLogger())
	req := tpReq(http.MethodGet, "/", nil, uuid.New().String(), "index.vtt")
	rec := httptest.NewRecorder()
	h.ServeFile(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestTrickplay_ServeFile_ServesVTTWithCorrectHeaders(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.vtt"), []byte("WEBVTT\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := &fakeTrickplayService{itemDir: dir}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{}, silentLogger())
	req := tpReq(http.MethodGet, "/", nil, uuid.New().String(), "index.vtt")
	rec := httptest.NewRecorder()
	h.ServeFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/vtt; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/vtt; charset=utf-8", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc == "" {
		t.Error("expected Cache-Control header")
	}
	if rec.Body.String() != "WEBVTT\n\n" {
		t.Errorf("body = %q, want WEBVTT prelude", rec.Body.String())
	}
}

func TestTrickplay_ServeFile_ServesSprite(t *testing.T) {
	dir := t.TempDir()
	body := []byte{0xff, 0xd8, 0xff, 0xe0} // JPEG magic prefix
	if err := os.WriteFile(filepath.Join(dir, "sprite_007.jpg"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	svc := &fakeTrickplayService{itemDir: dir}
	h := NewTrickplayHandler(svc, &fakeTrickplayMedia{}, silentLogger())
	req := tpReq(http.MethodGet, "/", nil, uuid.New().String(), "sprite_007.jpg")
	rec := httptest.NewRecorder()
	h.ServeFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if rec.Header().Get("Cache-Control") == "" {
		t.Error("expected Cache-Control header on sprite")
	}
	if rec.Body.Len() != len(body) {
		t.Errorf("served %d bytes, wrote %d", rec.Body.Len(), len(body))
	}
}
