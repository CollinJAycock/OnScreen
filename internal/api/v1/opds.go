package v1

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/mediastore"
)

// OPDSDB is the store the OPDS catalog reads. Satisfied by *gen.Queries.
type OPDSDB interface {
	ListLibraries(ctx context.Context) ([]gen.Library, error)
	GetLibrary(ctx context.Context, id uuid.UUID) (gen.Library, error)
	ListOPDSBooks(ctx context.Context, arg gen.ListOPDSBooksParams) ([]gen.ListOPDSBooksRow, error)
	GetMediaFile(ctx context.Context, id uuid.UUID) (gen.MediaFile, error)
	GetMediaItem(ctx context.Context, id uuid.UUID) (gen.GetMediaItemRow, error)
}

// OPDSHandler serves the OPDS 1.2 catalog of book libraries under /opds/ for
// e-reader apps (KOReader, Moon+ Reader, Panels, Chunky): a navigation feed
// of the caller's book libraries plus "Recently added", paged acquisition
// feeds per library, and OpenSearch. See docs/opds.md.
//
// Visibility is the same as everywhere else: only book libraries the caller
// may open, and within them only books under their rating ceiling and label
// restriction. Downloads go through the existing /media/download/{id} route
// — with its own library, rating, label, watch-limit and downloads-toggle
// checks — via a stream token bound to that one file, since reader apps
// don't reliably send credentials on acquisition links.
type OPDSHandler struct {
	db      OPDSDB
	access  LibraryAccessChecker
	tokens  *auth.TokenMaker
	dlGate  DownloadGate
	artwork func() mediastore.Store
	logger  *slog.Logger
}

// NewOPDSHandler builds the handler. tokens mints the per-file stream tokens
// on acquisition and cover links.
func NewOPDSHandler(db OPDSDB, tokens *auth.TokenMaker, logger *slog.Logger) *OPDSHandler {
	return &OPDSHandler{db: db, tokens: tokens, logger: logger}
}

// WithLibraryAccess wires the per-user library filter (required in
// production; see api.Handlers.ValidateLibraryAccess).
func (h *OPDSHandler) WithLibraryAccess(a LibraryAccessChecker) *OPDSHandler {
	h.access = a
	return h
}

// LibraryAccessWired reports whether the library-ACL checker is wired.
func (h *OPDSHandler) LibraryAccessWired() bool { return h.access != nil }

// WithDownloadGate wires the server-wide downloads toggle. OPDS exists to
// hand book files to another app, so while downloads are off the whole
// catalog answers 403 DOWNLOADS_DISABLED instead of listing books whose
// every download link would fail.
func (h *OPDSHandler) WithDownloadGate(g DownloadGate) *OPDSHandler {
	h.dlGate = g
	return h
}

// WithArtworkStore sets where covers are read from (the artwork manager's
// store, so covers in object storage work too). Default: local disk.
func (h *OPDSHandler) WithArtworkStore(s func() mediastore.Store) *OPDSHandler {
	h.artwork = s
	return h
}

const (
	opdsPageSize    = 50
	opdsAtomNS      = "http://www.w3.org/2005/Atom"
	opdsCatalogNS   = "http://opds-spec.org/2010/catalog"
	opdsDCNS        = "http://purl.org/dc/terms/"
	opdsSearchNS    = "http://a9.com/-/spec/opensearch/1.1/"
	opdsTypeNav     = "application/atom+xml;profile=opds-catalog;kind=navigation"
	opdsTypeAcq     = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	opdsTypeSearch  = "application/opensearchdescription+xml"
	opdsRelAcquire  = "http://opds-spec.org/acquisition"
	opdsRelImage    = "http://opds-spec.org/image"
	opdsRelThumb    = "http://opds-spec.org/image/thumbnail"
	opdsRelSortNew  = "http://opds-spec.org/sort/new"
	opdsIDPrefix    = "urn:onscreen:opds:"
	opdsCatalogPath = "/opds/"
)

