package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/settings"
)

type fixedLibraryRoots []string

func (f fixedLibraryRoots) LibraryRootPaths(context.Context) ([]string, error) { return f, nil }

func putStorage(t *testing.T, h *SettingsHandler, mappings map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"enabled": false, "backend": "local", "path_mappings": mappings})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.UpdateStorage(rec, httptest.NewRequest(http.MethodPut, "/settings/storage", strings.NewReader(string(body))))
	return rec
}

// S6: storage path mappings are validated on save — folder to folder, both
// absolute, no "..", and From tied to a library root — and stored normalised.
// A literal prefix map let an admin session read any file on the server
// (map one item's file to .env, then stream the item).
func TestUpdateStorage_ValidatesPathMappings(t *testing.T) {
	lib := t.TempDir()
	movie := filepath.Join(lib, "Film (2020)", "Film.mkv")
	if err := os.MkdirAll(filepath.Dir(movie), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(movie, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(secret, []byte("SECRET_KEY=x"), 0o600); err != nil {
		t.Fatal(err)
	}
	roots := fixedLibraryRoots{"/mnt/site-a/media/Movies", lib}

	refused := []struct {
		name     string
		mappings map[string]string
		wantKey  string
	}{
		{"one file onto another", map[string]string{movie: secret}, "is a file"},
		{"a folder onto a file", map[string]string{filepath.Dir(movie): secret}, "is a file"},
		{"outside every library", map[string]string{"/etc": "/standby/etc"}, "must be a library folder"},
		{"a sibling with a shared prefix", map[string]string{"/mnt/site-a/media/MoviesX": "/x"}, "must be a library folder"},
		{"relative from", map[string]string{"mnt/site-a/media": "/mnt/b"}, "must be absolute"},
		{"relative to", map[string]string{"/mnt/site-a/media": "standby"}, "must be absolute"},
		{"dot-dot", map[string]string{"/mnt/site-a/media/Movies/../..": "/mnt/b"}, `".."`},
		{"dot-dot in to", map[string]string{"/mnt/site-a/media": "/mnt/b/../../etc"}, `".."`},
		{"the root", map[string]string{"/": "/mnt/b"}, "root of the filesystem"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mockSettingsService{storage: settings.StorageConfig{PathMappings: map[string]string{"/keep": "/me"}}}
			h := newStorageHandler(svc).SetStorageLibraryRoots(roots)
			rec := putStorage(t, h, tc.mappings)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			code, msg := errorCode(t, rec)
			if code != "PATH_MAPPING_INVALID" || !strings.Contains(msg, tc.wantKey) {
				t.Errorf("error = %s %q, want PATH_MAPPING_INVALID mentioning %q", code, msg, tc.wantKey)
			}
			if svc.storage.PathMappings["/keep"] != "/me" || len(svc.storage.PathMappings) != 1 {
				t.Errorf("settings were written on a refused save: %+v", svc.storage.PathMappings)
			}
		})
	}

	// Allowed: the DR runbook's mount above the library folders, a library
	// root itself, a folder inside one — each stored normalised.
	svc := &mockSettingsService{}
	h := newStorageHandler(svc).SetStorageLibraryRoots(roots)
	rec := putStorage(t, h, map[string]string{
		"/mnt/site-a/media/":          "/mnt/site-b/media/",
		"/mnt/site-a/media/Movies/./": "/mnt/site-b//movies",
		"/mnt/site-a/media/Movies/4K": `D:/standby\4K/`,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("valid mappings: status %d (%s)", rec.Code, rec.Body.String())
	}
	want := map[string]string{
		"/mnt/site-a/media":           "/mnt/site-b/media",
		"/mnt/site-a/media/Movies":    "/mnt/site-b/movies",
		"/mnt/site-a/media/Movies/4K": `D:\standby\4K`,
	}
	if len(svc.storage.PathMappings) != len(want) {
		t.Fatalf("stored %+v, want %+v", svc.storage.PathMappings, want)
	}
	for k, v := range want {
		if svc.storage.PathMappings[k] != v {
			t.Errorf("stored[%q] = %q, want %q", k, svc.storage.PathMappings[k], v)
		}
	}

	// No mappings is always fine, roots or not.
	h = newStorageHandler(&mockSettingsService{})
	if rec := putStorage(t, h, nil); rec.Code != http.StatusNoContent {
		t.Errorf("no mappings: status %d", rec.Code)
	}
	// Without the library roots wired no mapping can be saved (fail closed).
	if rec := putStorage(t, h, map[string]string{"/mnt/site-a/media": "/mnt/b"}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("no library roots: status %d, want 422", rec.Code)
	}
}

func TestCleanMappingPath(t *testing.T) {
	cases := []struct {
		in, want string
		root     bool
		bad      bool
	}{
		{"/mnt/media/", "/mnt/media", false, false},
		{"  //mnt//media/./x  ", "/mnt/media/x", false, false},
		{"/", "/", true, false},
		{`C:\Media\`, `C:\Media`, false, false},
		{"c:/media/tv", `c:\media\tv`, false, false},
		{`D:\`, `D:\`, true, false},
		{`\\nas\share\media`, `\\nas\share\media`, false, false},
		{`\\nas`, "", false, true},
		{"media", "", false, true},
		{"C:media", "", false, true},
		{"/a/../b", "", false, true},
		{`C:\a\..\b`, "", false, true},
		{"", "", false, true},
	}
	for _, tc := range cases {
		got, root, err := cleanMappingPath(tc.in)
		if (err != nil) != tc.bad || got != tc.want || root != tc.root {
			t.Errorf("cleanMappingPath(%q) = %q, %v, %v; want %q, %v, bad=%v", tc.in, got, root, err, tc.want, tc.root, tc.bad)
		}
	}
}

func TestPathAtOrUnder(t *testing.T) {
	for _, tc := range []struct {
		child, parent string
		want          bool
	}{
		{"/media/a", "/media/a", true},
		{"/media/a/b", "/media/a", true},
		{"/media/ab", "/media/a", false},
		{"/media", "/media/a", false},
		{"/anything", "/", true},
		{`D:\media\tv`, `D:\media`, true},
		{`D:\mediatv`, `D:\media`, false},
	} {
		if got := pathAtOrUnder(tc.child, tc.parent); got != tc.want {
			t.Errorf("pathAtOrUnder(%q, %q) = %v, want %v", tc.child, tc.parent, got, tc.want)
		}
	}
}
