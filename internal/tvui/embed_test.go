package tvui

import (
	"io/fs"
	"strings"
	"testing"
)

// FS must hand back a servable index.html whether or not the TV app was
// built into this checkout; without a build it is the placeholder page.
func TestFS_AlwaysHasIndex(t *testing.T) {
	fsys, present := FS()
	body, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		t.Fatalf("index.html: %v", err)
	}
	isPlaceholder := strings.Contains(string(body), "TV app isn't included")
	if present == isPlaceholder {
		t.Fatalf("present = %v but placeholder page = %v", present, isPlaceholder)
	}
}
