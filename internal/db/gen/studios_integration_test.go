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

// TestStudioQueries_Integration runs the studio / network queries and the
// extra-art columns (migration 00049) against Postgres: case-insensitive
// studio upsert, ordered replace of an item's studios, listings scoped by
// library grants, rating ceiling and labels, sorted paging, and the
// COALESCE rule of SetMediaItemExtraArt.
//
// Run with: go test -tags 'dev integration' -run StudioQueries ./internal/db/gen/
func TestStudioQueries_Integration(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := gen.New(pool)

	newLib := func(name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO libraries (name, type, scan_paths) VALUES ($1, 'movie', ARRAY['/'||$1]) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	libA, libB := newLib("a"), newLib("b")
	newItem := func(lib uuid.UUID, title string, year int, rating, labels []string) uuid.UUID {
		t.Helper()
		var cr any
		if len(rating) > 0 {
			cr = rating[0]
		}
		var id uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO media_items (library_id, type, title, sort_title, year, content_rating, labels)
			 VALUES ($1, 'movie', $2, $2, $3, $4, $5) RETURNING id`,
			lib, title, year, cr, labels).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	alien := newItem(libA, "Alien", 1979, []string{"R"}, []string{})
	aliens := newItem(libA, "Aliens", 1986, []string{"R"}, []string{"scary"})
	toy := newItem(libB, "Toy Story", 1995, []string{"G"}, []string{})

	upsert := func(name, kind string, tmdb int) uuid.UUID {
		t.Helper()
		var tid *int32
		if tmdb > 0 {
			v := int32(tmdb)
			tid = &v
		}
		id, err := q.UpsertStudio(ctx, gen.UpsertStudioParams{Name: name, Kind: kind, TmdbID: tid})
		if err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
		return id
	}
	fox := upsert("20th Century Fox", "studio", 0)
	if again := upsert("20TH CENTURY FOX", "studio", 25); again != fox {
		t.Errorf("case-insensitive upsert made a second studio")
	}
	if got, _ := q.GetStudio(ctx, fox); got.TmdbID == nil || *got.TmdbID != 25 || got.Name != "20th Century Fox" {
		t.Errorf("studio = %+v; the first name stays and the TMDB id fills in", got)
	}
	if net := upsert("20th Century Fox", "network", 0); net == fox {
		t.Error("a network and a studio of the same name must be different rows")
	}
	brandy := upsert("Brandywine", "studio", 0)
	pixar := upsert("Pixar", "studio", 3)

	replace := func(item uuid.UUID, ids ...uuid.UUID) {
		t.Helper()
		if ids == nil {
			ids = []uuid.UUID{}
		}
		if err := q.ReplaceItemStudios(ctx, gen.ReplaceItemStudiosParams{MediaItemID: item, StudioIds: ids}); err != nil {
			t.Fatal(err)
		}
	}
	replace(alien, pixar, fox) // wrong at first...
	replace(alien, brandy, fox)
	rows, err := q.ListItemStudios(ctx, alien)
	if err != nil || len(rows) != 2 || rows[0].ID != brandy || rows[1].ID != fox {
		t.Fatalf("alien studios = %+v, %v; want Brandywine then Fox", rows, err)
	}
	replace(aliens, fox)
	replace(toy, pixar)
	replace(toy, pixar) // idempotent

	all := func(p gen.ListStudiosWithCountsParams) map[string]int64 {
		t.Helper()
		rows, err := q.ListStudiosWithCounts(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int64{}
		for _, r := range rows {
			out[r.Name+"/"+r.Kind] = r.ItemCount
		}
		return out
	}
	got := all(gen.ListStudiosWithCountsParams{})
	if got["20th Century Fox/studio"] != 2 || got["Pixar/studio"] != 1 || got["Brandywine/studio"] != 1 || len(got) != 3 {
		t.Errorf("admin listing = %v (studios with no items are left out)", got)
	}
	// Grants on library B only.
	got = all(gen.ListStudiosWithCountsParams{LibraryIds: []uuid.UUID{libB}})
	if len(got) != 1 || got["Pixar/studio"] != 1 {
		t.Errorf("library B grant = %v", got)
	}
	// No grants at all.
	if got = all(gen.ListStudiosWithCountsParams{LibraryIds: []uuid.UUID{}}); len(got) != 0 {
		t.Errorf("no grants = %v", got)
	}
	// PG ceiling hides the R-rated Fox films.
	rank := int32(2)
	got = all(gen.ListStudiosWithCountsParams{MaxRatingRank: &rank})
	if _, ok := got["20th Century Fox/studio"]; ok || got["Pixar/studio"] != 1 {
		t.Errorf("rating ceiling = %v", got)
	}
	// Label "scary" blocked: Fox keeps Alien only.
	got = all(gen.ListStudiosWithCountsParams{BlockedLabels: []string{"scary"}})
	if got["20th Century Fox/studio"] != 1 {
		t.Errorf("blocked label = %v", got)
	}
	// One library.
	got = all(gen.ListStudiosWithCountsParams{LibraryID: pgtype.UUID{Bytes: libA, Valid: true}})
	if _, ok := got["Pixar/studio"]; ok || got["20th Century Fox/studio"] != 2 {
		t.Errorf("library A only = %v", got)
	}

	items := func(sort string, asc bool, limit, offset int32) []string {
		t.Helper()
		rows, err := q.ListStudioItems(ctx, gen.ListStudioItemsParams{StudioID: fox, Sort: sort, SortAsc: asc, LimitN: limit, OffsetN: offset})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.Title)
		}
		return out
	}
	if got := items("year", false, 10, 0); len(got) != 2 || got[0] != "Aliens" {
		t.Errorf("year desc = %v", got)
	}
	if got := items("title", true, 1, 1); len(got) != 1 || got[0] != "Aliens" {
		t.Errorf("title asc, page 2 of 1 = %v", got)
	}
	n, err := q.CountStudioItems(ctx, gen.CountStudioItemsParams{StudioID: fox, BlockedLabels: []string{"scary"}})
	if err != nil || n != 1 {
		t.Errorf("count with blocked label = %d, %v", n, err)
	}

	// Deleting an item drops its studio links.
	if _, err := pool.Exec(ctx, `DELETE FROM media_items WHERE id = $1`, toy); err != nil {
		t.Fatal(err)
	}
	if got = all(gen.ListStudiosWithCountsParams{}); got["Pixar/studio"] != 0 {
		t.Errorf("after delete = %v", got)
	}
	replace(alien) // empty clears
	if rows, _ := q.ListItemStudios(ctx, alien); len(rows) != 0 {
		t.Errorf("cleared = %+v", rows)
	}

	// Extra art: set, then a NULL argument keeps the stored path.
	logo, banner := "Alien (1979)/clearlogo.png", "Alien (1979)/banner.jpg"
	if err := q.SetMediaItemExtraArt(ctx, gen.SetMediaItemExtraArtParams{ID: alien, LogoPath: &logo, BannerPath: &banner}); err != nil {
		t.Fatal(err)
	}
	disc := "Alien (1979)/disc.png"
	if err := q.SetMediaItemExtraArt(ctx, gen.SetMediaItemExtraArtParams{ID: alien, DiscArtPath: &disc}); err != nil {
		t.Fatal(err)
	}
	item, err := q.GetMediaItem(ctx, alien)
	if err != nil || item.LogoPath == nil || *item.LogoPath != logo || item.BannerPath == nil || item.DiscArtPath == nil || *item.DiscArtPath != disc {
		t.Errorf("extra art = %v %v %v, %v", item.LogoPath, item.BannerPath, item.DiscArtPath, err)
	}
	// The /artwork gate resolves the owning item of a logo path too.
	cr, err := q.GetArtworkContentRating(ctx, gen.GetArtworkContentRatingParams{LibraryID: libA, ArtPath: "alien (1979)/CLEARLOGO.png"})
	if err != nil || cr != "R" {
		t.Errorf("artwork rating for the logo = %q, %v", cr, err)
	}

	// save_nfo defaults off and reads back with the language.
	opts, err := q.GetLibraryNFOOptions(ctx, libA)
	if err != nil || opts.SaveNfo || opts.Language == "" {
		t.Errorf("library NFO options = %+v, %v", opts, err)
	}
}