type atomFeed struct {
	XMLName  xml.Name    `xml:"feed"`
	Xmlns    string      `xml:"xmlns,attr"`
	XmlnsDC  string      `xml:"xmlns:dc,attr"`
	XmlnsOS  string      `xml:"xmlns:opensearch,attr"`
	XmlnsOPD string      `xml:"xmlns:opds,attr"`
	ID       string      `xml:"id"`
	Title    string      `xml:"title"`
	Updated  string      `xml:"updated"`
	Author   *atomPerson `xml:"author,omitempty"`
	Links    []atomLink  `xml:"link"`
	Entries  []atomEntry `xml:"entry"`
}

type atomPerson struct {
	Name string `xml:"name"`
}

type atomLink struct {
	Rel   string `xml:"rel,attr,omitempty"`
	Href  string `xml:"href,attr"`
	Type  string `xml:"type,attr,omitempty"`
	Title string `xml:"title,attr,omitempty"`
}

type atomText struct {
	Type string `xml:"type,attr,omitempty"`
	Body string `xml:",chardata"`
}

type atomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr,omitempty"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	ID         string         `xml:"id"`
	Updated    string         `xml:"updated"`
	Authors    []atomPerson   `xml:"author"`
	Issued     string         `xml:"dc:issued,omitempty"`
	Categories []atomCategory `xml:"category"`
	Summary    *atomText      `xml:"summary,omitempty"`
	Content    *atomText      `xml:"content,omitempty"`
	Links      []atomLink     `xml:"link"`
}

func newFeed(id, title string) *atomFeed {
	return &atomFeed{
		Xmlns: opdsAtomNS, XmlnsDC: opdsDCNS, XmlnsOS: opdsSearchNS, XmlnsOPD: opdsCatalogNS,
		ID:      opdsIDPrefix + id,
		Title:   title,
		Updated: time.Now().UTC().Format(time.RFC3339),
		Author:  &atomPerson{Name: "OnScreen"},
	}
}

// commonLinks are the start + search links every feed carries.
func commonLinks(self, selfType string) []atomLink {
	return []atomLink{
		{Rel: "self", Href: self, Type: selfType},
		{Rel: "start", Href: opdsCatalogPath, Type: opdsTypeNav},
		{Rel: "search", Href: "/opds/opensearch.xml", Type: opdsTypeSearch},
	}
}

func writeFeed(w http.ResponseWriter, contentType string, f *atomFeed) {
	w.Header().Set("Content-Type", contentType+";charset=utf-8")
	// Per-user content; never shared-cached.
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	_ = enc.Encode(f)
}

// ready runs the checks every OPDS route shares: an authenticated caller and
// downloads switched on. It writes the refusal and returns nil otherwise.
func (h *OPDSHandler) ready(w http.ResponseWriter, r *http.Request) *auth.Claims {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return nil
	}
	if h.dlGate != nil && !h.dlGate.WebDownloadsEnabled(r.Context()) {
		respond.Error(w, r, http.StatusForbidden, "DOWNLOADS_DISABLED",
			"file downloads, and with them the OPDS catalog, are disabled on this server")
		return nil
	}
	return claims
}

// bookLibraries returns the book libraries the caller may open, by name.
func (h *OPDSHandler) bookLibraries(ctx context.Context, claims *auth.Claims) ([]gen.Library, error) {
	libs, err := h.db.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	var allowed map[uuid.UUID]struct{}
	if h.access != nil {
		allowed, err = h.access.AllowedLibraryIDs(ctx, claims.UserID, claims.IsAdmin)
		if err != nil {
			return nil, err
		}
	}
	out := libs[:0:0]
	for _, l := range libs {
		if l.Type != "book" {
			continue
		}
		if allowed != nil {
			if _, ok := allowed[l.ID]; !ok {
				continue
			}
		}
		out = append(out, l)
	}
	return out, nil
}

