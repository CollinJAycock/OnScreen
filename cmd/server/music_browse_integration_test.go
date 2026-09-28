//go:build integration

// The music albums index against a real Postgres: every album in a library,
// listed across artists through the production media adapter — the
// sort=artist queries, the shared filters on top of them — and the batched
// title lookup that labels each album with its artist.
//
// Run with: go test -tags=integration ./cmd/server/ -run MusicBrowse
package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestMusicBrowse_Integration_AlbumsAcrossArtists(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()
	adapter := &mediaAdapter{q: q}

	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name: "Music", Type: "music", ScanPaths: []string{t.TempDir()},
		Agent: "tmdb", Language: "en", ScanInterval: time.Hour, MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	item := func(typ, title, sortTitle string, parent uuid.UUID, year int32, genres ...string) uuid.UUID {
		t.Helper()
		p := gen.CreateMediaItemParams{
			LibraryID: lib.ID, Type: typ, Title: title, SortTitle: sortTitle, Genres: genres,
		}
		if parent != uuid.Nil {
			p.ParentID = pgtype.UUID{Bytes: parent, Valid: true}
		}
		if year != 0 {
			p.Year = &year
		}
		row, err := q.CreateMediaItem(ctx, p)
		if err != nil {
			t.Fatalf("create %s %q: %v", typ, title, err)
		}
		return row.ID
	}

	// Artists created out of order; "The Beatles" sorts under B.
	zappa := item("artist", "Frank Zappa", "zappa frank", uuid.Nil, 0)
	beatles := item("artist", "The Beatles", "beatles", uuid.Nil, 0)
	abba := item("artist", "ABBA", "abba", uuid.Nil, 0)

	item("album", "Hot Rats", "hot rats", zappa, 1969, "Jazz Rock")
	item("album", "Apostrophe (')", "apostrophe", zappa, 1974)
	item("album", "Lost Tapes", "lost tapes", zappa, 0) // no year: last within Zappa
	abbey := item("album", "Abbey Road", "abbey road", beatles, 1969, "Rock")
	item("album", "Please Please Me", "please please me", beatles, 1963, "Rock", "Pop")
	item("album", "Revolver", "revolver", beatles, 1966, "Rock")
	item("album", "Arrival", "arrival", abba, 1976, "Pop")
	// Not albums, or not live: never listed.
	item("track", "Come Together", "come together", abbey, 1969, "Rock")
	gone := item("album", "Deleted Album", "deleted album", abba, 1980, "Pop")
	if _, err := pool.Exec(ctx, `UPDATE media_items SET deleted_at = NOW() WHERE id = $1`, gone); err != nil {
		t.Fatalf("soft-delete: %v", err)
	}

	list := func(label string, limit, offset int32, f media.FilterParams) []string {
		t.Helper()
		items, err := adapter.ListMediaItemsFiltered(ctx, lib.ID, "album", limit, offset, f)
		if err != nil {
			t.Fatalf("%s: list: %v", label, err)
		}
		titles := make([]string, len(items))
		for i, it := range items {
			titles[i] = it.Title
		}
		return titles
	}
	expect := func(label string, got, want []string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n got  %q\n want %q", label, got, want)
		}
	}
	strp := func(s string) *string { return &s }
	intp := func(n int) *int { return &n }

	expect("artist A→Z, oldest first within an artist",
		list("asc", 50, 0, media.FilterParams{Sort: "artist", SortAsc: true}),
		[]string{"Arrival", "Please Please Me", "Revolver", "Abbey Road", "Hot Rats", "Apostrophe (')", "Lost Tapes"})
	expect("artist Z→A keeps each discography in release order",
		list("desc", 50, 0, media.FilterParams{Sort: "artist", SortAsc: false}),
		[]string{"Hot Rats", "Apostrophe (')", "Lost Tapes", "Please Please Me", "Revolver", "Abbey Road", "Arrival"})
	expect("paged",
		list("page", 2, 2, media.FilterParams{Sort: "artist", SortAsc: true}),
		[]string{"Revolver", "Abbey Road"})
	expect("genre filter",
		list("genre", 50, 0, media.FilterParams{Sort: "artist", SortAsc: true, Genre: strp("Pop")}),
		[]string{"Arrival", "Please Please Me"})
	expect("year range",
		list("years", 50, 0, media.FilterParams{Sort: "artist", SortAsc: true, YearMin: intp(1969), YearMax: intp(1969)}),
		[]string{"Abbey Road", "Hot Rats"})
	// Albums have no watch state of their own, so they are all "unwatched";
	// this runs the join's qualified media_watch_bucket call.
	caller := uuid.New()
	if got := list("unwatched", 50, 0, media.FilterParams{Sort: "artist", SortAsc: true, Watch: "unwatched", WatchUserID: caller}); len(got) != 7 {
		t.Errorf("watch=unwatched: got %d albums, want all 7", len(got))
	}
	expect("watch=watched",
		list("watched", 50, 0, media.FilterParams{Sort: "artist", SortAsc: true, Watch: "watched", WatchUserID: caller}),
		[]string{})
	// The existing sorts list albums across artists too.
	expect("year newest first",
		list("year", 3, 0, media.FilterParams{Sort: "year", SortAsc: false}),
		[]string{"Arrival", "Apostrophe (')", "Abbey Road"})

	n, err := adapter.CountMediaItemsFiltered(ctx, lib.ID, "album", media.FilterParams{Sort: "artist", Genre: strp("Rock")})
	if err != nil || n != 3 {
		t.Errorf("count genre=Rock: got %d, %v; want 3", n, err)
	}

	// Parent titles: one row per live id, unknown ids ignored.
	rows, err := q.ListMediaItemTitles(ctx, []uuid.UUID{zappa, beatles, abba, uuid.New(), gone})
	if err != nil {
		t.Fatalf("ListMediaItemTitles: %v", err)
	}
	got := map[uuid.UUID]string{}
	for _, r := range rows {
		got[r.ID] = r.Title
	}
	want := map[uuid.UUID]string{zappa: "Frank Zappa", beatles: "The Beatles", abba: "ABBA"}
	if len(got) != len(want) {
		t.Errorf("titles: got %v, want %v", got, want)
	}
	for id, title := range want {
		if got[id] != title {
			t.Errorf("title of %s: got %q, want %q", id, got[id], title)
		}
	}
}
