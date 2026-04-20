// Package webui embeds the built SvelteKit frontend.
// Run `npm run build` in the web/ directory before building the server binary.
package webui

import (
	"embed"
	"io/fs"
	"log"
)

//go:embed all:dist
var distFS embed.FS

// FS returns a filesystem rooted at the dist directory. It terminates the
// process if the embed directive did not include a dist/ — that only happens
// when the binary was built without first running the frontend build, so
// failing fast at startup is safer than serving 404s for every asset.
func FS() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		log.Fatalf("webui: dist not embedded (run `make frontend` before building): %v", err)
	}
	return sub
}
