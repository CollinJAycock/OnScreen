//go:build integration

package gen_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/collections"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

// TestCollectionsPhase2_Integration pins the phase-2 collection SQL
// (migration 00038) against Postgres: smart refreshes through the real
// resolver, NFO-imported collections found again by their source key,
// uploaded covers and their versions, the promoted listing, and the
// library nfo_collections setting.
func TestCollectionsPhase2_Integration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)

	lib := seedLibrary(ctx, t, q, "p2-movies")
	other := seedLibrary(ctx, t, q, "p2-other")
	item := func(lib uuid.UUID, title string, year int, genres ...string) uuid.UUID {
		t.Helper()
		id := seedMediaItem(ctx, t, q, lib, title)
		if _, err := pool.Exec(ctx, `UPDATE media_items SET year = $2, genres = $3 WHERE id = $1`, id, year, genres); err != nil {
			t.Fatalf("set item fields: %v", err)
		}
		return id
	}
	halloween := item(lib, "Halloween", 1978, "Horror")
	thing := item(lib, "The Thing", 1982, "Horror", "Sci-Fi")
	aliens := item(lib, "Aliens", 1986, "Sci-Fi")
	nightmare := item(other, "A Nightmare on Elm Street", 1984, "Horror")

	// ── Smart collections ──────────────────────────────────────────────
	col, err := q.CreateManualCollection(ctx, gen.CreateManualCollectionParams{Name: "80s horror", ItemOrder: "custom"})
	if err != nil {
		t.Fatal(err)
	}
	smart := collections.NewSmart(q, slog.Default())
	y0, y1 := 1980, 1989
	rules := collections.Rules{Types: []string{"movie"}, Genres: []string{"Horror"}, YearMin: &y0, YearMax: &y1}
	n, err := smart.Refresh(ctx, col.ID, rules)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	members := func() map[uuid.UUID]bool {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT media_item_id FROM collection_items WHERE collection_id = $1`, col.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[uuid.UUID]bool{}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			out[id] = true
		}
		return out
	}
	if m := members(); n != 2 || len(m) != 2 || !m[thing] || !m[nightmare] {
		t.Fatalf("refresh = %d, members %v; want The Thing and Nightmare", n, m)
	}
	// Narrower rules drop the member they no longer match; positions follow
	// the resolver's order.
	rules.LibraryIDs = []string{lib.String()}
	if _, err := smart.Refresh(ctx, col.ID, rules); err != nil {
		t.Fatal(err)
	}
	if m := members(); len(m) != 1 || !m[thing] {
		t.Fatalf("library-limited members %v", m)
	}
	// Stored rules are listed for the hourly task, and RefreshAll uses them.
	if err := q.SetCollectionRules(ctx, gen.SetCollectionRulesParams{ID: col.ID, Rules: []byte(`{"types":["movie"],"genres":["Sci-Fi"]}`)}); err != nil {
		t.Fatal(err)
	}
	listed, err := q.ListSmartCollections(ctx)
	if err != nil || len(listed) != 1 || listed[0].ID != col.ID {
		t.Fatalf("ListSmartCollections = %+v, %v", listed, err)
	}
	if _, err := smart.RefreshAll(ctx); err != nil {
		t.Fatal(err)
	}
	if m := members(); len(m) != 2 || !m[thing] || !m[aliens] {
		t.Fatalf("after RefreshAll members %v", m)
	}
	got, err := q.GetCollection(ctx, col.ID)
	if err != nil || len(got.Rules) == 0 {
		t.Fatalf("GetCollection rules = %s, %v", got.Rules, err)
	}
	if err := q.SetCollectionRules(ctx, gen.SetCollectionRulesParams{ID: col.ID, Rules: nil}); err != nil {
		t.Fatal(err)
	}
	if listed, _ := q.ListSmartCollections(ctx); len(listed) != 0 {
		t.Fatalf("cleared rules still listed: %+v", listed)
	}

	// ── NFO-imported collections ───────────────────────────────────────
	src, key := "nfo", "set:alien collection"
	a, err := q.FindOrCreateSourceCollection(ctx, gen.FindOrCreateSourceCollectionParams{Name: "Alien Collection", Source: &src, SourceKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.FindOrCreateSourceCollection(ctx, gen.FindOrCreateSourceCollectionParams{Name: "alien collection", Source: &src, SourceKey: &key})
	if err != nil || b != a {
		t.Fatalf("second find = %v, %v; want the same collection %v", b, err, a)
	}
	imported, err := q.GetCollection(ctx, a)
	if err != nil || imported.Type != "manual" || imported.ItemOrder != "release" || imported.Name != "Alien Collection" || imported.Source == nil || *imported.Source != "nfo" {
		t.Fatalf("imported collection %+v, %v", imported, err)
	}
	// Hand-made collections (no source) never collide.
	for i := 0; i < 2; i++ {
		if _, err := q.CreateManualCollection(ctx, gen.CreateManualCollectionParams{Name: "Alien Collection", ItemOrder: "custom"}); err != nil {
			t.Fatalf("hand-made duplicate name: %v", err)
		}
	}

	// ── Uploaded covers ────────────────────────────────────────────────
	if err := q.UpsertCollectionPoster(ctx, gen.UpsertCollectionPosterParams{CollectionID: col.ID, ContentType: "image/jpeg", Data: []byte{0xFF, 0xD8, 1}}); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertCollectionPoster(ctx, gen.UpsertCollectionPosterParams{CollectionID: col.ID, ContentType: "image/jpeg", Data: []byte{0xFF, 0xD8, 2}}); err != nil {
		t.Fatal(err)
	}
	p, err := q.GetCollectionPoster(ctx, col.ID)
	if err != nil || p.Data[2] != 2 || !p.UpdatedAt.Valid {
		t.Fatalf("GetCollectionPoster = %+v, %v", p, err)
	}
	versions, err := q.ListCollectionPosterVersions(ctx, []uuid.UUID{col.ID, a})
	if err != nil || len(versions) != 1 || versions[0].CollectionID != col.ID {
		t.Fatalf("ListCollectionPosterVersions = %+v, %v", versions, err)
	}
	if err := q.DeleteCollectionPoster(ctx, col.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetCollectionPoster(ctx, col.ID); err == nil {
		t.Fatal("poster still there after delete")
	}
	// Deleting a collection drops its cover.
	if err := q.UpsertCollectionPoster(ctx, gen.UpsertCollectionPosterParams{CollectionID: a, ContentType: "image/jpeg", Data: []byte{1}}); err != nil {
		t.Fatal(err)
	}

	// ── Promoted ───────────────────────────────────────────────────────
	if err := q.SetCollectionPromoted(ctx, gen.SetCollectionPromotedParams{ID: a, Promoted: true}); err != nil {
		t.Fatal(err)
	}
	promoted, err := q.ListPromotedCollections(ctx)
	if err != nil || len(promoted) != 1 || promoted[0].ID != a || promoted[0].ItemOrder != "release" {
		t.Fatalf("ListPromotedCollections = %+v, %v", promoted, err)
	}
	if err := q.DeleteCollection(ctx, a); err != nil {
		t.Fatal(err)
	}
	var covers int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM collection_posters WHERE collection_id = $1`, a).Scan(&covers); err != nil || covers != 0 {
		t.Fatalf("cover rows after deleting the collection = %d, %v", covers, err)
	}
	_ = halloween

	// ── Library setting ────────────────────────────────────────────────
	l, err := q.GetLibrary(ctx, lib)
	if err != nil || l.NfoCollections != "off" {
		t.Fatalf("default nfo_collections = %q, %v", l.NfoCollections, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE libraries SET nfo_collections = 'everything' WHERE id = $1`, lib); err == nil {
		t.Fatal("nfo_collections CHECK accepted 'everything'")
	}
	mode := "sets_and_tags"
	upd, err := q.UpdateLibrary(ctx, gen.UpdateLibraryParams{ID: lib, Name: l.Name, ScanPaths: l.ScanPaths, Agent: l.Agent, Language: l.Language,
		ScanInterval: l.ScanInterval, MetadataRefreshInterval: l.MetadataRefreshInterval, NfoCollections: &mode})
	if err != nil || upd.NfoCollections != mode {
		t.Fatalf("UpdateLibrary nfo_collections = %q, %v", upd.NfoCollections, err)
	}
}
