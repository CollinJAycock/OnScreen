package main

import (
	"context"
	"fmt"

	"github.com/onscreen/onscreen/internal/domain/library"
)

// storageLibraryRootsAdapter lists every library's root folders for the
// storage settings' path-mapping check (v1.StorageLibraryRoots). Read on each
// save, which is rare, so a newly added library counts at once.
type storageLibraryRootsAdapter struct{ libs *library.Service }

func (a storageLibraryRootsAdapter) LibraryRootPaths(ctx context.Context) ([]string, error) {
	libs, err := a.libs.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("library roots: %w", err)
	}
	var out []string
	for _, l := range libs {
		out = append(out, l.Paths...)
	}
	return out, nil
}
