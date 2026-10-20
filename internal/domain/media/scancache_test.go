package media

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// countingQuerier counts the lookups FindOrCreate* issue, and the rows the
// child listings return, on top of the in-memory mockQuerier. It also
// resolves FindShowByFolderPrefix through a folder→show map, the way the real
// query finds a show through files already attached under its folder.
type countingQuerier struct {
	*mockQuerier
	folders map[string]uuid.UUID

	byTMDB, exact, flexible, search, folder, children, childRows, get, create int
}

func newCountingQuerier() *countingQuerier {
	return &countingQuerier{mockQuerier: newMockQuerier(), folders: map[string]uuid.UUID{}}
}

func (c *countingQuerier) GetMediaItem(ctx context.Context, id uuid.UUID) (Item, error) {
	c.get++
	return c.mockQuerier.GetMediaItem(ctx, id)
}
func (c *countingQuerier) GetMediaItemByTMDBID(ctx context.Context, lib uuid.UUID, id int) (Item, error) {
	c.byTMDB++
	return c.mockQuerier.GetMediaItemByTMDBID(ctx, lib, id)
}
func (c *countingQuerier) FindTopLevelItemByTitleYear(ctx context.Context, lib uuid.UUID, typ, title string, year *int) (*Item, error) {
	c.exact++
	return c.mockQuerier.FindTopLevelItemByTitleYear(ctx, lib, typ, title, year)
}
func (c *countingQuerier) FindTopLevelItemsByTitleFlexible(ctx context.Context, lib uuid.UUID, typ, title string) ([]Item, error) {
	c.flexible++
	return c.mockQuerier.FindTopLevelItemsByTitleFlexible(ctx, lib, typ, title)
}
func (c *countingQuerier) SearchMediaItems(ctx context.Context, lib uuid.UUID, q string, limit int32) ([]Item, error) {
	c.search++
	return c.mockQuerier.SearchMediaItems(ctx, lib, q, limit)
}
func (c *countingQuerier) FindShowByFolderPrefix(_ context.Context, _ uuid.UUID, folder string) (*Item, error) {
	c.folder++
	if id, ok := c.folders[folder]; ok {
		if it, ok := c.items[id]; ok {
			return &it, nil
		}
	}
	return nil, nil
}
func (c *countingQuerier) ListMediaItemChildren(ctx context.Context, parent uuid.UUID) ([]Item, error) {
	c.children++
	out, err := c.mockQuerier.ListMediaItemChildren(ctx, parent)
	c.childRows += len(out)
	return out, err
}
func (c *countingQuerier) CreateMediaItem(ctx context.Context, p CreateItemParams) (Item, error) {
	c.create++
	return c.mockQuerier.CreateMediaItem(ctx, p)
}

func (c *countingQuerier) queries() int {
	return c.byTMDB + c.exact + c.flexible + c.search + c.folder + c.children + c.get
}

func (c *countingQuerier) reset() {
	c.byTMDB, c.exact, c.flexible, c.search, c.folder, c.children, c.childRows, c.get, c.create = 0, 0, 0, 0, 0, 0, 0, 0, 0
}

// scanShow resolves show → season → episode for every file of a show the way
// the scanner's processShowHierarchy does, and returns each file's episode ID.
func scanShow(t *testing.T, svc *Service, ctx context.Context, lib uuid.UUID, folderTitle, folder string, seasons, episodes int) []uuid.UUID {
	t.Helper()
	var out []uuid.UUID
	for s := 1; s <= seasons; s++ {
		for e := 1; e <= episodes; e++ {
			show, err := svc.FindOrCreateHierarchyItem(ctx, CreateItemParams{
				LibraryID: lib, Type: "show", Title: folderTitle, SortTitle: folderTitle, FolderPath: folder,
			})
			if err != nil {
				t.Fatal(err)
			}
			sn := s
			season, err := svc.FindOrCreateHierarchyItem(ctx, CreateItemParams{
				LibraryID: lib, Type: "season", Title: fmt.Sprintf("Season %d", s), ParentID: &show.ID, Index: &sn,
			})
			if err != nil {
				t.Fatal(err)
			}
			en := e
			ep, err := svc.FindOrCreateHierarchyItem(ctx, CreateItemParams{
				LibraryID: lib, Type: "episode", Title: fmt.Sprintf("Episode %d", e), ParentID: &season.ID, Index: &en,
			})
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, ep.ID)
		}
	}
	return out
}

