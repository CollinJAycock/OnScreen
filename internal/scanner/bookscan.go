package scanner

import (
	"archive/zip"
	"context"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// processBook creates a 'book' media_item for a CBZ file. v2.1 Stage 1
// scope is CBZ-only — CBZ is just a zip of images and Go's stdlib
// archive/zip covers it without any new dependency. CBR and EPUB are
// deferred until we pick parser deps (see migration 00059's notes).
//
// One file = one row. Title is derived from the filename with leading
// volume / issue prefixes stripped (the scanner doesn't try to infer
// series-and-issue hierarchy in Stage 1; that's a v2.x story matching
// the audiobook author/series follow-up). Page count comes from
// counting image entries in the zip directory and is stored on
// media_items.duration_ms (re-purposed: "duration" for a book = total
// pages). Page count being 0 means the CBZ contained no recognised
// images — surfaced in the API as duration_ms=0 and rendered as
// "empty" in the reader.
func (s *Scanner) processBook(ctx context.Context, libraryID uuid.UUID, path string) (*media.Item, error) {
	title := parseBookTitle(path)
	if title == "" {
		title = filepath.Base(path)
	}

	pages := countCBZPages(path)
	pagesMS := int64(pages)

	p := media.CreateItemParams{
		LibraryID:  libraryID,
		Type:       "book",
		Title:      title,
		SortTitle:  sortTitle(title),
		DurationMS: &pagesMS,
	}
	item, err := s.media.FindOrCreateItem(ctx, p)
	if err != nil {
		return nil, err
	}
	return item, nil
}

// countCBZPages opens the CBZ as a zip and counts entries whose name
// looks like an image. Returns 0 on any error — the caller treats 0
// as "empty book" and the reader UI surfaces it as such, instead of
// failing the entire scan because one CBZ was unreadable.
func countCBZPages(path string) int {
	r, err := zip.OpenReader(path)
	if err != nil {
		return 0
	}
	defer r.Close()
	n := 0
	for _, f := range r.File {
		if isCBZPageEntry(f.Name) {
			n++
		}
	}
	return n
}

// isCBZPageEntry returns true when a zip entry name looks like a
// page image — extension match against the image set, with directory
// entries (trailing slash) and macOS metadata files (__MACOSX,
// .DS_Store) filtered out so they don't inflate the page count.
func isCBZPageEntry(name string) bool {
	if name == "" || strings.HasSuffix(name, "/") {
		return false
	}
	base := filepath.Base(name)
	if base == ".DS_Store" || strings.HasPrefix(base, "._") {
		return false
	}
	if strings.Contains(name, "__MACOSX/") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	return imageExtensions[ext]
}

// parseBookTitle returns a clean title from a book file path. Strips
// the extension and normalises common separators. Doesn't try to
// extract series/issue numbers — that hierarchy work is a follow-up
// alongside the audiobook author/series story.
func parseBookTitle(path string) string {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	stem = strings.ReplaceAll(stem, "_", " ")
	stem = strings.ReplaceAll(stem, ".", " ")
	return strings.TrimSpace(stem)
}
