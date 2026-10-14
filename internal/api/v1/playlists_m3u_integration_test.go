//go:build integration

package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// M3U import and export against real Postgres: the path, suffix and tag
// matches, the label restriction applied in SQL, and the export's library-
// relative paths and #EXTINF artist.
func TestPlaylistM3U_Integration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)
	h := NewPlaylistHandler(q, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithLibraryAccess(&fakePlaylistAccess{}).WithM3U(q)

	u, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "m3u-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	root := "/srv/music-" + uuid.NewString()[:8]
	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name: "Music-" + uuid.NewString()[:8], Type: "music", ScanPaths: []string{root},
		Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	item := func(typ, title string, parent uuid.UUID) uuid.UUID {
		t.Helper()
		p := gen.CreateMediaItemParams{LibraryID: lib.ID, Type: typ, Title: title, SortTitle: strings.ToLower(title),
			Genres: []string{}, Tags: []string{}}
		if parent != uuid.Nil {
			p.ParentID = pgtype.UUID{Bytes: parent, Valid: true}
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return row.ID
	}
	file := func(itemID uuid.UUID, path string, ms int64) {
		t.Helper()
		if _, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{MediaItemID: itemID, FilePath: path, FileSize: 1, DurationMs: &ms}); err != nil {
			t.Fatal(err)
		}
	}
	artist := item("artist", "Daft Punk", uuid.Nil)
	album := item("album", "Discovery", artist)
	one := item("track", "One More Time", album)
	two := item("track", "Aerodynamic", album)
	three := item("track", "Digital Love", album)
	file(one, root+"/Daft Punk/Discovery/01 One More Time.flac", 320_000)
	file(two, root+"/Daft Punk/Discovery/02 Aerodynamic.flac", 212_000)
	file(three, root+"/Daft Punk/Discovery/03 Digital Love.flac", 301_000)
	if _, err := pool.Exec(ctx, `UPDATE media_items SET labels = ARRAY['hidden'] WHERE id = $1`, two); err != nil {
		t.Fatal(err)
	}

	claims := &auth.Claims{UserID: u.ID, BlockedLabels: []string{"hidden"}}
	playlist := "#EXTM3U\r\n" +
		`M:\Music\Daft Punk\Discovery\01 One More Time.flac` + "\r\n" + // suffix
		"Daft Punk/Discovery/02 Aerodynamic.flac\r\n" + // relative — but label-hidden
		"#EXTINF:301,daft punk - digital love\r\n" +
		"/old/nas/somewhere/else.mp3\r\n" + // tags
		root + "/Daft Punk/Discovery/01 One More Time.flac\r\n" // exact (duplicate item)
	req := m3uUpload(t, u.ID, "Favourites.m3u8", playlist, nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	rec := httptest.NewRecorder()
	h.ImportM3U(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data m3uImportResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	res := env.Data
	if res.Matched != 3 || res.Unmatched != 1 || res.Added != 2 ||
		res.ByMethod["suffix"] != 1 || res.ByMethod["tags"] != 1 || res.ByMethod["path"] != 1 {
		t.Fatalf("import result = %+v", res)
	}
	if len(res.UnmatchedEntries) != 1 || res.UnmatchedEntries[0].Line != 3 {
		t.Fatalf("unmatched = %+v (the label-hidden track must not match)", res.UnmatchedEntries)
	}

	// Export, relative and absolute.
	pid := res.Playlist.ID
	rec = httptest.NewRecorder()
	er := plReq(http.MethodGet, "", u.ID, pid, "")
	er = er.WithContext(middleware.WithClaims(er.Context(), claims))
	h.ExportM3U(rec, er)
	want := "#EXTM3U\n" +
		"#EXTINF:320,Daft Punk - One More Time\nDaft Punk/Discovery/01 One More Time.flac\n" +
		"#EXTINF:301,Daft Punk - Digital Love\nDaft Punk/Discovery/03 Digital Love.flac\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("export: %d\n%s\nwant\n%s", rec.Code, rec.Body.String(), want)
	}

	// The playlist now also holds the hidden track (added by someone who
	// could see it); the restricted export leaves it out.
	if err := q.AppendCollectionItems(ctx, gen.AppendCollectionItemsParams{
		CollectionID: uuid.MustParse(pid), ItemIds: []uuid.UUID{two},
	}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	er = plReq(http.MethodGet, "", u.ID, pid, "")
	er.URL.RawQuery = "paths=absolute"
	er = er.WithContext(middleware.WithClaims(er.Context(), claims))
	h.ExportM3U(rec, er)
	body := rec.Body.String()
	if strings.Contains(body, "Aerodynamic") || !strings.Contains(body, "\n"+root+"/Daft Punk/Discovery/03 Digital Love.flac\n") {
		t.Fatalf("absolute export:\n%s", body)
	}
}
