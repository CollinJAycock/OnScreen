//go:build integration

package v1

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// The OPDS book query against real Postgres: book libraries only, the rating
// ceiling and label restriction in SQL, search, and newest-first ordering.
func TestOPDS_Integration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)
	tm, err := auth.NewTokenMaker(auth.DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	h := NewOPDSHandler(q, tm, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithLibraryAccess(&fakePlaylistAccess{})

	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name: "Books-" + uuid.NewString()[:8], Type: "book", ScanPaths: []string{"/srv/books"},
		Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	book := func(title, author, rating, ext string) uuid.UUID {
		t.Helper()
		p := gen.CreateMediaItemParams{LibraryID: lib.ID, Type: "book", Title: title, SortTitle: strings.ToLower(title),
			OriginalTitle: &author, Genres: []string{"Fantasy"}, Tags: []string{}}
		if rating != "" {
			p.ContentRating = &rating
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.CreateMediaFile(ctx, gen.CreateMediaFileParams{MediaItemID: row.ID,
			FilePath: "/srv/books/" + row.ID.String() + ext, FileSize: 1000}); err != nil {
			t.Fatal(err)
		}
		return row.ID
	}
	book("A Wizard of Earthsea", "Ursula K. Le Guin", "PG", ".epub")
	book("Grown-up Comic", "Someone", "R", ".cbz")
	labelled := book("Labelled Book", "Someone Else", "PG", ".pdf")
	if _, err := pool.Exec(ctx, `UPDATE media_items SET labels = ARRAY['hidden'] WHERE id = $1`, labelled); err != nil {
		t.Fatal(err)
	}

	claims := &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG-13", BlockedLabels: []string{"hidden"}}
	titles := func(rec *httptest.ResponseRecorder) []string {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var f parsedFeed
		if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range f.Entries {
			out = append(out, e.Title)
		}
		return out
	}

	rec := httptest.NewRecorder()
	h.Library(rec, opdsReq(t, claims, "/", map[string]string{"id": lib.ID.String()}))
	if got := titles(rec); len(got) != 1 || got[0] != "A Wizard of Earthsea" {
		t.Fatalf("library feed = %v, want only the in-ceiling, unlabelled book", got)
	}
	if !strings.Contains(rec.Body.String(), "Ursula K. Le Guin") || !strings.Contains(rec.Body.String(), `type="application/epub+zip"`) {
		t.Errorf("entry lacks author or acquisition type:\n%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.Search(rec, opdsReq(t, claims, "/opds/search?q=earthsea", nil))
	if got := titles(rec); len(got) != 1 {
		t.Fatalf("search = %v", got)
	}
	rec = httptest.NewRecorder()
	h.Search(rec, opdsReq(t, claims, "/opds/search?q=comic", nil))
	if got := titles(rec); len(got) != 0 {
		t.Fatalf("search found an over-ceiling book: %v", got)
	}

	// Unrestricted: everything, newest first on /recent.
	all := &auth.Claims{UserID: uuid.New()}
	rec = httptest.NewRecorder()
	h.Recent(rec, opdsReq(t, all, "/opds/recent", nil))
	got := titles(rec)
	if len(got) != 3 || got[0] != "Labelled Book" || got[2] != "A Wizard of Earthsea" {
		t.Fatalf("recent = %v", got)
	}
}