// Root handles GET /opds/: the navigation feed.
func (h *OPDSHandler) Root(w http.ResponseWriter, r *http.Request) {
	claims := h.ready(w, r)
	if claims == nil {
		return
	}
	libs, err := h.bookLibraries(r.Context(), claims)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "opds: libraries", "err", err)
		respond.InternalError(w, r)
		return
	}
	f := newFeed("root", "OnScreen books")
	f.Links = commonLinks(opdsCatalogPath, opdsTypeNav)
	now := f.Updated
	f.Entries = append(f.Entries, atomEntry{
		Title:   "Recently added",
		ID:      opdsIDPrefix + "recent",
		Updated: now,
		Content: &atomText{Type: "text", Body: "The newest books in every library you can read."},
		Links:   []atomLink{{Rel: opdsRelSortNew, Href: "/opds/recent", Type: opdsTypeAcq}},
	})
	for _, l := range libs {
		f.Entries = append(f.Entries, atomEntry{
			Title:   l.Name,
			ID:      opdsIDPrefix + "library:" + l.ID.String(),
			Updated: now,
			Content: &atomText{Type: "text", Body: "All books in " + l.Name + ", by title."},
			Links:   []atomLink{{Rel: "subsection", Href: "/opds/libraries/" + l.ID.String(), Type: opdsTypeAcq}},
		})
	}
	writeFeed(w, opdsTypeNav, f)
}

// Library handles GET /opds/libraries/{id}?page=N: one book library by
// title, 50 per page.
func (h *OPDSHandler) Library(w http.ResponseWriter, r *http.Request) {
	claims := h.ready(w, r)
	if claims == nil {
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	libs, err := h.bookLibraries(r.Context(), claims)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "opds: libraries", "err", err)
		respond.InternalError(w, r)
		return
	}
	var lib *gen.Library
	for i := range libs {
		if libs[i].ID == id {
			lib = &libs[i]
		}
	}
	if lib == nil {
		// Not a book library, or not the caller's: indistinguishable.
		respond.NotFound(w, r)
		return
	}
	self := "/opds/libraries/" + id.String()
	h.acquisitionFeed(w, r, claims, "library:"+id.String(), lib.Name, self, "",
		[]uuid.UUID{id}, nil, "title")
}

// Recent handles GET /opds/recent?page=N: the newest books across the
// caller's book libraries.
func (h *OPDSHandler) Recent(w http.ResponseWriter, r *http.Request) {
	claims := h.ready(w, r)
	if claims == nil {
		return
	}
	ids, ok := h.bookLibraryIDs(w, r, claims)
	if !ok {
		return
	}
	h.acquisitionFeed(w, r, claims, "recent", "Recently added", "/opds/recent", "", ids, nil, "recent")
}

// Search handles GET /opds/search?q=…&page=N.
func (h *OPDSHandler) Search(w http.ResponseWriter, r *http.Request) {
	claims := h.ready(w, r)
	if claims == nil {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		respond.BadRequest(w, r, "q is required")
		return
	}
	if len(q) > 200 {
		q = q[:200]
	}
	ids, ok := h.bookLibraryIDs(w, r, claims)
	if !ok {
		return
	}
	h.acquisitionFeed(w, r, claims, "search", "Search: "+q, "/opds/search",
		"q="+url.QueryEscape(q), ids, &q, "title")
}

func (h *OPDSHandler) bookLibraryIDs(w http.ResponseWriter, r *http.Request, claims *auth.Claims) ([]uuid.UUID, bool) {
	libs, err := h.bookLibraries(r.Context(), claims)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "opds: libraries", "err", err)
		respond.InternalError(w, r)
		return nil, false
	}
	ids := make([]uuid.UUID, len(libs))
	for i, l := range libs {
		ids[i] = l.ID
	}
	return ids, true
}

