package v1

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/nwaples/rardecode/v2"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// BookHandler serves single pages from CBZ archives stored as book
// items. v2.1 Stage 1 is CBZ-only (zip of images, Go stdlib parses it
// without any new dependency); CBR + EPUB land once we pick parser
// deps. The handler streams the n-th sorted image entry from the
// archive — pagination state lives on the client, no server-side
// session.
type BookHandler struct {
	media  ItemMediaService
	access LibraryAccessChecker
	logger *slog.Logger
}

// NewBookHandler constructs a BookHandler.
func NewBookHandler(m ItemMediaService, logger *slog.Logger) *BookHandler {
	return &BookHandler{media: m, logger: logger}
}

// WithLibraryAccess wires per-user library filtering. Same pattern as
// every other item-scoped handler.
func (h *BookHandler) WithLibraryAccess(a LibraryAccessChecker) *BookHandler {
	h.access = a
	return h
}

// LibraryAccessWired reports whether the library-ACL checker is wired (a nil
// checker fails open; see api.Handlers.ValidateLibraryAccess).
func (h *BookHandler) LibraryAccessWired() bool { return h.access != nil }

// Page handles GET /api/v1/items/{id}/book/page/{n}.
//
// n is 1-indexed for human-friendliness — page 1 is the first page,
// page <count> is the last. Out-of-range returns 404 with a neutral
// message (same as "item doesn't exist") to keep URL-fishers from
// distinguishing "no page n" from "no item." Sorted-name ordering is
// the convention for CBZ — virtually every CBZ is built with file
// names like "001.jpg, 002.jpg, ..." or per-chapter prefixes that
// preserve order under lexicographic sort.
func (h *BookHandler) Page(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	pageStr := chi.URLParam(r, "n")
	pageNum, err := strconv.Atoi(pageStr)
	if err != nil || pageNum < 1 {
		respond.BadRequest(w, r, "invalid page number")
		return
	}

	item, err := h.media.GetItem(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get item for book page", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !h.checkLibraryAccess(w, r, item.LibraryID) {
		return
	}
	// Content-rating ceiling. Library access alone is not enough: a restricted
	// profile sharing a mixed-rating book library would otherwise read an
	// over-ceiling title page-by-page via this endpoint (GET /items/{id} already
	// blocks it). 404 (not 403) to match this handler's existence-hiding posture.
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		cr := ""
		if item.ContentRating != nil {
			cr = *item.ContentRating
		}
		if !contentrating.IsAllowed(cr, claims.MaxContentRating) {
			respond.NotFound(w, r)
			return
		}
	}
	if item.Type != "book" {
		// Endpoint is book-specific; use the regular file streaming path
		// for other types. 404 (not 400) so we don't leak existence.
		respond.NotFound(w, r)
		return
	}

	files, err := h.media.GetFiles(r.Context(), id)
	if err != nil || len(files) == 0 {
		respond.NotFound(w, r)
		return
	}

	if err := h.servePage(r.Context(), w, files[0], pageNum); err != nil {
		if errors.Is(err, errBookPageNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.WarnContext(r.Context(), "serve book page", "id", id, "page", pageNum, "err", err)
		// Headers may already be flushed if the error fired mid-copy;
		// stopping the write is the best we can do.
		return
	}
}

// errBookPageNotFound separates "asked for page 999 of a 12-page book"
// (a 404 in the user's view) from "couldn't open the archive" (a 500
// or transient log-and-give-up). The handler maps it to 404 so the
// client can react cleanly.
var errBookPageNotFound = errors.New("book: page not found")

// servePage routes to the right archive reader based on the file
// extension. CBZ uses stdlib archive/zip; CBR uses nwaples/rardecode/v2;
// EPUB doesn't go through this path at all (the client streams the
// whole .epub via /media/stream and renders it with epub.js — chapter
// pagination happens in the browser, not the server).
//
// "page N" is 1-indexed and resolved against the alphabetical sort of
// image entries, the convention every comic reader follows. Out-of-
// range yields errBookPageNotFound which the caller maps to 404.
func (h *BookHandler) servePage(ctx context.Context, w http.ResponseWriter, file media.File, pageNum int) error {
	ext := strings.ToLower(filepath.Ext(file.FilePath))
	switch ext {
	case ".cbr":
		return servePageFromCBR(ctx, w, file.FilePath, pageNum)
	case ".epub":
		// EPUB pages aren't image-extracted server-side; the client
		// renders chapters via epub.js. Return 404 so a client that
		// somehow asks /book/page/N for an EPUB sees the same
		// "out of range" code path as a CBZ asking for page 999.
		return errBookPageNotFound
	}

	r, err := zip.OpenReader(file.FilePath)
	if err != nil {
		return err
	}
	defer r.Close()

	// Collect + sort the image entries. Sort once per request — page
	// counts are typically <500 even for omnibus volumes, so the
	// allocation overhead is negligible vs the IO cost of reading
	// the actual page bytes.
	var pages []*zip.File
	for _, f := range r.File {
		if isCBZPageEntryAPI(f.Name) {
			pages = append(pages, f)
		}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Name < pages[j].Name })

	if pageNum < 1 || pageNum > len(pages) {
		return errBookPageNotFound
	}

	entry := pages[pageNum-1]
	rc, err := entry.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	w.Header().Set("Content-Type", contentTypeForBookPage(entry.Name))
	w.Header().Set("Cache-Control", "private, max-age=3600, immutable")
	_, err = io.Copy(w, rc)
	return err
}

