package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/library"
)

// arrLibraryAdapter implements v1.ArrLibraryFinder by searching libraries
// for a matching scan_path and delegating scans to the scanEnqueuer.
type arrLibraryAdapter struct {
	libSvc  *library.Service
	scanner *scanEnqueuer
}

// FindLibraryByPath returns the library whose scan_paths contain the given file/dir path.
func (a *arrLibraryAdapter) FindLibraryByPath(ctx context.Context, filePath string) (uuid.UUID, error) {
	libs, err := a.libSvc.List(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("list libraries: %w", err)
	}

	clean := filepath.Clean(filePath)
	for _, lib := range libs {
		for _, root := range lib.Paths {
			cleanRoot := filepath.Clean(root)
			// Check if the path is under this root.
			if strings.HasPrefix(clean, cleanRoot+string(filepath.Separator)) || clean == cleanRoot {
				return lib.ID, nil
			}
		}
	}

	return uuid.Nil, fmt.Errorf("no library contains path %q", filePath)
}

// TriggerDirectoryScan delegates to the scanEnqueuer.
func (a *arrLibraryAdapter) TriggerDirectoryScan(ctx context.Context, libraryID uuid.UUID, dir string) error {
	return a.scanner.TriggerDirectoryScan(ctx, libraryID, dir)
}