// acquisitionFeed renders one page of books. extraQuery is carried on the
// self/next/previous links (the search term).
func (h *OPDSHandler) acquisitionFeed(w http.ResponseWriter, r *http.Request, claims *auth.Claims,
	id, title, path, extraQuery string, libIDs []uuid.UUID, query *string, sort string) {
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 1 && p <= 10000 {
		page = p
	}
	var rows []gen.ListOPDSBooksRow
	if len(libIDs) > 0 {
		var err error
		rows, err = h.db.ListOPDSBooks(r.Context(), gen.ListOPDSBooksParams{
			LibraryIds:    libIDs,
			Query:         query,
			MaxRatingRank: maxRatingRankFromClaims(claims.MaxContentRating),
			BlockedLabels: claims.LabelFilter().BlockedArg(),
			AllowedLabels: claims.LabelFilter().AllowedArg(),
			Sort:          sort,
			Off:           int32((page - 1) * opdsPageSize),
			Lim:           opdsPageSize + 1, // one extra row says there's a next page
		})
		if err != nil {
			h.logger.ErrorContext(r.Context(), "opds: list books", "feed", id, "err", err)
			respond.InternalError(w, r)
			return
		}
	}
	hasNext := len(rows) > opdsPageSize
	if hasNext {
		rows = rows[:opdsPageSize]
	}
	pageURL := func(n int) string {
		v := extraQuery
		if n > 1 {
			if v != "" {
				v += "&"
			}
			v += "page=" + strconv.Itoa(n)
		}
		if v == "" {
			return path
		}
		return path + "?" + v
	}

	f := newFeed(id, title)
	f.Links = commonLinks(pageURL(page), opdsTypeAcq)
	f.Links = append(f.Links, atomLink{Rel: "up", Href: opdsCatalogPath, Type: opdsTypeNav})
	if page > 1 {
		f.Links = append(f.Links, atomLink{Rel: "previous", Href: pageURL(page - 1), Type: opdsTypeAcq})
	}
	if hasNext {
		f.Links = append(f.Links, atomLink{Rel: "next", Href: pageURL(page + 1), Type: opdsTypeAcq})
	}
	for _, row := range rows {
		f.Entries = append(f.Entries, h.bookEntry(r, claims, row))
	}
	writeFeed(w, opdsTypeAcq, f)
}

// bookEntry builds one acquisition entry. The download and cover links each
// carry a stream token bound to the book's file (24 h, revoked with the
// session or API key that minted it), so they work in a reader app that
// doesn't send credentials on links.
func (h *OPDSHandler) bookEntry(r *http.Request, claims *auth.Claims, b gen.ListOPDSBooksRow) atomEntry {
	updated := b.UpdatedAt.Time
	if updated.IsZero() {
		updated = b.CreatedAt.Time
	}
	e := atomEntry{
		Title:   b.Title,
		ID:      "urn:uuid:" + b.ID.String(),
		Updated: updated.UTC().Format(time.RFC3339),
	}
	if b.OriginalTitle != nil && strings.TrimSpace(*b.OriginalTitle) != "" {
		// Book items keep their author in original_title (as audiobooks do).
		e.Authors = []atomPerson{{Name: strings.TrimSpace(*b.OriginalTitle)}}
	}
	if b.Year != nil && *b.Year > 0 {
		e.Issued = fmt.Sprintf("%04d", *b.Year)
	}
	for _, g := range b.Genres {
		e.Categories = append(e.Categories, atomCategory{Term: g, Label: g})
	}
	if b.Summary != nil && *b.Summary != "" {
		e.Summary = &atomText{Type: "text", Body: *b.Summary}
	}

	token := ""
	if h.tokens != nil {
		if t, err := h.tokens.IssueStreamToken(*claims, b.FileID); err == nil {
			token = "?token=" + url.QueryEscape(t)
		} else {
			h.logger.WarnContext(r.Context(), "opds: issue stream token", "file_id", b.FileID, "err", err)
		}
	}
	e.Links = append(e.Links, atomLink{
		Rel:   opdsRelAcquire,
		Href:  "/media/download/" + b.FileID.String() + token,
		Type:  bookMIMEType(b.FilePath),
		Title: "Download",
	})
	if b.PosterPath != nil && *b.PosterPath != "" {
		cover := "/opds/files/" + b.FileID.String() + "/cover" + token
		e.Links = append(e.Links,
			atomLink{Rel: opdsRelImage, Href: cover, Type: artworkMIMEType(*b.PosterPath)},
			atomLink{Rel: opdsRelThumb, Href: cover, Type: artworkMIMEType(*b.PosterPath)},
		)
	}
	return e
}

