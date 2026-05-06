package scanner

import (
	"bytes"
	"context"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata/openlibrary"
	"github.com/onscreen/onscreen/internal/metadata/wikipedia"
	"github.com/onscreen/onscreen/internal/safehttp"
)

// trailingVolumeRangeRE strips trailing volume / part / book-range markers
// from a book folder name: " 1-2", " 1 of 2", " vol 1", " volume 1",
// " book 1". Case-insensitive. Used by cleanReleaseGroupBookTitle so
// "A Court of Silver Flames 1-2" trims to "A Court of Silver Flames".
var trailingVolumeRangeRE = regexp.MustCompile(`(?i)\s+(?:vol(?:ume)?|book|pt|part)\.?\s*\d+(?:\s*-\s*\d+|\s+of\s+\d+)?\s*$|\s+\d+\s*-\s*\d+\s*$`)

// audiobookArtFilenames lists on-disk cover candidates checked in the
// audiobook's directory. Same shape as albumArtFilenames; "cover.jpg"
// is the standard for Audible/Plex/Jellyfin libraries, "folder.jpg" is
// the Windows convention.
var audiobookArtFilenames = []string{
	"cover.jpg", "cover.jpeg", "cover.png",
	"folder.jpg", "folder.jpeg", "folder.png",
	"poster.jpg", "poster.jpeg", "poster.png",
}

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
	// Strip release-group encoding from folder-derived names so the UI
	// doesn't show "A.Court.of.Silver.Flames.1-2.by.Sarah.J.Maas" as
	// the author tile. No-op for already-clean folder names like
	// "Brandon Sanderson" / "Mistborn".
	author = cleanReleaseGroupAuthor(author)
	bookTitle = cleanReleaseGroupBookTitle(bookTitle)

	// Read embedded tags so the chapter row carries a useful title
	// (taggers commonly write "Chapter 3: …") and the book row's
	// author can override the folder guess for the no-folder case
	// (loose .m4b at the library root, where there's no author dir).
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
	// Tag-author override is restricted to layouts with NO folder author
	// (loose .m4b at the root). For author-organized layouts —
	// <root>/<Author>/<file>, <root>/<Author>/<Book>/<file>, and
	// <root>/<Author>/<Series>/<Book>/<file> — the folder is
	// authoritative. Why: m4b/mp3 AlbumArtist tags are wildly
	// inconsistent across files in the same book (Graphic Audio
	// productions use "Graphic Audio LLC." for some chapters and
	// "<Real Author>" for others), and accepting the per-file tag
	// would split a single logical author into N separate book_author
	// rows, breaking the author hub tile and creating audiobook dupes
	// — exactly the QA bug that prompted this carve-out.
	if tagAuthor != "" && author == "" {
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
		book, err := s.findOrCreateAudiobookRow(ctx, libraryID, "audiobook", title, author, bookParent)
		if err != nil {
			return nil, err
		}
		s.extractAudiobookArt(ctx, book, authorRow, path, roots)
		s.fetchExternalAudiobookArt(ctx, book, authorRow, path, roots, author)
		return book, nil
	}

	// Multi-file: ensure the book row exists (one per <Book>
	// directory), then create a chapter row pointing at it.
	book, err := s.findOrCreateAudiobookRow(ctx, libraryID, "audiobook", bookTitle, author, bookParent)
	if err != nil {
		return nil, err
	}
	s.extractAudiobookArt(ctx, book, authorRow, path, roots)
	s.fetchExternalAudiobookArt(ctx, book, authorRow, path, roots, author)

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

