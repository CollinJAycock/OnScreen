package scanner

import (
	"archive/zip"
	"bytes"
	"context"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"sort"
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
func (s *Scanner) processBook(ctx context.Context, libraryID uuid.UUID, path string, roots []string) (*media.Item, error) {
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
	s.extractBookCover(ctx, item, path, roots)
	return item, nil
}

// extractBookCover writes the CBZ's first page (alphabetically; this is
// the de-facto cover for comic archives — Picard / Komga / Kavita all
// agree on this convention) as {book.id}-poster.jpg next to the CBZ
// file and sets book.poster_path. Re-encodes through stdlib JPEG when
// possible so even PNG-page covers land as JPEG. Idempotent on re-scan
// via os.Stat-then-skip; only the DB pointer is synced when the
// ID-qualified file already exists.
//
// Skipped silently when:
//   - the CBZ contains no recognisable image entries (empty book)
//   - the image's bytes are unreadable (corrupted entry)
//   - filesystem write fails (logged at warn, scan continues)
func (s *Scanner) extractBookCover(ctx context.Context, book *media.Item, cbzPath string, roots []string) {
	if book == nil {
		return
	}
	bookDir := filepath.Dir(cbzPath)
	posterFile := filepath.Join(bookDir, book.ID.String()+"-poster.jpg")

	relPath := ""
	for _, root := range roots {
		if rel, err := filepath.Rel(root, posterFile); err == nil && !strings.HasPrefix(rel, "..") {
			relPath = filepath.ToSlash(rel)
			break
		}
	}
	if relPath == "" {
		relPath = filepath.ToSlash(filepath.Join(filepath.Base(bookDir), book.ID.String()+"-poster.jpg"))
	}

	// Idempotent: if the cover is already on disk, just sync the DB pointer.
	if _, err := os.Stat(posterFile); err == nil {
		s.syncBookCoverPath(ctx, book, relPath)
		return
	}

	imgBytes, ok := readFirstCBZPage(cbzPath)
	if !ok {
		return
	}

	var outData []byte
	if img, imgErr := decodeImageBytes(imgBytes); imgErr == nil {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err == nil {
			outData = buf.Bytes()
		}
	}
	if outData == nil {
		outData = imgBytes
	}

	if err := os.WriteFile(posterFile, outData, 0o644); err != nil {
		s.logger.WarnContext(ctx, "failed to write book cover",
			"book_id", book.ID, "err", err)
		return
	}
	s.syncBookCoverPath(ctx, book, relPath)
}

// syncBookCoverPath sets book.poster_path to relPath when it's missing
// or stale. Mirrors the audiobook variant; kept separate because we
// don't cascade book covers up a parent — books are flat in v2.1.
func (s *Scanner) syncBookCoverPath(ctx context.Context, book *media.Item, relPath string) {
	if book.PosterPath != nil && *book.PosterPath == relPath {
		return
	}
	if _, err := s.media.UpdateItemMetadata(ctx, media.UpdateItemMetadataParams{
		ID:         book.ID,
		Title:      book.Title,
		SortTitle:  book.SortTitle,
		Year:       book.Year,
		DurationMS: book.DurationMS,
		PosterPath: &relPath,
	}); err != nil {
		s.logger.WarnContext(ctx, "failed to update book poster_path",
			"book_id", book.ID, "err", err)
		return
	}
	book.PosterPath = &relPath
}

// readFirstCBZPage opens the CBZ, sorts the image entries alphabetically
// (matching the convention every comic reader uses for page order),
// and returns the bytes of the first page. Returns ok=false when the
// archive is unreadable or contains no image entries.
func readFirstCBZPage(cbzPath string) ([]byte, bool) {
	r, err := zip.OpenReader(cbzPath)
	if err != nil {
		return nil, false
	}
	defer r.Close()

	pages := make([]*zip.File, 0, len(r.File))
	for _, f := range r.File {
		if isCBZPageEntry(f.Name) {
			pages = append(pages, f)
		}
	}
	if len(pages) == 0 {
		return nil, false
	}
	sort.Slice(pages, func(i, j int) bool {
		return pages[i].Name < pages[j].Name
	})

	rc, err := pages[0].Open()
	if err != nil {
		return nil, false
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
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
