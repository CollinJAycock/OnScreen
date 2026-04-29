package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// processAudiobook ingests one audio file from an audiobook library.
//
// Two layouts are supported, mapping to two different DB shapes:
//
//   - Single-file book: <root>/<file>.m4b OR <root>/<Author>/<file>.m4b
//     One row, type="audiobook", file attached. Plays directly.
//
//   - Multi-file book: <root>/<Author>/<Book>/<part1>.mp3, <part2>.mp3, …
//     One parent row per book directory, type="audiobook" (this is what
//     the library grid renders). One child row per audio file,
//     type="audiobook_chapter", with parent_id pointing at the book.
//     Drilling into the book on the detail page lists the chapters in
//     filename order. Mirrors the album → track shape used for music.
//
// processAudiobook returns the row that owns this *file* — for single-
// file layouts that's the book itself, for multi-file layouts that's
// the chapter. The scanner pipeline attaches the file to whichever row
// is returned, so the parent book in multi-file mode never carries
// files of its own.
//
// Folder layout precedence (matching parseAudiobookPath):
//
//	<root>/<Author>/<Book>/<file>   → multi-file book; book = parent dir,
//	                                  author = grandparent dir
//	<root>/<Author>/<file>          → single-file book; author = parent dir
//	<root>/<Book Name>.m4b          → single-file book at root, no author
//
// The series hierarchy (author → series → book) is deferred to a later
// pass — author still lives on OriginalTitle for both single-file books
// and multi-file parents.
func (s *Scanner) processAudiobook(ctx context.Context, libraryID uuid.UUID, path string, roots []string) (*media.Item, error) {
	bookTitle, author := parseAudiobookPath(path, roots)
	if bookTitle == "" {
		bookTitle = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	// Read embedded tags so the chapter row carries a useful title
	// (taggers commonly write "Chapter 3: …") and the book row's
	// author can override the folder guess if the artist tag is set.
	var tagTitle, tagAuthor string
	if f, err := os.Open(path); err == nil {
		if m, err := readTagFrom(f); err == nil {
			tagTitle = strings.TrimSpace(m.Title())
			if a := strings.TrimSpace(m.AlbumArtist()); a != "" {
				tagAuthor = a
			} else if a := strings.TrimSpace(m.Artist()); a != "" {
				tagAuthor = a
			}
		}
		_ = f.Close()
	}
	if tagAuthor != "" {
		author = tagAuthor
	}

	multiFile := isMultiFileBookPath(path, roots)

	if !multiFile {
		// Existing single-file flow. Tag title (if present) wins
		// over folder guess for the visible name.
		title := bookTitle
		if tagTitle != "" {
			title = tagTitle
		}
		return s.findOrCreateAudiobookRow(ctx, libraryID, "audiobook", title, author, nil)
	}

	// Multi-file: ensure a parent book row exists (one per <Book>
	// directory), then create a chapter row pointing at it.
	book, err := s.findOrCreateAudiobookRow(ctx, libraryID, "audiobook", bookTitle, author, nil)
	if err != nil {
		return nil, err
	}

	chapterTitle := tagTitle
	if chapterTitle == "" {
		chapterTitle = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	// Children use the hierarchy variant so dedupe is scoped to the
	// parent's children list — two books with a "Chapter 1" each
	// don't collide.
	return s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "audiobook_chapter",
		Title:     chapterTitle,
		SortTitle: sortTitle(chapterTitle),
		ParentID:  &book.ID,
	})
}

// findOrCreateAudiobookRow centralises the "stash author on
// OriginalTitle" detail so both the single-file and multi-file paths
// emit the same DB shape.
func (s *Scanner) findOrCreateAudiobookRow(
	ctx context.Context,
	libraryID uuid.UUID,
	itemType, title, author string,
	parentID *uuid.UUID,
) (*media.Item, error) {
	p := media.CreateItemParams{
		LibraryID: libraryID,
		Type:      itemType,
		Title:     title,
		SortTitle: sortTitle(title),
		ParentID:  parentID,
	}
	if author != "" {
		p.OriginalTitle = &author
	}
	if parentID != nil {
		return s.media.FindOrCreateHierarchyItem(ctx, p)
	}
	return s.media.FindOrCreateItem(ctx, p)
}

// isMultiFileBookPath returns true when the path matches the
// <root>/<Author>/<Book>/<file> layout — i.e., the file's
// great-grandparent directory is a library root. Single-level layouts
// (<root>/<Author>/<file> and <root>/<file>) are single-file books.
func isMultiFileBookPath(path string, roots []string) bool {
	dir := filepath.Dir(path)        // <Book>
	grand := filepath.Dir(dir)       // <Author>
	greatGrand := filepath.Dir(grand) // <library root>?
	return isLibraryRoot(greatGrand, roots)
}

// parseAudiobookPath returns (bookTitle, author) derived from the
// directory layout. Used as the fallback when ID3 tags are missing or
// when we need a stable "this file belongs to which book" key
// independent of tag quality.
//
//	<root>/<Author>/<Book>/<file>   → bookTitle = parent dir,
//	                                  author    = grandparent dir
//	<root>/<Author>/<file>          → bookTitle = filename stem,
//	                                  author    = parent dir
//	<root>/<file>                   → bookTitle = filename stem,
//	                                  author    = ""
func parseAudiobookPath(path string, roots []string) (bookTitle, author string) {
	bookTitle = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	dir := filepath.Dir(path)
	parent := filepath.Base(dir)
	grand := filepath.Dir(dir)
	greatGrand := filepath.Dir(grand)

	// Loose file at the library root — no author, title from filename.
	if isLibraryRoot(dir, roots) {
		return
	}

	// Multi-file: <root>/<Author>/<Book>/<file>. Book is the parent
	// dir, author is the grandparent dir.
	if isLibraryRoot(greatGrand, roots) {
		bookTitle = parent
		author = filepath.Base(grand)
		return
	}

	// Single-file with author: <root>/<Author>/<file>.
	if isLibraryRoot(grand, roots) {
		author = parent
		return
	}

	// Deeper than two levels — fall back to "nearest non-root
	// ancestor is the author." Title stays as the filename stem.
	author = parent
	return
}