// extractAudiobookArt writes the audiobook's cover to {book.id}-poster.jpg
// in the book directory and updates book.poster_path. Source order:
// on-disk cover files first (cover.jpg / folder.jpg / poster.jpg in the
// book dir or its parent for single-file-with-author layouts), then the
// audio file's embedded picture tag. When the author row has no
// poster_path yet, it inherits the same path so the author tile shows
// the first scanned book's cover until something better lands (currently
// nothing — there's no audiobook-side equivalent of TheAudioDB).
//
// Idempotent: if the {id}-poster.jpg already exists on disk, we just
// ensure the DB pointers are correct without re-extracting. Re-scans
// of the same library are cheap.
//
// Mirrors extractAlbumArt in shape; kept separate because the parent
// chain differs (audiobook → book_author, vs album → artist) and
// because the candidate filename list is narrower for books (no
// "front.jpg" / "album.jpg" — those only appear in music libraries).
func (s *Scanner) extractAudiobookArt(ctx context.Context, book *media.Item, author *media.Item, filePath string, roots []string) string {
	if book == nil {
		return ""
	}
	bookDir := filepath.Dir(filePath)

	// Look in the book's own directory first; if nothing there and the
	// layout is "single-file at <Author>/<file>", the cover may be in
	// the author dir alongside other books. We don't walk further up.
	artData, ok := findArtOnDisk(bookDir, audiobookArtFilenames)
	if !ok {
		// Embedded picture tag from the audio file itself.
		if data, err := readEmbeddedArtwork(filePath); err == nil && len(data) > 0 {
			artData = data
		}
	}
	if len(artData) == 0 {
		return ""
	}

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

	// Idempotent re-scan: if the ID-qualified poster already exists,
	// only sync the DB pointers (book + author).
	if _, err := os.Stat(posterFile); err == nil {
		s.syncAudiobookPosterPaths(ctx, book, author, relPath)
		return relPath
	}

	// Re-encode through stdlib JPEG when possible for consistent quality;
	// fall through to raw bytes when the source isn't a decodable image
	// the stdlib supports (some m4b embeds are JPEG-2000 etc).
	var outData []byte
	if img, imgErr := decodeImageBytes(artData); imgErr == nil {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err == nil {
			outData = buf.Bytes()
		}
	}
	if outData == nil {
		outData = artData
	}

	if err := os.WriteFile(posterFile, outData, 0o644); err != nil {
		s.logger.WarnContext(ctx, "failed to write audiobook art",
			"book_id", book.ID, "err", err)
		return ""
	}

	s.syncAudiobookPosterPaths(ctx, book, author, relPath)
	return relPath
}

// syncAudiobookPosterPaths sets book.poster_path to relPath, and
// cascades the same path to the author row when the author has no
// poster yet. Skips the author update when the author already has a
// poster — first-book-wins keeps later scans from churning the
// author tile every time another book is added.
func (s *Scanner) syncAudiobookPosterPaths(ctx context.Context, book *media.Item, author *media.Item, relPath string) {
	if book.PosterPath == nil || *book.PosterPath != relPath {
		if _, err := s.media.UpdateItemMetadata(ctx, media.UpdateItemMetadataParams{
			ID:        book.ID,
			Title:     book.Title,
			SortTitle: book.SortTitle,
			Year:      book.Year,
			PosterPath: &relPath,
		}); err != nil {
			s.logger.WarnContext(ctx, "failed to update audiobook poster_path",
				"book_id", book.ID, "err", err)
		} else {
			book.PosterPath = &relPath
		}
	}
	if author != nil && (author.PosterPath == nil || *author.PosterPath == "") {
		if _, err := s.media.UpdateItemMetadata(ctx, media.UpdateItemMetadataParams{
			ID:        author.ID,
			Title:     author.Title,
			SortTitle: author.SortTitle,
			PosterPath: &relPath,
		}); err != nil {
			s.logger.WarnContext(ctx, "failed to update author poster_path",
				"author_id", author.ID, "err", err)
		} else {
			author.PosterPath = &relPath
		}
	}
}

// externalArtHTTPClient is shared across openlibrary/wikipedia metadata
// lookups + cover image downloads. 15s is generous enough for slow
// upload.wikimedia.org responses on first-fetch (their CDN warmup),
// short enough that a stuck scan doesn't hang the library scan loop.
//
// Routed through safehttp so a compromised or hostile upstream
// (poisoned OpenLibrary mirror, MITM'd Wikipedia thumbnail CDN)
// can't redirect us at internal services — 169.254.169.254 (cloud
// metadata), 127.0.0.1, RFC1918 ranges, etc. are rejected at the
// dialer's Control hook (post-resolution, pre-connect, immune to
// DNS rebinding). Mirrors the policy on artwork.Manager and
// internal/subtitles/opensubtitles.
var externalArtHTTPClient = safehttp.NewClient(safehttp.DialPolicy{}, 15*time.Second)