// TestScanCache_RenamedShowQueryReduction measures the case known issue 3 is
// about: a show whose row was renamed (Fix Match / enrichment) so every title
// key misses and each episode file ran the exact, flexible and full-text
// lookups before the folder fallback found it. A first scan creates the
// seasons and episodes; the measured rescan resolves the same 60 files again,
// once without and once with the per-scan cache, and must resolve every file
// to the same episode either way.
func TestScanCache_RenamedShowQueryReduction(t *testing.T) {
	const seasons, episodes = 3, 20
	q := newCountingQuerier()
	svc := NewService(q, q, slog.Default())
	lib := uuid.New()
	const folder = "/tv/The Floor US/"

	show, _ := q.CreateMediaItem(context.Background(), CreateItemParams{LibraryID: lib, Type: "show", Title: "The Floor"})
	q.folders[folder] = show.ID
	first := scanShow(t, svc, context.Background(), lib, "The Floor US", folder, seasons, episodes)

	q.reset()
	uncached := scanShow(t, svc, context.Background(), lib, "The Floor US", folder, seasons, episodes)
	before := *q

	q.reset()
	cache := NewScanCache(0)
	cached := scanShow(t, svc, WithScanCache(context.Background(), cache), lib, "The Floor US", folder, seasons, episodes)
	after := *q

	for i := range first {
		if uncached[i] != first[i] || cached[i] != first[i] {
			t.Fatalf("file %d resolved differently: first %s, uncached %s, cached %s", i, first[i], uncached[i], cached[i])
		}
	}
	if before.create != 0 || after.create != 0 {
		t.Fatalf("rescan created items (uncached %d, cached %d), want none", before.create, after.create)
	}

	files := seasons * episodes
	t.Logf("rescan of %d files, renamed show: queries %d → %d; full-text searches %d → %d; child rows read %d → %d; cache %+v",
		files, before.queries(), after.queries(), before.search, after.search, before.childRows, after.childRows, cache.Stats())

	if before.search != files {
		t.Errorf("uncached full-text searches = %d, want one per file (%d)", before.search, files)
	}
	if after.search != 1 {
		t.Errorf("cached full-text searches = %d, want 1 (the show's first lookup)", after.search)
	}
	// Uncached: 4 show lookups + 2 child listings per file. Cached: the
	// show's and season's repeats become one primary-key read each.
	if want := files * 6; before.queries() != want {
		t.Errorf("uncached queries = %d, want %d", before.queries(), want)
	}
	// Cached: one read each for show and season plus the episode's own
	// listing, after the first file's full lookups.
	if limit := files*3 + 5; after.queries() > limit {
		t.Errorf("cached queries = %d, want at most %d", after.queries(), limit)
	}
}

// TestScanCache_ExactTitleShowRowsRead: when the show's title matches its row
// the uncached show lookup is already one query, but every file still listed
// all of the show's seasons. The cache replaces that listing with one row.
func TestScanCache_ExactTitleShowRowsRead(t *testing.T) {
	const seasons, episodes = 5, 24
	q := newCountingQuerier()
	svc := NewService(q, q, slog.Default())
	lib := uuid.New()
	scanShow(t, svc, context.Background(), lib, "Show", "/tv/Show/", seasons, episodes)

	q.reset()
	scanShow(t, svc, context.Background(), lib, "Show", "/tv/Show/", seasons, episodes)
	before := *q
	q.reset()
	scanShow(t, svc, WithScanCache(context.Background(), NewScanCache(0)), lib, "Show", "/tv/Show/", seasons, episodes)
	after := *q

	t.Logf("rescan of %d files, exact-title show: queries %d → %d; title lookups %d → %d; child listings %d → %d (rows %d → %d)",
		seasons*episodes, before.queries(), after.queries(), before.exact, after.exact,
		before.children, after.children, before.childRows, after.childRows)
	if after.exact != 1 {
		t.Errorf("cached title lookups = %d, want 1", after.exact)
	}
	if after.children >= before.children || after.childRows >= before.childRows {
		t.Errorf("child listings %d→%d, rows %d→%d: want both lower with the cache",
			before.children, after.children, before.childRows, after.childRows)
	}
}

