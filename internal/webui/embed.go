// Package webui embeds the built SvelteKit frontend.
// Run `npm run build` in the web/ directory before building the server binary.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// FS returns a filesystem rooted at the dist directory.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("webui: dist not embedded: " + err.Error())
	}
	return sub
}