// fetchExternalAudiobookArt fills in book + author posters from
// OpenLibrary (book covers) and Wikipedia (author portraits) when
// extractAudiobookArt didn't find anything local. Skipped silently
// for any field that already has poster_path set — both upstream
// services are best-effort fallbacks, never authoritative over
// hand-curated cover.jpg / m4b embedded art.
//
// Each lookup is idempotent: the saved file is named with the row's
// UUID so re-scans see the cover already on disk and reuse it (the
// audiobookArtFilenames disk-scan picks the {id}-poster.jpg back up
// on the next pass via syncAudiobookPosterPaths). The actual
// HTTP fetches only happen on the first scan that lands an empty
// poster_path; once a row has art, we never re-query.
func (s *Scanner) fetchExternalAudiobookArt(ctx context.Context, book, author *media.Item, filePath string, roots []string, parsedAuthor string) {
	bookDir := filepath.Dir(filePath)

	if book != nil && (book.PosterPath == nil || *book.PosterPath == "") {
		olAuthor := parsedAuthor
		if olAuthor == "" && book.OriginalTitle != nil {
			olAuthor = *book.OriginalTitle
		}
		if olAuthor == "" && author != nil {
			olAuthor = author.Title
		}

		olClient := openlibrary.NewWithClient(externalArtHTTPClient)
		coverURL, err := olClient.SearchBookCoverURL(ctx, book.Title, olAuthor)
		if err != nil {
			s.logger.WarnContext(ctx, "OpenLibrary lookup failed",
				"book_id", book.ID, "title", book.Title, "err", err)
		} else if coverURL != "" {
			if relPath, ok := s.downloadAndStorePoster(ctx, book.ID, coverURL, bookDir, roots); ok {
				s.syncAudiobookPosterPaths(ctx, book, author, relPath)
				s.logger.InfoContext(ctx, "OpenLibrary cover applied",
					"book_id", book.ID, "title", book.Title, "url", coverURL)
			}
		}
	}

	// Author portrait. Skip if author already has art (either from
	// cascading book cover via syncAudiobookPosterPaths above, or from
	// an earlier scan, or from Fix Match).
	if author != nil && (author.PosterPath == nil || *author.PosterPath == "") {
		// Authors live in their own directory at <root>/<Author>/...; we
		// store the portrait there so the /artwork/* route can resolve
		// {author.id}-poster.jpg the same way book covers resolve
		// {book.id}-poster.jpg next to the book.
		authorDir := filepath.Dir(bookDir)
		if !isLibraryRoot(authorDir, roots) && !isLibraryRoot(filepath.Dir(authorDir), roots) {
			// Path is too deep to safely guess the author dir
			// (series-layout grandchild, deeper-than-supported folder
			// layout). Fall back to bookDir's parent which is
			// authorDir for the standard layouts and a near-miss for
			// the rest — worst case we write a portrait that's
			// adjacent to the wrong folder; the relPath is still
			// valid for /artwork/* serving.
		}
		wikiClient := wikipedia.NewWithClient(externalArtHTTPClient)
		portraitURL, err := wikiClient.GetThumbnailURL(ctx, author.Title)
		if err != nil {
			s.logger.WarnContext(ctx, "Wikipedia lookup failed",
				"author_id", author.ID, "name", author.Title, "err", err)
		} else if portraitURL != "" {
			if relPath, ok := s.downloadAndStorePoster(ctx, author.ID, portraitURL, authorDir, roots); ok {
				if _, err := s.media.UpdateItemMetadata(ctx, media.UpdateItemMetadataParams{
					ID:         author.ID,
					Title:      author.Title,
					SortTitle:  author.SortTitle,
					PosterPath: &relPath,
				}); err != nil {
					s.logger.WarnContext(ctx, "failed to update author poster_path",
						"author_id", author.ID, "err", err)
				} else {
					author.PosterPath = &relPath
					s.logger.InfoContext(ctx, "Wikipedia portrait applied",
						"author_id", author.ID, "name", author.Title, "url", portraitURL)
				}
			}
		}
	}
}

