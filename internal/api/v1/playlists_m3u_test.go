package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// fakeM3UFile is one media file the fake M3U store knows.
type fakeM3UFile struct {
	path    string
	itemID  uuid.UUID
	title   string
	artist  string
	library uuid.UUID
}

// fakeM3UDB answers the M3U queries from an in-memory file list, applying the
// library_ids visibility argument the way the SQL does (the rating and label
// arguments are recorded for assertions).
type fakeM3UDB struct {
	libs  []gen.Library
	files []fakeM3UFile

	exportRows []gen.ListM3UExportRowsRow
	exportArg  gen.ListM3UExportRowsParams

	pathArgs   []gen.FindM3UFilesByPathParams
	baseArg    gen.FindM3UFilesByBasenameParams
	titleArg   gen.FindM3UTracksByTitleParams
	appendArgs []gen.AppendCollectionItemsParams
}

func (f *fakeM3UDB) visible(lib uuid.UUID, ids []uuid.UUID) bool {
	return ids == nil || slices.Contains(ids, lib)
}

func (f *fakeM3UDB) ListLibraries(context.Context) ([]gen.Library, error) { return f.libs, nil }
func (f *fakeM3UDB) ListM3UExportRows(_ context.Context, arg gen.ListM3UExportRowsParams) ([]gen.ListM3UExportRowsRow, error) {
	f.exportArg = arg
	return f.exportRows, nil
}
func (f *fakeM3UDB) FindM3UFilesByPath(_ context.Context, arg gen.FindM3UFilesByPathParams) ([]gen.FindM3UFilesByPathRow, error) {
	f.pathArgs = append(f.pathArgs, arg)
	var out []gen.FindM3UFilesByPathRow
	for _, fl := range f.files {
		if slices.Contains(arg.Paths, fl.path) && f.visible(fl.library, arg.LibraryIds) {
			out = append(out, gen.FindM3UFilesByPathRow{FilePath: fl.path, MediaItemID: fl.itemID, Title: fl.title})
		}
	}
	return out, nil
}
func (f *fakeM3UDB) FindM3UFilesByBasename(_ context.Context, arg gen.FindM3UFilesByBasenameParams) ([]gen.FindM3UFilesByBasenameRow, error) {
	f.baseArg = arg
	var out []gen.FindM3UFilesByBasenameRow
	for _, fl := range f.files {
		i := strings.LastIndexAny(fl.path, `/\`)
		if slices.Contains(arg.Basenames, strings.ToLower(fl.path[i+1:])) && f.visible(fl.library, arg.LibraryIds) {
			out = append(out, gen.FindM3UFilesByBasenameRow{FilePath: fl.path, MediaItemID: fl.itemID, Title: fl.title})
		}
	}
	return out, nil
}
func (f *fakeM3UDB) FindM3UTracksByTitle(_ context.Context, arg gen.FindM3UTracksByTitleParams) ([]gen.FindM3UTracksByTitleRow, error) {
	f.titleArg = arg
	var out []gen.FindM3UTracksByTitleRow
	for _, fl := range f.files {
		if slices.Contains(arg.Titles, strings.ToLower(fl.title)) && f.visible(fl.library, arg.LibraryIds) {
			out = append(out, gen.FindM3UTracksByTitleRow{ID: fl.itemID, Title: fl.title, Artist: fl.artist})
		}
	}
	return out, nil
}
func (f *fakeM3UDB) AppendCollectionItems(_ context.Context, arg gen.AppendCollectionItemsParams) error {
	f.appendArgs = append(f.appendArgs, arg)
	return nil
}

func TestPlaylists_ExportM3U(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	col.Name = "Road trip"
	musicLib, hiddenLib := uuid.New(), uuid.New()
	visible, hidden := uuid.New(), uuid.New()
	secs := int64(245_400)
	db := &mockPlaylistDB{
		getResult: col,
		listItems: []gen.ListCollectionItemsRow{
			{ID: visible, LibraryID: musicLib, Title: "One More Time", Type: "track"},
			{ID: hidden, LibraryID: hiddenLib, Title: "Secret", Type: "track"},
		},
	}
	m3uDB := &fakeM3UDB{exportRows: []gen.ListM3UExportRowsRow{{
		ID: visible, LibraryID: musicLib, Type: "track", Title: "One More Time", Artist: "Daft Punk",
		FilePath: "/mnt/music/Daft Punk/Discovery/01.flac", FileDurationMs: &secs,
		ScanPaths: []string{"/mnt/music"},
	}}}
	access := &fakePlaylistAccess{allowed: map[uuid.UUID]struct{}{musicLib: {}}}
	h := newPlaylistHandler(db).WithLibraryAccess(access).WithM3U(m3uDB)

	for _, tc := range []struct {
		query, wantPath string
	}{
		{"", "Daft Punk/Discovery/01.flac"},
		{"?paths=absolute", "/mnt/music/Daft Punk/Discovery/01.flac"},
	} {
		req := plReq(http.MethodGet, "", uid, col.ID.String(), "")
		req.URL.RawQuery = strings.TrimPrefix(tc.query, "?")
		rec := httptest.NewRecorder()
		h.ExportM3U(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", tc.query, rec.Code, rec.Body.String())
		}
		want := "#EXTM3U\n#EXTINF:245,Daft Punk - One More Time\n" + tc.wantPath + "\n"
		if rec.Body.String() != want {
			t.Errorf("%s: body %q, want %q", tc.query, rec.Body.String(), want)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="Road trip.m3u8"`) {
			t.Errorf("Content-Disposition = %q", cd)
		}
	}
	// The hidden library's item never reached the export query, and the
	// query got the caller's library scope.
	if !reflect.DeepEqual(m3uDB.exportArg.ItemIds, []uuid.UUID{visible}) {
		t.Errorf("export item ids = %v, want only the visible one", m3uDB.exportArg.ItemIds)
	}
	if !reflect.DeepEqual(m3uDB.exportArg.LibraryIds, []uuid.UUID{musicLib}) {
		t.Errorf("export library ids = %v", m3uDB.exportArg.LibraryIds)
	}
}

