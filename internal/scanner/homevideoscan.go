package scanner

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// processHomeVideo creates a 'home_video' media_item for a file in a
// home-video library. v2.1 scope is flat — one file = one row — with
// no external metadata agent (TMDB doesn't have your kid's birthday
// party). Title comes from the filename with leading date prefixes
// stripped; the date itself populates originally_available_at, which
// the library page uses as the primary sort key.
//
// Files under a non-root subfolder (e.g.
// `<root>/Yellowstone 2024/<file>`) get auto-added to an event_folder
// collection named after the top-level subfolder. Re-scans use the
// same collection (idempotent upsert). Files loose at the library
// root skip the collection step.
//
// Date sourced from file mtime. ffprobe's container `creation_time`
// would be more accurate for camcorder formats but the scanner doesn't
// route the probe result back into the create-item path here, and
// adding that plumbing is a v2.x polish — mtime is correct for the
// "drop a file in and rescan" workflow most users follow.
func (s *Scanner) processHomeVideo(ctx context.Context, libraryID uuid.UUID, path string, roots []string, mtime time.Time) (*media.Item, error) {
	title := parseHomeVideoTitle(path)
	if title == "" {
		title = "Home Video"
	}

	taken := mtime
	p := media.CreateItemParams{
		LibraryID:             libraryID,
		Type:                  "home_video",
		Title:                 title,
		SortTitle:             sortTitle(title),
		OriginallyAvailableAt: &taken,
	}

	item, err := s.media.FindOrCreateItem(ctx, p)
	if err != nil {
		return nil, err
	}

	if event := eventFolderName(path, roots); event != "" {
		colID, cerr := s.media.UpsertEventCollection(ctx, libraryID, event)
		if cerr != nil {
			s.logger.WarnContext(ctx, "upsert event collection failed",
				"library_id", libraryID, "event", event, "err", cerr)
		} else if aerr := s.media.AddItemToCollection(ctx, colID, item.ID); aerr != nil {
			s.logger.WarnContext(ctx, "add home_video to event collection failed",
				"collection_id", colID, "item_id", item.ID, "err", aerr)
		}
	}

	return item, nil
}

// eventFolderName returns the immediate child of the library root that
// contains `path`. For `<root>/Yellowstone 2024/clip.mp4` it returns
// "Yellowstone 2024"; for `<root>/Yellowstone 2024/Day 1/clip.mp4` it
// also returns "Yellowstone 2024" (the *top-level* event subfolder
// wins so multi-day trips collapse into one collection rather than
// splitting per day). Returns "" when the file sits directly at the
// library root or when no configured root is an ancestor of path.
func eventFolderName(path string, roots []string) string {
	pathClean := filepath.Clean(path)
	pathDir := filepath.Dir(pathClean)
	for _, r := range roots {
		rootClean := filepath.Clean(r)
		// Loose at root — no event folder above.
		if pathDir == rootClean {
			return ""
		}
		// Walk up from the file's parent until the parent of the
		// current dir IS the library root. That child of root is the
		// event folder. filepath.Dir on the root itself returns the
		// drive (e.g. `C:\`), which never matches a configured root —
		// so the loop exits cleanly when path isn't under r.
		dir := pathDir
		for {
			parent := filepath.Dir(dir)
			if parent == dir { // hit FS root, not under this library root
				break
			}
			if parent == rootClean {
				return filepath.Base(dir)
			}
			dir = parent
		}
	}
	return ""
}

// dateStemPrefix matches a leading "YYYY-MM-DD" or "YYYY_MM_DD" or
// "YYYYMMDD" optionally followed by " - " or " " separators. Stripped
// from titles so a file named "2024-04-15 - Yellowstone hike.mp4"
// surfaces as "Yellowstone hike" rather than the raw filename.
var dateStemPrefix = regexp.MustCompile(`^(?:\d{4}[-_]\d{2}[-_]\d{2}|\d{8})\s*(?:[-—]\s*)?`)

// parseHomeVideoTitle returns a clean title from a home-video file path.
// Strips the extension, normalises common separators, and removes any
// leading date prefix that the user added for their own ordering.
func parseHomeVideoTitle(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	stem = dateStemPrefix.ReplaceAllString(stem, "")
	stem = strings.ReplaceAll(stem, "_", " ")
	stem = strings.ReplaceAll(stem, ".", " ")
	return strings.TrimSpace(stem)
}