// servePageFromCBR mirrors servePage's CBZ branch but for RAR
// archives. rardecode v2 is streaming-only (no random access), so this makes
// TWO passes: the first reads only entry NAMES to work out which one the
// sorted page index refers to, the second re-opens and buffers just that one.
//
// It used to buffer every image entry in the archive and then serve one of
// them. With only a 100 MB per-entry limit and no aggregate cap, a 300-page
// omnibus — or a crafted archive of many large entries — meant one cheap
// authenticated GET could pin gigabytes, and concurrent requests multiplied
// it. The name-only pass costs a second decompression walk but bounds peak
// memory at a single page. maxCBRPageEntries additionally stops an archive
// with an absurd entry count from ballooning the name slice.
//
// ctx is honoured between entries so a client disconnect aborts the walk
// instead of decompressing the remainder for nobody.
func servePageFromCBR(ctx context.Context, w http.ResponseWriter, cbrPath string, pageNum int) error {
	if pageNum < 1 {
		return errBookPageNotFound
	}

	names, err := cbrPageNames(ctx, cbrPath)
	if err != nil {
		return err
	}
	if pageNum > len(names) {
		return errBookPageNotFound
	}
	sort.Strings(names)
	want := names[pageNum-1]

	// Second pass: re-open and buffer ONLY the requested entry.
	f, err := os.Open(cbrPath)
	if err != nil {
		return err
	}
	defer f.Close()
	rr, err := rardecode.NewReader(f)
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := rr.Next()
		if err == io.EOF || header == nil {
			break
		}
		if err != nil {
			return err
		}
		if header.Name != want {
			continue
		}
		// 100 MB for the single entry we actually serve — generous for any
		// legitimate page scan (most are well under 5 MB) while keeping a
		// decompression-bomb entry from ballooning server memory.
		data, rerr := io.ReadAll(io.LimitReader(rr, maxBookPageBytes))
		if rerr != nil {
			return rerr
		}
		w.Header().Set("Content-Type", contentTypeForBookPage(header.Name))
		w.Header().Set("Cache-Control", "private, max-age=3600, immutable")
		_, err = w.Write(data)
		return err
	}
	return errBookPageNotFound
}

const (
	// maxBookPageBytes caps a single decompressed page.
	maxBookPageBytes = 100 << 20
	// maxCBRPageEntries caps how many image entries we will enumerate, so a
	// crafted archive declaring millions of tiny entries can't grow the name
	// slice without bound. No real comic approaches this.
	maxCBRPageEntries = 10_000
)

// cbrPageNames walks the archive reading entry names only, never buffering
// entry bodies.
func cbrPageNames(ctx context.Context, cbrPath string) ([]string, error) {
	f, err := os.Open(cbrPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rr, err := rardecode.NewReader(f)
	if err != nil {
		return nil, err
	}
	var names []string
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := rr.Next()
		if err == io.EOF || header == nil {
			break
		}
		if err != nil {
			return nil, err
		}
		if !isCBZPageEntryAPI(header.Name) {
			continue
		}
		names = append(names, header.Name)
		if len(names) >= maxCBRPageEntries {
			break
		}
	}
	return names, nil
}

// isCBZPageEntryAPI mirrors the scanner's isCBZPageEntry — duplicated
// here to keep the api package free of an internal/scanner import
// (we don't want the API depending on the scanner package). Cheaper
// than wiring a shared internal/cbz package for two callers.
func isCBZPageEntryAPI(name string) bool {
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
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".avif":
		return true
	}
	return false
}

func contentTypeForBookPage(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".avif":
		return "image/avif"
	}
	// Browsers typically sniff if Content-Type is missing/wrong, but a
	// generic octet-stream is cleaner than guessing JPEG.
	return "application/octet-stream"
}

// checkLibraryAccess: same pattern as the other handlers. Translates a
// missing access checker (dev setups) to "everyone can see everything,"
// otherwise consults the per-user grant map.
func (h *BookHandler) checkLibraryAccess(w http.ResponseWriter, r *http.Request, libraryID uuid.UUID) bool {
	if h.access == nil {
		return true
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return false
	}
	allowed, err := h.access.AllowedLibraryIDs(r.Context(), claims.UserID, claims.IsAdmin)
	if err != nil {
		respond.InternalError(w, r)
		return false
	}
	if allowed != nil {
		if _, ok := allowed[libraryID]; !ok {
			respond.NotFound(w, r)
			return false
		}
	}
	return true
}