// bookMIMEType maps a book file to the media type its acquisition link
// advertises — readers pick the file extension and the opener from it.
func bookMIMEType(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".epub":
		return "application/epub+zip"
	case ".pdf":
		return "application/pdf"
	case ".cbz":
		return "application/vnd.comicbook+zip"
	case ".cbr":
		return "application/vnd.comicbook-rar"
	case ".mobi":
		return "application/x-mobipocket-ebook"
	case ".azw3":
		return "application/vnd.amazon.ebook"
	case ".fb2":
		return "application/x-fictionbook+xml"
	case ".djvu":
		return "image/vnd.djvu"
	}
	return "application/octet-stream"
}

func artworkMIMEType(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	}
	return "image/jpeg"
}

// OpenSearch handles GET /opds/opensearch.xml.
func (h *OPDSHandler) OpenSearch(w http.ResponseWriter, r *http.Request) {
	if h.ready(w, r) == nil {
		return
	}
	type osURL struct {
		Type     string `xml:"type,attr"`
		Template string `xml:"template,attr"`
	}
	type osDesc struct {
		XMLName       xml.Name `xml:"OpenSearchDescription"`
		Xmlns         string   `xml:"xmlns,attr"`
		ShortName     string   `xml:"ShortName"`
		Description   string   `xml:"Description"`
		InputEncoding string   `xml:"InputEncoding"`
		URL           osURL    `xml:"Url"`
	}
	w.Header().Set("Content-Type", opdsTypeSearch+";charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(osDesc{
		Xmlns:         opdsSearchNS,
		ShortName:     "OnScreen",
		Description:   "Search the books on this OnScreen server",
		InputEncoding: "UTF-8",
		URL:           osURL{Type: opdsTypeAcq, Template: "/opds/search?q={searchTerms}"},
	})
}

// Cover handles GET /opds/files/{fileId}/cover: the cover of the book that
// file belongs to, under the same library, rating and label checks as the
// file itself. Keyed by the file (not the item) so the stream token on the
// feed's links — bound to that file — authenticates it.
func (h *OPDSHandler) Cover(w http.ResponseWriter, r *http.Request) {
	claims := h.ready(w, r)
	if claims == nil {
		return
	}
	fileID, err := parseUUID(r, "fileId")
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	file, err := h.db.GetMediaFile(r.Context(), fileID)
	if err != nil || file.Status != "active" {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			h.logger.ErrorContext(r.Context(), "opds cover: file", "file_id", fileID, "err", err)
		}
		respond.NotFound(w, r)
		return
	}
	item, err := h.db.GetMediaItem(r.Context(), file.MediaItemID)
	if err != nil || item.Type != "book" || item.PosterPath == nil || *item.PosterPath == "" {
		respond.NotFound(w, r)
		return
	}
	if h.access != nil {
		ok, err := h.access.CanAccessLibrary(r.Context(), claims.UserID, item.LibraryID, claims.IsAdmin)
		if err != nil || !ok {
			respond.NotFound(w, r)
			return
		}
	}
	cr := ""
	if item.ContentRating != nil {
		cr = *item.ContentRating
	}
	if !itemPermitted(r.Context(), claims, item.ID, cr) {
		respond.NotFound(w, r)
		return
	}
	rel := filepath.Clean(filepath.FromSlash(*item.PosterPath))
	// Images only (the artwork route's rule): poster_path comes from the
	// scanner, but this route must never become a way to read other files.
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
	default:
		respond.NotFound(w, r)
		return
	}
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		respond.NotFound(w, r)
		return
	}
	lib, err := h.db.GetLibrary(r.Context(), item.LibraryID)
	if err != nil {
		respond.NotFound(w, r)
		return
	}
	store := mediastore.Store(mediastore.Local{})
	if h.artwork != nil {
		store = h.artwork()
	}
	for _, root := range lib.ScanPaths {
		abs := filepath.Join(root, rel)
		if _, err := store.Stat(r.Context(), abs); err != nil {
			continue
		}
		w.Header().Set("Content-Type", artworkMIMEType(rel))
		w.Header().Set("Cache-Control", "private, max-age=86400")
		if err := mediastore.Serve(w, r, store, abs, filepath.Base(abs), 15*time.Minute); err != nil &&
			!respond.IsClientGone(err) {
			h.logger.WarnContext(r.Context(), "opds cover: serve", "file_id", fileID, "err", err)
		}
		return
	}
	respond.NotFound(w, r)
}
