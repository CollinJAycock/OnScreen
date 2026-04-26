package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fsBrowseEnvelope mirrors the {"data": {...}} shape respond.Success writes.
type fsBrowseEnvelope struct {
	Data browseResult `json:"data"`
}

func decodeFSBrowse(t *testing.T, body []byte) browseResult {
	t.Helper()
	var env fsBrowseEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	return env.Data
}

func TestFS_Browse_ListsDirectoriesOnly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A regular file should NOT appear in the response.
	if err := os.WriteFile(filepath.Join(dir, "regular.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := NewFSHandler()
	rec := httptest.NewRecorder()
	h.Browse(rec, httptest.NewRequest(http.MethodGet, "/?path="+dir, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rec.Code, rec.Body.String())
	}
	got := decodeFSBrowse(t, rec.Body.Bytes())
	if len(got.Dirs) != 3 {
		t.Errorf("dirs: got %d, want 3 (regular file leaked into result?)", len(got.Dirs))
	}
	for _, d := range got.Dirs {
		base := filepath.Base(d)
		if base == "regular.txt" {
			t.Errorf("file %q should not appear in dirs list", base)
		}
	}
}

func TestFS_Browse_HidesDotDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "visible"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}

	h := NewFSHandler()
	rec := httptest.NewRecorder()
	h.Browse(rec, httptest.NewRequest(http.MethodGet, "/?path="+dir, nil))

	got := decodeFSBrowse(t, rec.Body.Bytes())
	if len(got.Dirs) != 1 {
		t.Fatalf("dirs: got %d, want 1 (hidden dir leaked?)", len(got.Dirs))
	}
	if filepath.Base(got.Dirs[0]) != "visible" {
		t.Errorf("got %q, want \"visible\"", filepath.Base(got.Dirs[0]))
	}
}

func TestFS_Browse_SortsAlphabetically(t *testing.T) {
	dir := t.TempDir()
	// Create in reverse alphabetical order to prove the response sorts.
	for _, name := range []string{"zeta", "delta", "alpha"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	h := NewFSHandler()
	rec := httptest.NewRecorder()
	h.Browse(rec, httptest.NewRequest(http.MethodGet, "/?path="+dir, nil))

	got := decodeFSBrowse(t, rec.Body.Bytes())
	if len(got.Dirs) != 3 {
		t.Fatalf("got %d dirs, want 3", len(got.Dirs))
	}
	want := []string{"alpha", "delta", "zeta"}
	for i, d := range got.Dirs {
		if filepath.Base(d) != want[i] {
			t.Errorf("dir[%d] = %q, want %q", i, filepath.Base(d), want[i])
		}
	}
}

func TestFS_Browse_PopulatesParent(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}

	h := NewFSHandler()
	rec := httptest.NewRecorder()
	h.Browse(rec, httptest.NewRequest(http.MethodGet, "/?path="+child, nil))

	got := decodeFSBrowse(t, rec.Body.Bytes())
	wantParent := filepath.Clean(parent)
	if got.Parent != wantParent {
		t.Errorf("parent: got %q, want %q", got.Parent, wantParent)
	}
}

func TestFS_Browse_BadPathReturns400(t *testing.T) {
	h := NewFSHandler()
	rec := httptest.NewRecorder()
	h.Browse(rec, httptest.NewRequest(http.MethodGet,
		"/?path=/this/path/genuinely/does/not/exist/anywhere", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: %d, want 400 (got body=%s)", rec.Code, rec.Body.String())
	}
}

func TestFS_Browse_SetsNoStoreHeader(t *testing.T) {
	// Directory listings reveal filesystem layout — must not be cached.
	h := NewFSHandler()
	rec := httptest.NewRecorder()
	h.Browse(rec, httptest.NewRequest(http.MethodGet, "/?path="+t.TempDir(), nil))

	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want it to contain \"no-store\"", cc)
	}
}

func TestFS_Browse_EmptyPathDefaultsToRoot(t *testing.T) {
	// On Windows, "" → "/" → drive list. On Unix, "" → "/" → root listing.
	// Either way the response code is 200; we just exercise the default.
	h := NewFSHandler()
	rec := httptest.NewRecorder()
	h.Browse(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("empty path should default to root and succeed; got %d body=%s",
			rec.Code, rec.Body.String())
	}
}

func TestFS_Browse_PathCleanCollapsesDotDot(t *testing.T) {
	// filepath.Clean strips ".."; a request for /tmp/foo/../bar is treated
	// as /tmp/bar. Not a security boundary on its own (admin-only endpoint
	// can read anything readable by the process anyway), but the clean is
	// load-bearing for the Parent field — we want Parent to reflect the
	// resolved path, not the raw input.
	parent := t.TempDir()
	a := filepath.Join(parent, "a")
	b := filepath.Join(parent, "b")
	if err := os.Mkdir(a, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(b, 0o755); err != nil {
		t.Fatal(err)
	}

	h := NewFSHandler()
	rec := httptest.NewRecorder()
	rawPath := filepath.Join(a, "..", "b")
	h.Browse(rec, httptest.NewRequest(http.MethodGet, "/?path="+rawPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rec.Code, rec.Body.String())
	}
	got := decodeFSBrowse(t, rec.Body.Bytes())
	if got.Path != filepath.Clean(b) {
		t.Errorf("path: got %q, want cleaned form of %q", got.Path, b)
	}
}

func TestIsRoot_PlatformAware(t *testing.T) {
	// "/" is root on every platform; "\\" is root only on Windows.
	if !isRoot("/") {
		t.Error("\"/\" should be root on all platforms")
	}
	if runtime.GOOS == "windows" {
		if !isRoot(`\`) {
			t.Error("\"\\\\\" should be root on Windows")
		}
	}
}
