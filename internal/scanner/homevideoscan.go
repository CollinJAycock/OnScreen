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
// Date sourced from file mtime. ffprobe's container `creation_time`
// would be more accurate for camcorder formats but the scanner doesn't
// route the probe result back into the create-item path here, and
// adding that plumbing is a v2.x polish — mtime is correct for the
// "drop a file in and rescan" workflow most users follow.
func (s *Scanner) processHomeVideo(ctx context.Context, libraryID uuid.UUID, path string, mtime time.Time) (*media.Item, error) {
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
	return item, nil
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