// TestScanCache_DeletedItemFallsThrough: a show merged away mid-scan (its row
// soft-deleted) must not keep collecting episodes; the next lookup runs in
// full and finds the survivor.
func TestScanCache_DeletedItemFallsThrough(t *testing.T) {
	q := newCountingQuerier()
	svc := NewService(q, q, slog.Default())
	lib := uuid.New()
	cache := NewScanCache(0)
	ctx := WithScanCache(context.Background(), cache)
	p := CreateItemParams{LibraryID: lib, Type: "show", Title: "Frieren", FolderPath: "/tv/Frieren/"}

	first, err := svc.FindOrCreateHierarchyItem(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	// Enrichment merges the new row into the canonical one: the loser is
	// soft-deleted and its files now resolve through the survivor's folder.
	survivor, _ := q.CreateMediaItem(ctx, CreateItemParams{LibraryID: lib, Type: "show", Title: "Frieren: Beyond Journey's End"})
	delete(q.items, first.ID)
	q.folders["/tv/Frieren/"] = survivor.ID

	got, err := svc.FindOrCreateHierarchyItem(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != survivor.ID {
		t.Fatalf("lookup after the merge returned %s, want the survivor %s", got.ID, survivor.ID)
	}
	if st := cache.Stats(); st.Stale != 1 {
		t.Errorf("stale = %d, want 1", st.Stale)
	}
	// And the survivor is now the cached answer.
	q.reset()
	if again, _ := svc.FindOrCreateHierarchyItem(ctx, p); again.ID != survivor.ID || q.queries() != 1 {
		t.Errorf("third lookup: %s with %d queries, want the survivor from one read", again.ID, q.queries())
	}
}

// TestScanCache_ReparentedItemFallsThrough: a season moved under another show
// no longer matches its cached key.
func TestScanCache_ReparentedItemFallsThrough(t *testing.T) {
	q := newCountingQuerier()
	svc := NewService(q, q, slog.Default())
	lib := uuid.New()
	ctx := WithScanCache(context.Background(), NewScanCache(0))
	show, _ := svc.FindOrCreateHierarchyItem(ctx, CreateItemParams{LibraryID: lib, Type: "show", Title: "A"})
	one := 1
	p := CreateItemParams{LibraryID: lib, Type: "season", Title: "Season 1", ParentID: &show.ID, Index: &one}
	season, _ := svc.FindOrCreateHierarchyItem(ctx, p)

	other := uuid.New()
	moved := q.items[season.ID]
	moved.ParentID = &other
	q.items[season.ID] = moved

	got, _ := svc.FindOrCreateHierarchyItem(ctx, p)
	if got.ID == season.ID {
		t.Fatal("cache returned a season that now belongs to another show")
	}
}

// TestScanCache_TrackDiscClaimedElsewhere pins the music scanner's retry: after
// another file fills a track row's disc with a different number, looking the
// same params up again must not return that row.
func TestScanCache_TrackDiscClaimedElsewhere(t *testing.T) {
	q := newCountingQuerier()
	svc := NewService(q, q, slog.Default())
	lib := uuid.New()
	ctx := WithScanCache(context.Background(), NewScanCache(0))
	album := uuid.New()
	one, two := 1, 2
	p := CreateItemParams{LibraryID: lib, Type: "track", Title: "Song", ParentID: &album, Index: &one, DiscNumber: &two}

	track, _ := svc.FindOrCreateHierarchyItem(ctx, p)
	claimed := q.items[track.ID]
	claimed.DiscNumber = &one // the other file's fill won
	q.items[track.ID] = claimed

	again, _ := svc.FindOrCreateHierarchyItem(ctx, p)
	if again.ID == track.ID {
		t.Fatal("re-lookup returned the row another disc claimed")
	}
}

// TestScanCache_UncacheableStillCreates: a lookup that can never find an
// existing item (no title, no index) creates a fresh item every time, with or
// without the cache.
func TestScanCache_UncacheableStillCreates(t *testing.T) {
	q := newCountingQuerier()
	svc := NewService(q, q, slog.Default())
	ctx := WithScanCache(context.Background(), NewScanCache(0))
	p := CreateItemParams{LibraryID: uuid.New(), Type: "movie"}
	a, _ := svc.FindOrCreateItem(ctx, p)
	b, _ := svc.FindOrCreateItem(ctx, p)
	if a.ID == b.ID {
		t.Fatal("untitled lookups were collapsed onto one item by the cache")
	}
}

// TestScanCache_SeparateScansDontShare: the cache is scoped to the context it
// was attached to; another scan (or a non-scan caller) never sees it.
func TestScanCache_SeparateScansDontShare(t *testing.T) {
	q := newCountingQuerier()
	svc := NewService(q, q, slog.Default())
	p := CreateItemParams{LibraryID: uuid.New(), Type: "movie", Title: "Heat"}
	c1 := NewScanCache(0)
	if _, err := svc.FindOrCreateItem(WithScanCache(context.Background(), c1), p); err != nil {
		t.Fatal(err)
	}
	c2 := NewScanCache(0)
	if _, err := svc.FindOrCreateItem(WithScanCache(context.Background(), c2), p); err != nil {
		t.Fatal(err)
	}
	if c2.Stats().Hits != 0 || c1.Stats().Size != 1 || c2.Stats().Size != 1 {
		t.Errorf("caches shared state: c1 %+v c2 %+v", c1.Stats(), c2.Stats())
	}
}

func TestScanCache_BoundedLRU(t *testing.T) {
	c := NewScanCache(2)
	a, b, d := uuid.New(), uuid.New(), uuid.New()
	c.put("a", a)
	c.put("b", b)
	c.get("a") // a is now the most recent
	c.put("d", d)
	if st := c.Stats(); st.Size != 2 {
		t.Fatalf("size = %d, want 2", st.Size)
	}
	if _, ok := c.get("b"); ok {
		t.Error("least recently used entry survived eviction")
	}
	if id, ok := c.get("a"); !ok || id != a {
		t.Error("recently used entry was evicted")
	}
}

func TestScanCacheKey_DistinguishesInputs(t *testing.T) {
	lib, parent := uuid.New(), uuid.New()
	y1, y2, i1 := 2001, 2002, 1
	base := CreateItemParams{LibraryID: lib, Type: "show", Title: "X"}
	variants := []CreateItemParams{
		base,
		{LibraryID: lib, Type: "movie", Title: "X"},
		{LibraryID: lib, Type: "show", Title: "Y"},
		{LibraryID: lib, Type: "show", Title: "X", Year: &y1},
		{LibraryID: lib, Type: "show", Title: "X", Year: &y2},
		{LibraryID: lib, Type: "show", Title: "X", TMDBID: &y1},
		{LibraryID: lib, Type: "show", Title: "X", ParentID: &parent},
		{LibraryID: lib, Type: "show", Title: "X", Index: &i1},
		{LibraryID: lib, Type: "show", Title: "X", DiscNumber: &i1},
		{LibraryID: lib, Type: "show", Title: "X", FolderPath: "/a/"},
	}
	seen := map[string]int{}
	for i, v := range variants {
		k := scanCacheKey(scanKindHierarchy, v)
		if j, dup := seen[k]; dup {
			t.Errorf("variants %d and %d share key %q", j, i, strings.ReplaceAll(k, "\x00", "|"))
		}
		seen[k] = i
	}
	if scanCacheKey(scanKindItem, base) == scanCacheKey(scanKindHierarchy, base) {
		t.Error("the two lookup kinds share a key")
	}
}
