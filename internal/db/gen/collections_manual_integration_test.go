//go:build integration

package gen_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestManualCollections_Integration pins the manual-collection SQL (migration
// 00037) against Postgres: who sees which collection, which cover they get,
// the three listing orders, the item -> collections lookup, and the poster
// member being cleared when that item is deleted.
func TestManualCollections_Integration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)

	libMovies := seedLibrary(ctx, t, q, "mc-movies")
	libKids := seedLibrary(ctx, t, q, "mc-kids")

	// item creates a movie with a rating, poster and release date.
	item := func(lib uuid.UUID, title, sortTitle, rating, poster, released string) uuid.UUID {
		t.Helper()
		id := seedMediaItem(ctx, t, q, lib, title)
		if _, err := pool.Exec(ctx, `UPDATE media_items
			SET sort_title = $2, content_rating = NULLIF($3, ''), poster_path = NULLIF($4, ''),
			    originally_available_at = NULLIF($5, '')::date
			WHERE id = $1`, id, sortTitle, rating, poster, released); err != nil {
			t.Fatalf("set item fields: %v", err)
		}
		return id
	}
	halloween := item(libMovies, "Halloween", "Halloween", "R", "/p/halloween.jpg", "1978-10-25")
	scream := item(libMovies, "Scream", "Scream", "R", "/p/scream.jpg", "1996-12-20")
	coraline := item(libKids, "Coraline", "Coraline", "PG", "/p/coraline.jpg", "2009-02-06")
	casper := item(libKids, "Casper", "Casper", "PG", "", "1995-05-26") // no poster

	spooky, err := q.CreateManualCollection(ctx, gen.CreateManualCollectionParams{Name: "Spooky", ItemOrder: "custom"})
	if err != nil {
		t.Fatalf("CreateManualCollection: %v", err)
	}
	if spooky.Type != "manual" || spooky.UserID.Valid || spooky.ItemOrder != "custom" {
		t.Fatalf("created %+v", spooky)
	}
	grownUp, err := q.CreateManualCollection(ctx, gen.CreateManualCollectionParams{Name: "Grown-up", ItemOrder: "custom"})
	if err != nil {
		t.Fatalf("CreateManualCollection: %v", err)
	}
	if _, err := q.CreateManualCollection(ctx, gen.CreateManualCollectionParams{Name: "Empty", ItemOrder: "custom"}); err != nil {
		t.Fatalf("CreateManualCollection: %v", err)
	}
	// The item_order CHECK refuses anything else.
	if _, err := q.CreateManualCollection(ctx, gen.CreateManualCollectionParams{Name: "Bad", ItemOrder: "random"}); err == nil {
		t.Fatal("item_order CHECK accepted 'random'")
	}

	add := func(col uuid.UUID, ids ...uuid.UUID) {
		t.Helper()
		for _, id := range ids {
			if _, err := q.AddCollectionItem(ctx, gen.AddCollectionItemParams{CollectionID: col, MediaItemID: id}); err != nil {
				t.Fatalf("AddCollectionItem: %v", err)
			}
		}
	}
	// Custom order: Scream, Halloween, Casper, Coraline.
	add(spooky.ID, scream, halloween, casper, coraline)
	add(grownUp.ID, halloween, scream)

	var pgRank int32
	if err := pool.QueryRow(ctx, `SELECT content_rating_rank('PG')`).Scan(&pgRank); err != nil {
		t.Fatal(err)
	}

	visible := func(libs []uuid.UUID, rank *int32) map[uuid.UUID]gen.ListVisibleManualCollectionsRow {
		t.Helper()
		rows, err := q.ListVisibleManualCollections(ctx, gen.ListVisibleManualCollectionsParams{LibraryIds: libs, MaxRatingRank: rank})
		if err != nil {
			t.Fatalf("ListVisibleManualCollections: %v", err)
		}
		out := map[uuid.UUID]gen.ListVisibleManualCollectionsRow{}
		for _, r := range rows {
			out[r.ID] = r
		}
		return out
	}

	// Unrestricted: both non-empty collections; the empty one has no row.
	all := visible(nil, nil)
	if len(all) != 2 || all[spooky.ID].ItemCount != 4 || all[grownUp.ID].ItemCount != 2 {
		t.Fatalf("unrestricted visible = %+v", all)
	}
	// No poster member chosen: the first member in order that has a poster.
	if got := all[spooky.ID].CoverPosterPath; got != "/p/scream.jpg" {
		t.Fatalf("default cover = %q, want the first member's", got)
	}

	// A PG-capped profile with both libraries: Grown-up (all R) is gone, and
	// Spooky shows only its PG members, with a cover from them.
	kid := visible([]uuid.UUID{libMovies, libKids}, &pgRank)
	if _, ok := kid[grownUp.ID]; ok {
		t.Fatal("a collection with only over-ceiling members is visible")
	}
	if r := kid[spooky.ID]; r.ItemCount != 2 || r.CoverPosterPath != "/p/coraline.jpg" {
		t.Fatalf("capped spooky = %+v, want 2 visible and Coraline's poster (Casper has none)", r)
	}

	// A profile with only the movies library: Spooky shows its 2 movies.
	moviesOnly := visible([]uuid.UUID{libMovies}, nil)
	if moviesOnly[spooky.ID].ItemCount != 2 || moviesOnly[grownUp.ID].ItemCount != 2 {
		t.Fatalf("movies-only = %+v", moviesOnly)
	}
	// No grants at all: nothing.
	if none := visible([]uuid.UUID{}, nil); len(none) != 0 {
		t.Fatalf("no grants = %+v, want none", none)
	}

	// The chosen poster member wins when visible, and is skipped when not.
	if err := q.SetCollectionPosterItem(ctx, gen.SetCollectionPosterItemParams{
		ID: spooky.ID, PosterItemID: pgtype.UUID{Bytes: [16]byte(halloween), Valid: true},
	}); err != nil {
		t.Fatalf("SetCollectionPosterItem: %v", err)
	}
	if got := visible(nil, nil)[spooky.ID].CoverPosterPath; got != "/p/halloween.jpg" {
		t.Fatalf("chosen cover = %q, want Halloween's", got)
	}
	if got := visible([]uuid.UUID{libMovies, libKids}, &pgRank)[spooky.ID].CoverPosterPath; got != "/p/coraline.jpg" {
		t.Fatalf("capped cover = %q: an over-ceiling poster member must not be the cover", got)
	}

	// Library tab: per-library counts and covers.
	libRows, err := q.ListLibraryManualCollections(ctx, gen.ListLibraryManualCollectionsParams{LibraryID: libKids})
	if err != nil {
		t.Fatalf("ListLibraryManualCollections: %v", err)
	}
	if len(libRows) != 1 || libRows[0].ID != spooky.ID || libRows[0].ItemCount != 2 || libRows[0].CoverPosterPath != "/p/coraline.jpg" {
		t.Fatalf("kids library tab = %+v", libRows)
	}
	libRows, err = q.ListLibraryManualCollections(ctx, gen.ListLibraryManualCollectionsParams{LibraryID: libMovies, MaxRatingRank: &pgRank})
	if err != nil || len(libRows) != 0 {
		t.Fatalf("movies tab under a PG cap = %+v (err %v), want none", libRows, err)
	}

	// Listing orders.
	order := func(mode string) []string {
		t.Helper()
		rows, err := q.ListCollectionItems(ctx, gen.ListCollectionItemsParams{CollectionID: spooky.ID, ItemOrder: mode})
		if err != nil {
			t.Fatalf("ListCollectionItems(%q): %v", mode, err)
		}
		var titles []string
		for _, r := range rows {
			titles = append(titles, r.Title)
		}
		return titles
	}
	for mode, want := range map[string][]string{
		"custom":  {"Scream", "Halloween", "Casper", "Coraline"},
		"":        {"Scream", "Halloween", "Casper", "Coraline"},
		"release": {"Halloween", "Casper", "Scream", "Coraline"},
		"title":   {"Casper", "Coraline", "Halloween", "Scream"},
	} {
		got := order(mode)
		if len(got) != len(want) {
			t.Fatalf("order %q = %v, want %v", mode, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("order %q = %v, want %v", mode, got, want)
			}
		}
	}

	// The custom order follows a reorder.
	if err := q.ReorderPlaylistItems(ctx, gen.ReorderPlaylistItemsParams{
		CollectionID: spooky.ID, ItemIds: []uuid.UUID{coraline, casper, halloween, scream},
	}); err != nil {
		t.Fatalf("ReorderPlaylistItems: %v", err)
	}
	if got := order("custom"); got[0] != "Coraline" || got[3] != "Scream" {
		t.Fatalf("after reorder = %v", got)
	}

	// Item -> its manual collections (a playlist holding it isn't one).
	pl, err := q.CreateCollection(ctx, gen.CreateCollectionParams{Name: "My list", Type: "playlist"})
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	add(pl.ID, halloween)
	refs, err := q.ListManualCollectionsForItem(ctx, halloween)
	if err != nil {
		t.Fatalf("ListManualCollectionsForItem: %v", err)
	}
	if len(refs) != 2 || refs[0].Name != "Grown-up" || refs[1].Name != "Spooky" {
		t.Fatalf("halloween is in %+v, want Grown-up and Spooky", refs)
	}

	// Settings round-trip through the full-row reads.
	if err := q.SetCollectionItemOrder(ctx, gen.SetCollectionItemOrderParams{ID: spooky.ID, ItemOrder: "title"}); err != nil {
		t.Fatalf("SetCollectionItemOrder: %v", err)
	}
	got, err := q.GetCollection(ctx, spooky.ID)
	if err != nil || got.ItemOrder != "title" || !got.PosterItemID.Valid || uuid.UUID(got.PosterItemID.Bytes) != halloween {
		t.Fatalf("GetCollection = %+v (err %v)", got, err)
	}

	// Deleting the poster member clears poster_item_id (ON DELETE SET NULL).
	if _, err := pool.Exec(ctx, `DELETE FROM media_items WHERE id = $1`, halloween); err != nil {
		t.Fatalf("delete item: %v", err)
	}
	got, err = q.GetCollection(ctx, spooky.ID)
	if err != nil || got.PosterItemID.Valid {
		t.Fatalf("poster_item_id after deleting that item = %+v (err %v), want NULL", got.PosterItemID, err)
	}

	// Existing collection types are untouched by the new columns' defaults.
	if pl.ItemOrder != "custom" || pl.PosterItemID.Valid {
		t.Fatalf("playlist defaults = %q / %v", pl.ItemOrder, pl.PosterItemID)
	}
}
