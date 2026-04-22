package v1

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/onscreen/onscreen/internal/api/respond"
)

// FSHandler handles filesystem browsing for the path picker UI.
type FSHandler struct{}

func NewFSHandler() *FSHandler { return &FSHandler{} }

type browseResult struct {
	Path   string   `json:"path"`
	Parent string   `json:"parent"`
	Dirs   []string `json:"dirs"`
}

// Browse handles GET /api/v1/fs/browse?path=<dir>
func (h *FSHandler) Browse(w http.ResponseWriter, r *http.Request) {
	// Directory listings reveal server filesystem layout; never let a shared
	// cache or browser back-button leak them. Set before any respond.* call.
	w.Header().Set("Cache-Control", "no-store")

	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	path = normalizeInputPath(path)

	// On Windows: when path is "/" or "\", return drive roots instead of
	// trying to read "\" (which has no drive letter and will fail or surprise).
	if isRoot(path) {
		if drives := rootDirs(); drives != nil {
			respond.Success(w, r, browseResult{
				Path:   "/",
				Parent: "",
				Dirs:   drives,
			})
			return
		}
		// Unix: fall through and read "/" normally.
	}

	path = filepath.Clean(path)

	entries, err := os.ReadDir(path)
	if err != nil {
		respond.BadRequest(w, r, "cannot read directory: "+err.Error())
		return
	}

	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Skip hidden dirs
		if strings.HasPrefix(name, ".") {
			continue
		}
		dirs = append(dirs, filepath.Join(path, name))
	}
	sort.Strings(dirs)

	parent := filepath.Dir(path)
	if parent == path {
		parent = "" // already at root (e.g. C:\ on Windows)
	}

	respond.Success(w, r, browseResult{
		Path:   path,
		Parent: parent,
		Dirs:   dirs,
	})
}
