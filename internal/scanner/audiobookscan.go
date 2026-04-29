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
// Hierarchy (matches parseAudiobookPath):
//
//	<root>/<Author>/<Series>/<Book>/<file>  → book_author → book_series
//	                                          → audiobook → audiobook_chapter
//	<root>/<Author>/<Book>/<file>           → book_author → audiobook
//	                                          → audiobook_chapter
//	<root>/<Author>/<file>                  → book_author → audiobook
//	<root>/<Book>.m4b                       → audiobook (no parent)
//
// Single-file books at the root keep `parent_id = NULL` and stash
// the author on OriginalTitle (when tags supply one). Everything
// else slots into a proper hierarchy so the library grid can drill
// from author → series → book → chapter the same way music does
// artist → album → track.
//
// processAudiobook returns the row that owns this *file* — for single-
// file layouts that's the book itself, for multi-file layouts that's
// the chapter. The scanner pipeline attaches the file to whichever row
// is returned, so book / author / series rows never carry files
// directly.
func (s *Scanner) processAudiobook(ctx context.Context, libraryID uuid.UUID, path string, roots []string) (*media.Item, error) {
	bookTitle, author, series := parseAudiobookPath(path, roots)
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

	// Resolve the parent chain. authorRow is nil for "loose at root"
	// books — those keep the historical parent_id=NULL shape so an
	// untagged drop at the library root doesn't gain a phantom author.
	var authorRow, seriesRow *media.Item
	if author != "" {
		var err error
		authorRow, err = s.findOrCreateAuthor(ctx, libraryID, author)
		if err != nil {
			return nil, err
		}
		if series != "" {
			seriesRow, err = s.findOrCreateSeries(ctx, libraryID, series, authorRow.ID)
			if err != nil {
				return nil, err
			}
		}
	}

	bookParent := (*uuid.UUID)(nil)
	switch {
	case seriesRow != nil:
		bookParent = &seriesRow.ID
	case authorRow != nil:
		bookParent = &authorRow.ID
	}

	multiFile := isMultiFileBookPath(path, roots) || isSeriesBookPath(path, roots)

	if !multiFile {
		// Single-file: tag title (if present) wins over folder guess.
		title := bookTitle
		if tagTitle != "" {
			title = tagTitle
		}
		return s.findOrCreateAudiobookRow(ctx, libraryID, "audiobook", title, author, bookParent)
	}

	// Multi-file: ensure the book row exists (one per <Book>
	// directory), then create a chapter row pointing at it.
	book, err := s.findOrCreateAudiobookRow(ctx, libraryID, "audiobook", bookTitle, author, bookParent)
	if err != nil {
		return nil, err
	}

	chapterTitle := tagTitle
	if chapterTitle == "" {
		chapterTitle = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	return s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "audiobook_chapter",
		Title:     chapterTitle,
		SortTitle: sortTitle(chapterTitle),
		ParentID:  &book.ID,
	})
}

// findOrCreateAuthor looks up (or creates) the book_author row for an
// author name within the audiobook library. Top-level (parent_id=null);
// dedupe is library-scoped via FindOrCreateItem so two `<Author>` dirs
// at the same root collapse into one row.
func (s *Scanner) findOrCreateAuthor(ctx context.Context, libraryID uuid.UUID, name string) (*media.Item, error) {
	return s.media.FindOrCreateItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "book_author",
		Title:     name,
		SortTitle: sortTitle(name),
	})
}

// findOrCreateSeries looks up (or creates) the book_series row under
// a given author. Hierarchy variant so dedupe is scoped to the
// author's children — two authors with a "Mistborn" series each
// don't collide.
func (s *Scanner) findOrCreateSeries(
	ctx context.Context,
	libraryID uuid.UUID,
	name string,
	authorID uuid.UUID,
) (*media.Item, error) {
	return s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "book_series",
		Title:     name,
		SortTitle: sortTitle(name),
		ParentID:  &authorID,
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
// great-grandparent directory is a library root.
func isMultiFileBookPath(path string, roots []string) bool {
	dir := filepath.Dir(path)         // <Book>
	grand := filepath.Dir(dir)        // <Author>
	greatGrand := filepath.Dir(grand) // <library root>?
	return isLibraryRoot(greatGrand, roots)
}

// isSeriesBookPath returns true when the path matches the
// <root>/<Author>/<Series>/<Book>/<file> layout — i.e., the file's
// great-great-grandparent directory is a library root. The series
// branch is one level deeper than the multi-file branch and is
// disjoint from it (different ancestor depth, never both true).
func isSeriesBookPath(path string, roots []string) bool {
	dir := filepath.Dir(path)              // <Book>
	grand := filepath.Dir(dir)             // <Series>
	greatGrand := filepath.Dir(grand)      // <Author>
	greatGreatGrand := filepath.Dir(greatGrand) // <library root>?
	return isLibraryRoot(greatGreatGrand, roots)
}

// parseAudiobookPath returns (bookTitle, author, series) derived from
// the directory layout. Used as the fallback when ID3 tags are missing
// or when we need a stable "this file belongs to which book" key
// independent of tag quality.
//
//	<root>/<Author>/<Series>/<Book>/<file>  → bookTitle = parent dir,
//	                                          series    = grand,
//	                                          author    = greatGrand
//	<root>/<Author>/<Book>/<file>           → bookTitle = parent dir,
//	                                          author    = grandparent dir,
//	                                          series    = ""
//	<root>/<Author>/<file>                  → bookTitle = filename stem,
//	                                          author    = parent dir,
//	                                          series    = ""
//	<root>/<file>                           → bookTitle = filename stem,
//	                                          author    = "",
//	                                          series    = ""
func parseAudiobookPath(path string, roots []string) (bookTitle, author, series string) {
	bookTitle = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	dir := filepath.Dir(path)
	parent := filepath.Base(dir)
	grand := filepath.Dir(dir)
	greatGrand := filepath.Dir(grand)
	greatGreatGrand := filepath.Dir(greatGrand)

	// Loose file at the library root — no author, title from filename.
	if isLibraryRoot(dir, roots) {
		return
	}

	// Series: <root>/<Author>/<Series>/<Book>/<file>. Book is parent,
	// series is grandparent, author is great-grandparent.
	if isLibraryRoot(greatGreatGrand, roots) {
		bookTitle = parent
		series = filepath.Base(grand)
		author = filepath.Base(greatGrand)
		return
	}

	// Multi-file: <root>/<Author>/<Book>/<file>. Book is parent,
	// author is grandparent.
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

	// Deeper than the supported layouts — fall back to "nearest
	// non-root ancestor is the author." Title stays as the filename
	// stem; no series guess.
	author = parent
	return
}