func TestPlaylists_ExportM3U_ForeignPlaylist404(t *testing.T) {
	col := ownedPlaylist(uuid.New())
	h := newPlaylistHandler(&mockPlaylistDB{getResult: col}).WithM3U(&fakeM3UDB{})
	rec := httptest.NewRecorder()
	h.ExportM3U(rec, plReq(http.MethodGet, "", uuid.New(), col.ID.String(), ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

func TestLibraryRelativePath(t *testing.T) {
	roots := []string{"/mnt/media", "/mnt/media/music"}
	if got := libraryRelativePath("/mnt/media/music/a/b.flac", roots); got != "a/b.flac" {
		t.Errorf("deepest root: %q", got)
	}
	if got := libraryRelativePath(`D:\Music\a\b.flac`, []string{`D:\Music\`}); got != "a/b.flac" {
		t.Errorf("windows root: %q", got)
	}
	if got := libraryRelativePath("/elsewhere/b.flac", roots); got != "/elsewhere/b.flac" {
		t.Errorf("outside every root keeps absolute: %q", got)
	}
}

func m3uUpload(t *testing.T, uid uuid.UUID, filename, content string, fields map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte(content))
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/playlists/import", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r = r.WithContext(middleware.WithClaims(r.Context(), &auth.Claims{UserID: uid, MaxContentRating: "PG-13"}))
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, chi.NewRouteContext()))
}

func TestPlaylists_ImportM3U_MatchesInOrder(t *testing.T) {
	uid := uuid.New()
	musicLib, hiddenLib := uuid.New(), uuid.New()
	exact, rel, suffix, tags, hiddenItem := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	m3uDB := &fakeM3UDB{
		libs: []gen.Library{
			{ID: musicLib, ScanPaths: []string{"/srv/music"}},
			{ID: hiddenLib, ScanPaths: []string{"/srv/private"}},
		},
		files: []fakeM3UFile{
			{path: "/srv/music/A/X/01.flac", itemID: exact, title: "Exact", library: musicLib},
			{path: "/srv/music/B/Y/02.flac", itemID: rel, title: "Rel", library: musicLib},
			{path: "/srv/music/Daft Punk/Discovery/03.flac", itemID: suffix, title: "Suffix", library: musicLib},
			{path: "/srv/music/Other/Album/99.flac", itemID: tags, title: "Harder Better", artist: "Daft Punk", library: musicLib},
			{path: "/srv/private/Secret/S/04.flac", itemID: hiddenItem, title: "Hidden", library: hiddenLib},
		},
	}
	created := ownedPlaylist(uid)
	db := &mockPlaylistDB{createResult: created}
	access := &fakePlaylistAccess{allowed: map[uuid.UUID]struct{}{musicLib: {}}}
	h := newPlaylistHandler(db).WithLibraryAccess(access).WithM3U(m3uDB)

	playlist := "#EXTM3U\n" +
		"/srv/music/A/X/01.flac\n" + // line 2: exact
		"B/Y/02.flac\n" + // line 3: relative to the library root
		`M:\Music\daft punk\Discovery\03.FLAC` + "\n" + // line 4: suffix, other mount + case
		"#EXTINF:224,Daft Punk - Harder Better\n" +
		"/old/box/gone.mp3\n" + // line 6: tags
		"https://radio.example/stream\n" + // line 7: url, never fetched
		"/srv/private/Secret/S/04.flac\n" + // line 8: exists but hidden
		"/srv/music/A/X/01.flac\n" // line 9: duplicate of line 2
	rec := httptest.NewRecorder()
	h.ImportM3U(rec, m3uUpload(t, uid, "Road Trip.m3u8", playlist, nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data m3uImportResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	got := env.Data
	if got.Total != 7 || got.Matched != 5 || got.Unmatched != 2 || got.Added != 4 {
		t.Fatalf("counts = total %d matched %d unmatched %d added %d", got.Total, got.Matched, got.Unmatched, got.Added)
	}
	wantMethods := map[string]int{"path": 2, "relative": 1, "suffix": 1, "tags": 1}
	if !reflect.DeepEqual(got.ByMethod, wantMethods) {
		t.Errorf("by_method = %v, want %v", got.ByMethod, wantMethods)
	}
	wantUnmatched := []m3uUnmatchedLine{
		{Line: 7, Entry: "https://radio.example/stream", Reason: "url"},
		{Line: 8, Entry: "/srv/private/Secret/S/04.flac", Reason: "not_found"},
	}
	if !reflect.DeepEqual(got.UnmatchedEntries, wantUnmatched) {
		t.Errorf("unmatched = %+v", got.UnmatchedEntries)
	}
	if db.createArg.Name != "Road Trip" || db.createArg.Type != "playlist" ||
		db.createArg.UserID.Bytes != [16]byte(uid) {
		t.Errorf("create arg = %+v", db.createArg)
	}
	if len(m3uDB.appendArgs) != 1 ||
		!reflect.DeepEqual(m3uDB.appendArgs[0].ItemIds, []uuid.UUID{exact, rel, suffix, tags}) {
		t.Fatalf("append = %+v", m3uDB.appendArgs)
	}
	// Every lookup ran under the caller's visibility.
	for _, a := range m3uDB.pathArgs {
		if !reflect.DeepEqual(a.LibraryIds, []uuid.UUID{musicLib}) || a.MaxRatingRank == nil {
			t.Errorf("path lookup visibility = %+v", a)
		}
	}
	if m3uDB.baseArg.MaxRatingRank == nil || m3uDB.titleArg.MaxRatingRank == nil {
		t.Error("suffix/tag lookups missing the rating ceiling")
	}
	// The hidden library's root is not used to resolve relative entries.
	for _, a := range m3uDB.pathArgs {
		for _, p := range a.Paths {
			if strings.HasPrefix(p, "/srv/private/B") {
				t.Errorf("relative entry resolved against a hidden root: %q", p)
			}
		}
	}
}

func TestPlaylists_ImportM3U_SuffixNeedsADirectory(t *testing.T) {
	uid := uuid.New()
	lib := uuid.New()
	m3uDB := &fakeM3UDB{
		libs: []gen.Library{{ID: lib, ScanPaths: []string{"/srv"}}},
		files: []fakeM3UFile{
			{path: "/srv/A/Album/01.flac", itemID: uuid.New(), title: "One", library: lib},
			{path: "/srv/B/Album2/01.flac", itemID: uuid.New(), title: "Two", library: lib},
		},
	}
	h := newPlaylistHandler(&mockPlaylistDB{createResult: ownedPlaylist(uid)}).WithM3U(m3uDB)
	// Same file name, different directory: no match. A bare, ambiguous file
	// name: no match either.
	rec := httptest.NewRecorder()
	h.ImportM3U(rec, m3uUpload(t, uid, "x.m3u", "/other/Z/01.flac\n01.flac\n", map[string]string{"name": "X"}))
	var env struct {
		Data m3uImportResponse `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if rec.Code != http.StatusCreated || env.Data.Matched != 0 || env.Data.Unmatched != 2 {
		t.Fatalf("status %d, %+v", rec.Code, env.Data)
	}
}

func TestPlaylists_ImportM3U_Rejects(t *testing.T) {
	uid := uuid.New()
	h := newPlaylistHandler(&mockPlaylistDB{createResult: ownedPlaylist(uid)}).WithM3U(&fakeM3UDB{})
	cases := []struct {
		name, file, content string
		want                int
	}{
		{"wrong extension", "list.txt", "a.mp3\n", http.StatusBadRequest},
		{"latin1 m3u8", "list.m3u8", "Bj\xF6rk.mp3\n", http.StatusBadRequest},
		{"too large", "list.m3u", strings.Repeat("a", m3uMaxBytes+1), http.StatusRequestEntityTooLarge},
		{"too many entries", "list.m3u", strings.Repeat("a.mp3\n", 10001), http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ImportM3U(rec, m3uUpload(t, uid, c.file, c.content, nil))
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
	// Latin-1 is fine in a plain .m3u.
	rec := httptest.NewRecorder()
	h.ImportM3U(rec, m3uUpload(t, uid, "list.m3u", "Bj\xF6rk.mp3\n", nil))
	if rec.Code != http.StatusCreated {
		t.Errorf("latin1 m3u: status %d", rec.Code)
	}
}
