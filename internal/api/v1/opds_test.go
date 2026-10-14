package v1

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

type fakeOPDSDB struct {
	libs    []gen.Library
	books   []gen.ListOPDSBooksRow
	listArg gen.ListOPDSBooksParams
	files   map[uuid.UUID]gen.MediaFile
	items   map[uuid.UUID]gen.GetMediaItemRow
}

func (f *fakeOPDSDB) ListLibraries(context.Context) ([]gen.Library, error) { return f.libs, nil }
func (f *fakeOPDSDB) GetLibrary(_ context.Context, id uuid.UUID) (gen.Library, error) {
	for _, l := range f.libs {
		if l.ID == id {
			return l, nil
		}
	}
	return gen.Library{}, pgx.ErrNoRows
}
func (f *fakeOPDSDB) ListOPDSBooks(_ context.Context, arg gen.ListOPDSBooksParams) ([]gen.ListOPDSBooksRow, error) {
	f.listArg = arg
	end := min(int(arg.Off)+int(arg.Lim), len(f.books))
	if int(arg.Off) >= len(f.books) {
		return nil, nil
	}
	return f.books[arg.Off:end], nil
}
func (f *fakeOPDSDB) GetMediaFile(_ context.Context, id uuid.UUID) (gen.MediaFile, error) {
	if m, ok := f.files[id]; ok {
		return m, nil
	}
	return gen.MediaFile{}, pgx.ErrNoRows
}
func (f *fakeOPDSDB) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	if m, ok := f.items[id]; ok {
		return m, nil
	}
	return gen.GetMediaItemRow{}, pgx.ErrNoRows
}

type opdsGate struct{ on bool }

func (g opdsGate) WebDownloadsEnabled(context.Context) bool { return g.on }