// downloadAndStorePoster fetches an image URL, re-encodes through
// stdlib JPEG when possible (consistent quality + size across PNG /
// WEBP / weird formats some sources serve), writes it to
// {targetDir}/{itemID}-poster.jpg, and returns the relative path the
// /artwork/* route resolves against. Returns ok=false when the
// fetch fails, the response is non-2xx, the bytes can't be written,
// or the image is suspiciously small (< 1KB suggests an error page
// or sentinel pixel).
//
// Idempotent: when {itemID}-poster.jpg already exists on disk, just
// returns its path without re-fetching. Re-scans of an unchanged
// library don't re-spam upstream services.
func (s *Scanner) downloadAndStorePoster(ctx context.Context, itemID uuid.UUID, imageURL, targetDir string, roots []string) (string, bool) {
	posterFile := filepath.Join(targetDir, itemID.String()+"-poster.jpg")

	relPath := ""
	for _, root := range roots {
		if rel, err := filepath.Rel(root, posterFile); err == nil && !strings.HasPrefix(rel, "..") {
			relPath = filepath.ToSlash(rel)
			break
		}
	}
	if relPath == "" {
		relPath = filepath.ToSlash(filepath.Join(filepath.Base(targetDir), itemID.String()+"-poster.jpg"))
	}

	// Idempotent: if we already wrote this poster, skip the network hop.
	if _, err := os.Stat(posterFile); err == nil {
		return relPath, true
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", false
	}
	req.Header.Set("User-Agent", "OnScreen/2.1 (https://github.com/onscreen/onscreen)")
	resp, err := externalArtHTTPClient.Do(req)
	if err != nil {
		s.logger.WarnContext(ctx, "external art GET failed", "url", imageURL, "err", err)
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		s.logger.WarnContext(ctx, "external art bad status", "url", imageURL, "status", resp.StatusCode)
		return "", false
	}

	// Cap the read at 10 MB. Wikipedia originals are typically 200KB-3MB;
	// OpenLibrary L-size covers are under 200KB. A 10 MB ceiling is
	// generous for either source while still preventing a misconfigured
	// upstream from filling our disk.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		s.logger.WarnContext(ctx, "external art read failed", "url", imageURL, "err", err)
		return "", false
	}
	if len(raw) < 1024 {
		// Smaller than 1KB — most likely an error page or transparent
		// sentinel pixel. Reject so we don't leave bogus art on disk.
		return "", false
	}

	// Re-encode through stdlib JPEG when possible (uniform quality 90,
	// drops alpha channels, normalises to JPEG); fall through to raw
	// bytes when the source isn't a stdlib-decodable image.
	var outData []byte
	if img, imgErr := decodeImageBytes(raw); imgErr == nil {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err == nil {
			outData = buf.Bytes()
		}
	}
	if outData == nil {
		outData = raw
	}
	if err := os.WriteFile(posterFile, outData, 0o644); err != nil {
		s.logger.WarnContext(ctx, "external art write failed", "path", posterFile, "err", err)
		return "", false
	}
	return relPath, true
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

// cleanReleaseGroupAuthor extracts a human-readable author name from a
// folder that may be release-group encoded. The two real-world sources
// of bad author names are:
//
//  1. Dot-separated tokens with the actual author embedded as
//     "by.First.Last" — e.g.
//     "A.Court.of.Silver.Flames.1-2.by.Sarah.J.Maas". This is the
//     scene/torrent convention; the author folder ends up as the whole
//     release identifier instead of just "Sarah J. Maas".
//  2. Hand-typed folders that include the author after the title:
//     "A Court of Silver Flames by Sarah J Maas".
//
// Strategy: replace dots with spaces (so token boundaries become
// whitespace), then look for " by " followed by a multi-word name. If
// found, return the part after "by" as the author. Otherwise return
// the de-dotted folder unchanged. The multi-word check guards against
// false positives like a literal name "Bob By Smith" — we only strip
// when what follows looks like a person's name.
//
// Idempotent: cleanReleaseGroupAuthor("Brandon Sanderson") returns
// "Brandon Sanderson". Folders that are already well-formed pass
// through untouched.
func cleanReleaseGroupAuthor(folder string) string {
	if folder == "" {
		return ""
	}
	cleaned := strings.ReplaceAll(folder, ".", " ")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	lc := strings.ToLower(cleaned)
	if idx := strings.LastIndex(lc, " by "); idx > 0 {
		candidate := strings.TrimSpace(cleaned[idx+len(" by "):])
		if isMultiWordName(candidate) {
			return candidate
		}
	}
	return cleaned
}

// cleanReleaseGroupBookTitle is the dual of cleanReleaseGroupAuthor:
// strips the trailing "by AUTHOR" tail and any trailing volume / part
// markers so a book folder like "A Court of Silver Flames 1-2 by Sarah
// J Maas" becomes "A Court of Silver Flames". Same multi-word guard so
// "Up By Up" titles aren't truncated.
func cleanReleaseGroupBookTitle(folder string) string {
	if folder == "" {
		return ""
	}
	cleaned := strings.ReplaceAll(folder, ".", " ")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	lc := strings.ToLower(cleaned)
	if idx := strings.LastIndex(lc, " by "); idx > 0 {
		candidate := strings.TrimSpace(cleaned[idx+len(" by "):])
		if isMultiWordName(candidate) {
			cleaned = strings.TrimSpace(cleaned[:idx])
		}
	}
	cleaned = trailingVolumeRangeRE.ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

func isMultiWordName(s string) bool {
	return len(strings.Fields(s)) >= 2
}
