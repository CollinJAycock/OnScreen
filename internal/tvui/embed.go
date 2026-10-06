// Package tvui embeds the built TV web app (clients/xbox) that the server
// serves at /tv/. The Xbox shell loads <server>/tv/index.html in a WebView2,
// so the app runs same-origin with the API.
//
// `make tvui` (and each build script that also builds web/) runs the
// clients/xbox build and copies its build/ output into dist/ before the Go
// build.
// Unlike internal/webui, a missing TV build is not fatal: only dist/.gitkeep
// is committed, so a checkout that never built the TV app still compiles and
// /tv/ serves placeholder/index.html instead.
package tvui

import (
	"embed"
	"io/fs"
)

// all: so dist/.gitkeep counts as embeddable content — without it a checkout
// with no TV build would have an "empty" dist and fail to compile.
//
//go:embed all:dist
var distFS embed.FS

//go:embed placeholder/index.html
var placeholderFS embed.FS

// FS returns the TV app's filesystem and true when a real build was embedded.
// A build is present exactly when dist/index.html exists: the committed dist/
// holds only .gitkeep, and the build's output always has an index.html. When
// it is absent FS returns the placeholder page's filesystem and false.
//
// The placeholder lives outside dist/ on purpose: a committed file inside
// dist/ would be overwritten by every TV build and show up as a modified
// (and very large) file in `git status`.
func FS() (fs.FS, bool) {
	if dist, err := fs.Sub(distFS, "dist"); err == nil {
		if _, err := fs.Stat(dist, "index.html"); err == nil {
			return dist, true
		}
	}
	// fs.Sub only fails on an invalid path, and this one is a literal.
	ph, _ := fs.Sub(placeholderFS, "placeholder")
	return ph, false
}