func opdsReq(t *testing.T, claims *auth.Claims, target string, params map[string]string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if claims != nil {
		r = r.WithContext(middleware.WithClaims(r.Context(), claims))
	}
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func newOPDSTest(t *testing.T, db *fakeOPDSDB, allowed map[uuid.UUID]struct{}) (*OPDSHandler, *auth.TokenMaker) {
	t.Helper()
	tm, err := auth.NewTokenMaker(auth.DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewOPDSHandler(db, tm, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithLibraryAccess(&fakePlaylistAccess{allowed: allowed}).
		WithDownloadGate(opdsGate{on: true})
	return h, tm
}

type parsedFeed struct {
	Links []struct {
		Rel  string `xml:"rel,attr"`
		Href string `xml:"href,attr"`
		Type string `xml:"type,attr"`
	} `xml:"link"`
	Entries []struct {
		Title  string `xml:"title"`
		Author []struct {
			Name string `xml:"name"`
		} `xml:"author"`
		Links []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
			Type string `xml:"type,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

func TestOPDS_RootListsOnlyVisibleBookLibraries(t *testing.T) {
	books, hiddenBooks, movies := uuid.New(), uuid.New(), uuid.New()
	db := &fakeOPDSDB{libs: []gen.Library{
		{ID: books, Name: "Books", Type: "book"},
		{ID: hiddenBooks, Name: "Private Books", Type: "book"},
		{ID: movies, Name: "Movies", Type: "movie"},
	}}
	h, _ := newOPDSTest(t, db, map[uuid.UUID]struct{}{books: {}, movies: {}})
	rec := httptest.NewRecorder()
	h.Root(rec, opdsReq(t, &auth.Claims{UserID: uuid.New()}, "/opds/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "kind=navigation") {
		t.Errorf("content type %q", ct)
	}
	var f parsedFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatalf("not XML: %v", err)
	}
	var titles []string
	for _, e := range f.Entries {
		titles = append(titles, e.Title)
	}
	if strings.Join(titles, "|") != "Recently added|Books" {
		t.Errorf("entries = %v, want Recently added + the one visible book library", titles)
	}
	hasSearch := false
	for _, l := range f.Links {
		hasSearch = hasSearch || (l.Rel == "search" && l.Href == "/opds/opensearch.xml")
	}
	if !hasSearch {
		t.Error("no search link")
	}
}

func TestOPDS_LibraryFeed(t *testing.T) {
	lib := uuid.New()
	author := "Ursula K. Le Guin"
	poster := "Le Guin/abc-poster.jpg"
	db := &fakeOPDSDB{libs: []gen.Library{{ID: lib, Name: "Books", Type: "book"}}}
	for i := 0; i < opdsPageSize+3; i++ {
		db.books = append(db.books, gen.ListOPDSBooksRow{
			ID: uuid.New(), LibraryID: lib, Title: "Book", OriginalTitle: &author, PosterPath: &poster,
			FileID: uuid.New(), FilePath: "/srv/books/x.epub",
		})
	}
	h, tm := newOPDSTest(t, db, map[uuid.UUID]struct{}{lib: {}})
	claims := &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG", BlockedLabels: []string{"adult"}}

	rec := httptest.NewRecorder()
	h.Library(rec, opdsReq(t, claims, "/opds/libraries/"+lib.String(), map[string]string{"id": lib.String()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var f parsedFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Entries) != opdsPageSize {
		t.Fatalf("entries = %d, want a full page", len(f.Entries))
	}
	next := ""
	for _, l := range f.Links {
		if l.Rel == "next" {
			next = l.Href
		}
	}
	if next != "/opds/libraries/"+lib.String()+"?page=2" {
		t.Errorf("next = %q", next)
	}
	// Visibility reached the query.
	if db.listArg.MaxRatingRank == nil || len(db.listArg.BlockedLabels) != 1 ||
		len(db.listArg.LibraryIds) != 1 || db.listArg.LibraryIds[0] != lib {
		t.Errorf("query visibility = %+v", db.listArg)
	}
	e := f.Entries[0]
	if len(e.Author) != 1 || e.Author[0].Name != author {
		t.Errorf("author = %+v", e.Author)
	}
	var acq, img string
	for _, l := range e.Links {
		switch l.Rel {
		case "http://opds-spec.org/acquisition":
			acq = l.Href
			if l.Type != "application/epub+zip" {
				t.Errorf("acquisition type %q", l.Type)
			}
		case "http://opds-spec.org/image":
			img = l.Href
		}
	}
	wantPrefix := "/media/download/" + db.books[0].FileID.String() + "?token="
	if !strings.HasPrefix(acq, wantPrefix) {
		t.Fatalf("acquisition href = %q", acq)
	}
	// The token is a stream token bound to that one file, carrying the
	// caller's restrictions.
	u, _ := url.Parse(acq)
	tc, err := tm.ValidateAccessToken(u.Query().Get("token"))
	if err != nil || tc.Purpose != "stream" || tc.FileID == nil || *tc.FileID != db.books[0].FileID ||
		tc.MaxContentRating != "PG" {
		t.Fatalf("token claims = %+v err %v", tc, err)
	}
	if !strings.HasPrefix(img, "/opds/files/"+db.books[0].FileID.String()+"/cover?token=") {
		t.Errorf("cover href = %q", img)
	}

	// Page 2 has the remainder and a previous link, no next.
	rec = httptest.NewRecorder()
	h.Library(rec, opdsReq(t, claims, "/opds/libraries/x?page=2", map[string]string{"id": lib.String()}))
	f = parsedFeed{}
	_ = xml.Unmarshal(rec.Body.Bytes(), &f)
	if len(f.Entries) != 3 {
		t.Errorf("page 2 entries = %d", len(f.Entries))
	}
}

func TestOPDS_HiddenLibrary404AndDownloadsOff403(t *testing.T) {
	lib := uuid.New()
	db := &fakeOPDSDB{libs: []gen.Library{{ID: lib, Name: "Books", Type: "book"}}}
	h, _ := newOPDSTest(t, db, map[uuid.UUID]struct{}{})
	rec := httptest.NewRecorder()
	h.Library(rec, opdsReq(t, &auth.Claims{UserID: uuid.New()}, "/", map[string]string{"id": lib.String()}))
	if rec.Code != http.StatusNotFound {
		t.Errorf("hidden library: status %d, want 404", rec.Code)
	}
	h.WithDownloadGate(opdsGate{on: false})
	rec = httptest.NewRecorder()
	h.Root(rec, opdsReq(t, &auth.Claims{UserID: uuid.New()}, "/opds/", nil))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "DOWNLOADS_DISABLED") {
		t.Errorf("downloads off: status %d %s", rec.Code, rec.Body.String())
	}
}

func TestOPDS_Cover(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "A"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "A", "b-poster.jpg"), []byte("JPEGDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, other := uuid.New(), uuid.New()
	fileID, itemID := uuid.New(), uuid.New()
	poster := "A/b-poster.jpg"
	rated := "R"
	db := &fakeOPDSDB{
		libs:  []gen.Library{{ID: lib, Type: "book", ScanPaths: []string{root}}},
		files: map[uuid.UUID]gen.MediaFile{fileID: {ID: fileID, MediaItemID: itemID, Status: "active"}},
		items: map[uuid.UUID]gen.GetMediaItemRow{itemID: {ID: itemID, LibraryID: lib, Type: "book", PosterPath: &poster, ContentRating: &rated}},
	}
	get := func(allowed map[uuid.UUID]struct{}, claims *auth.Claims) *httptest.ResponseRecorder {
		h, _ := newOPDSTest(t, db, allowed)
		rec := httptest.NewRecorder()
		h.Cover(rec, opdsReq(t, claims, "/", map[string]string{"fileId": fileID.String()}))
		return rec
	}
	if rec := get(map[uuid.UUID]struct{}{lib: {}}, &auth.Claims{UserID: uuid.New()}); rec.Code != http.StatusOK || rec.Body.String() != "JPEGDATA" {
		t.Fatalf("allowed: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(map[uuid.UUID]struct{}{other: {}}, &auth.Claims{UserID: uuid.New()}); rec.Code != http.StatusNotFound {
		t.Errorf("no library access: %d", rec.Code)
	}
	if rec := get(map[uuid.UUID]struct{}{lib: {}}, &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG"}); rec.Code != http.StatusNotFound {
		t.Errorf("over the rating ceiling: %d", rec.Code)
	}
	// A poster path that isn't an image is never served.
	bad := "A/../../etc/passwd"
	it := db.items[itemID]
	it.PosterPath = &bad
	db.items[itemID] = it
	if rec := get(map[uuid.UUID]struct{}{lib: {}}, &auth.Claims{UserID: uuid.New()}); rec.Code != http.StatusNotFound {
		t.Errorf("non-image path: %d", rec.Code)
	}
}

func TestBookMIMEType(t *testing.T) {
	for p, want := range map[string]string{
		"a.EPUB": "application/epub+zip", "a.pdf": "application/pdf",
		"a.cbz": "application/vnd.comicbook+zip", "a.cbr": "application/vnd.comicbook-rar",
		"a.xyz": "application/octet-stream",
	} {
		if got := bookMIMEType(p); got != want {
			t.Errorf("%s: %s, want %s", p, got, want)
		}
	}
}
