package v1

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
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

// servePage opens the CBZ at file.FilePath, finds the pageNum-th image
// entry (sorted lexicographically), and streams it to w with the
// appropriate Content-Type header. Cacheable for an hour — pages
// don't change without the underlying file changing, and the URL
// embeds the page number so a different page is a different URL.
func (h *BookHandler) servePage(_ context.Context, w http.ResponseWriter, file media.File, pageNum int) error {
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
