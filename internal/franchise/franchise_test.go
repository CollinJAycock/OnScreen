package franchise

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata"
)

// ── in-memory DB fake mirroring the franchise_collections.sql semantics ─────

type fakeMovie struct {
	id       uuid.UUID
	tmdbID   *int32
	released time.Time
	deleted  bool
	order    int
}

type fakeCollection struct {
	id   uuid.UUID
	name string
	desc *string
}

type fakeDB struct {
	mu        sync.Mutex
	now       time.Time
	movies    map[uuid.UUID]*fakeMovie
	links     map[uuid.UUID]gen.MediaItemTmdbCollection
	snapshots map[int32]gen.TmdbCollection
	cols      map[int32]*fakeCollection
	items     map[uuid.UUID][]uuid.UUID
	lastLim   int32
	nextOrder int
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		now:       time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		movies:    map[uuid.UUID]*fakeMovie{},
		links:     map[uuid.UUID]gen.MediaItemTmdbCollection{},
		snapshots: map[int32]gen.TmdbCollection{},
		cols:      map[int32]*fakeCollection{},
		items:     map[uuid.UUID][]uuid.UUID{},
	}
}

func (f *fakeDB) addMovie(tmdbID int, released string) uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	d, _ := time.Parse("2006-01-02", released)
	v := int32(tmdbID)
	f.nextOrder++
	f.movies[id] = &fakeMovie{id: id, tmdbID: &v, released: d, order: f.nextOrder}
	return id
}

func (f *fakeDB) GetMovieTMDBCollectionLink(_ context.Context, id uuid.UUID) (gen.MediaItemTmdbCollection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.links[id]
	if !ok {
		return gen.MediaItemTmdbCollection{}, pgx.ErrNoRows
	}
	return l, nil
}

func (f *fakeDB) UpsertMovieTMDBCollectionLink(_ context.Context, a gen.UpsertMovieTMDBCollectionLinkParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.links[a.MediaItemID] = gen.MediaItemTmdbCollection{MediaItemID: a.MediaItemID, TmdbID: a.TmdbID, TmdbCollectionID: a.TmdbCollectionID}
	return nil
}

func (f *fakeDB) LinkMoviesToTMDBCollection(_ context.Context, a gen.LinkMoviesToTMDBCollectionParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := map[int32]bool{}
	for _, id := range a.PartTmdbIds {
		want[id] = true
	}
	for _, m := range f.movies {
		if m.deleted || m.tmdbID == nil || !want[*m.tmdbID] {
			continue
		}
		cur, ok := f.links[m.id]
		if ok && cur.TmdbCollectionID != nil && cur.TmdbID == *m.tmdbID {
			continue // fresh link — left alone
		}
		cid := a.TmdbCollectionID
		f.links[m.id] = gen.MediaItemTmdbCollection{MediaItemID: m.id, TmdbID: *m.tmdbID, TmdbCollectionID: &cid}
	}
	return nil
}

func (f *fakeDB) needsCheck() []*fakeMovie {
	var out []*fakeMovie
	for _, m := range f.movies {
		if m.deleted || m.tmdbID == nil {
			continue
		}
		l, ok := f.links[m.id]
		if !ok || l.TmdbID != *m.tmdbID {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].order < out[j].order })
	return out
}

func (f *fakeDB) ListMoviesNeedingTMDBCollectionCheck(_ context.Context, lim int32) ([]gen.ListMoviesNeedingTMDBCollectionCheckRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastLim = lim
	var out []gen.ListMoviesNeedingTMDBCollectionCheckRow
	for _, m := range f.needsCheck() {
		if int32(len(out)) >= lim {
			break
		}
		out = append(out, gen.ListMoviesNeedingTMDBCollectionCheckRow{ID: m.id, TmdbID: m.tmdbID})
	}
	return out, nil
}

func (f *fakeDB) CountMoviesNeedingTMDBCollectionCheck(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int64(len(f.needsCheck())), nil
}

func (f *fakeDB) GetTMDBCollectionSnapshot(_ context.Context, id int32) (gen.TmdbCollection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.snapshots[id]
	if !ok {
		return gen.TmdbCollection{}, pgx.ErrNoRows
	}
	return s, nil
}

func (f *fakeDB) UpsertTMDBCollectionSnapshot(_ context.Context, a gen.UpsertTMDBCollectionSnapshotParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshots[a.TmdbCollectionID] = gen.TmdbCollection{
		TmdbCollectionID: a.TmdbCollectionID, Name: a.Name, Overview: a.Overview,
		PosterUrl: a.PosterUrl, BackdropUrl: a.BackdropUrl, Parts: a.Parts,
		FetchedAt: pgtype.Timestamptz{Time: f.now, Valid: true},
	}
	return nil
}

func (f *fakeDB) InsertTMDBCollectionPlaceholder(_ context.Context, a gen.InsertTMDBCollectionPlaceholderParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.snapshots[a.TmdbCollectionID]; ok {
		return nil
	}
	f.snapshots[a.TmdbCollectionID] = gen.TmdbCollection{
		TmdbCollectionID: a.TmdbCollectionID, Name: a.Name, PosterUrl: a.PosterUrl, BackdropUrl: a.BackdropUrl,
		Parts:     []byte("[]"),
		FetchedAt: pgtype.Timestamptz{Time: time.Unix(0, 0).UTC(), Valid: true},
	}
	return nil
}

func (f *fakeDB) ListStaleFranchiseSnapshots(_ context.Context, a gen.ListStaleFranchiseSnapshotsParams) ([]int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []int32
	for cid, s := range f.snapshots {
		if _, ok := f.cols[cid]; ok && s.FetchedAt.Time.Before(a.OlderThan.Time) {
			out = append(out, cid)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if int32(len(out)) > a.Lim {
		out = out[:a.Lim]
	}
	return out, nil
}

func (f *fakeDB) members(cid int32) []*fakeMovie {
	var out []*fakeMovie
	for id, l := range f.links {
		m := f.movies[id]
		if m == nil || m.deleted || l.TmdbCollectionID == nil || *l.TmdbCollectionID != cid || m.tmdbID == nil || *m.tmdbID != l.TmdbID {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].released.Before(out[j].released) })
	return out
}

func (f *fakeDB) CountFranchiseMembers(_ context.Context, cid *int32) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	distinct := map[int32]bool{}
	for _, m := range f.members(*cid) {
		distinct[*m.tmdbID] = true
	}
	return int64(len(distinct)), nil
}

func (f *fakeDB) UpsertFranchiseCollection(_ context.Context, a gen.UpsertFranchiseCollectionParams) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.cols[a.TmdbCollectionID]
	if !ok {
		c = &fakeCollection{id: uuid.New()}
		f.cols[a.TmdbCollectionID] = c
	}
	c.name, c.desc = a.Name, a.Description
	return c.id, nil
}

func (f *fakeDB) SyncFranchiseCollectionItems(_ context.Context, a gen.SyncFranchiseCollectionItemsParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []uuid.UUID
	for _, m := range f.members(a.TmdbCollectionID) {
		ids = append(ids, m.id)
	}
	f.items[a.CollectionID] = ids
	return nil
}

func (f *fakeDB) DeleteFranchiseCollection(_ context.Context, cid *int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.cols[*cid]; ok {
		delete(f.items, c.id)
		delete(f.cols, *cid)
	}
	return nil
}

func (f *fakeDB) ListFranchiseSyncCandidates(context.Context) ([]int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	set := map[int32]bool{}
	for cid := range f.cols {
		set[cid] = true
	}
	counts := map[int32]int{}
	for _, l := range f.links {
		if l.TmdbCollectionID != nil {
			counts[*l.TmdbCollectionID]++
		}
	}
	for cid, n := range counts {
		if n >= 2 {
			set[cid] = true
		}
	}
	var out []int32
	for cid := range set {
		out = append(out, cid)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func (f *fakeDB) collectionItems(cid int32) (string, []uuid.UUID, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.cols[cid]
	if !ok {
		return "", nil, false
	}
	return c.name, f.items[c.id], true
}

// ── TMDB fake ────────────────────────────────────────────────────────────────

type fakeTMDB struct {
	mu          sync.Mutex
	movies      map[int]*metadata.CollectionRef // movie tmdb id → collection (nil = none)
	movieErr    map[int]error
	collections map[int]*metadata.CollectionDetail
	collErr     error
	movieCalls  int
	collCalls   map[int]int
}

func newFakeTMDB() *fakeTMDB {
	return &fakeTMDB{
		movies:      map[int]*metadata.CollectionRef{},
		movieErr:    map[int]error{},
		collections: map[int]*metadata.CollectionDetail{},
		collCalls:   map[int]int{},
	}
}

func (t *fakeTMDB) MovieCollection(_ context.Context, id int) (*metadata.CollectionRef, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.movieCalls++
	if err := t.movieErr[id]; err != nil {
		return nil, err
	}
	return t.movies[id], nil
}

func (t *fakeTMDB) GetCollection(_ context.Context, id int) (*metadata.CollectionDetail, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.collCalls[id]++
	if t.collErr != nil {
		return nil, t.collErr
	}
	c, ok := t.collections[id]
	if !ok {
		return nil, ErrNotFound
	}
	return c, nil
}

func (t *fakeTMDB) totalCollCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, c := range t.collCalls {
		n += c
	}
	return n
}

func alienDetail() *metadata.CollectionDetail {
	return &metadata.CollectionDetail{
		TMDBID: 8091, Name: "Alien Collection", Overview: "Xenomorphs.",
		PosterURL: "https://image.tmdb.org/t/p/original/p.jpg",
		Parts: []metadata.CollectionPart{
			{TMDBID: 348, Title: "Alien", ReleaseDate: "1979-05-25", Year: 1979},
			{TMDBID: 679, Title: "Aliens", ReleaseDate: "1986-07-18", Year: 1986},
			{TMDBID: 8077, Title: "Alien³", ReleaseDate: "1992-05-22", Year: 1992},
		},
	}
}

var alienRef = &metadata.CollectionRef{TMDBID: 8091, Name: "Alien Collection"}

func newSvc(db *fakeDB, t *fakeTMDB) *Service {
	var fn func() TMDB
	if t != nil {
		fn = func() TMDB { return t }
	}
	s := New(db, fn, slog.Default())
	s.now = func() time.Time { return db.now }
	return s
}

// ── RecordMovie / Sync ───────────────────────────────────────────────────────

func TestRecordMovie_OneMemberNoCollection(t *testing.T) {
	db, tm := newFakeDB(), newFakeTMDB()
	tm.collections[8091] = alienDetail()
	svc := newSvc(db, tm)
	alien := db.addMovie(348, "1979-05-25")

	if err := svc.RecordMovie(context.Background(), alien, 348, alienRef); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := db.collectionItems(8091); ok {
		t.Fatal("a single movie must not create a franchise collection")
	}
	if _, ok := db.snapshots[8091]; !ok {
		t.Fatal("first sight of a collection stores its snapshot")
	}
}

func TestRecordMovie_SecondMemberCreatesOrderedFranchise(t *testing.T) {
	db, tm := newFakeDB(), newFakeTMDB()
	tm.collections[8091] = alienDetail()
	svc := newSvc(db, tm)
	ctx := context.Background()
	// Enriched out of release order on purpose.
	aliens := db.addMovie(679, "1986-07-18")
	alien := db.addMovie(348, "1979-05-25")

	if err := svc.RecordMovie(ctx, aliens, 679, alienRef); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordMovie(ctx, alien, 348, alienRef); err != nil {
		t.Fatal(err)
	}
	name, items, ok := db.collectionItems(8091)
	if !ok {
		t.Fatal("two movies of one TMDB collection must create the franchise")
	}
	if name != "Alien Collection" {
		t.Errorf("name = %q", name)
	}
	if len(items) != 2 || items[0] != alien || items[1] != aliens {
		t.Fatalf("members must be in release order: %v (alien=%s aliens=%s)", items, alien, aliens)
	}
	if tm.collCalls[8091] != 1 {
		t.Errorf("collection fetched %d times; the stored snapshot must be reused", tm.collCalls[8091])
	}
}

// A film enriched before its sequel existed was recorded as "no collection";
// the sequel's snapshot fetch links it through the parts list.
func TestRecordMovie_PartsLinkPreviouslyStandaloneMovie(t *testing.T) {
	db, tm := newFakeDB(), newFakeTMDB()
	tm.collections[8091] = alienDetail()
	svc := newSvc(db, tm)
	ctx := context.Background()
	alien := db.addMovie(348, "1979-05-25")
	aliens := db.addMovie(679, "1986-07-18")

	if err := svc.RecordMovie(ctx, alien, 348, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordMovie(ctx, aliens, 679, alienRef); err != nil {
		t.Fatal(err)
	}
	_, items, ok := db.collectionItems(8091)
	if !ok || len(items) != 2 {
		t.Fatalf("parts-driven linking must pick up the earlier film: ok=%v items=%v", ok, items)
	}
}

// The same film in two libraries (4K + 1080p) is one part, not two.
func TestRecordMovie_SameFilmTwiceIsOnePart(t *testing.T) {
	db, tm := newFakeDB(), newFakeTMDB()
	tm.collections[8091] = alienDetail()
	svc := newSvc(db, tm)
	ctx := context.Background()
	a1 := db.addMovie(348, "1979-05-25")
	a2 := db.addMovie(348, "1979-05-25")
	for _, id := range []uuid.UUID{a1, a2} {
		if err := svc.RecordMovie(ctx, id, 348, alienRef); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, ok := db.collectionItems(8091); ok {
		t.Fatal("two copies of one film must not form a franchise")
	}
}

// Re-matching a member to a film outside the collection drops the franchise
// below two members, which removes it.
func TestRecordMovie_RematchRemovesFranchise(t *testing.T) {
	db, tm := newFakeDB(), newFakeTMDB()
	tm.collections[8091] = alienDetail()
	svc := newSvc(db, tm)
	ctx := context.Background()
	alien := db.addMovie(348, "1979-05-25")
	aliens := db.addMovie(679, "1986-07-18")
	_ = svc.RecordMovie(ctx, alien, 348, alienRef)
	_ = svc.RecordMovie(ctx, aliens, 679, alienRef)
	if _, _, ok := db.collectionItems(8091); !ok {
		t.Fatal("precondition: franchise exists")
	}

	// Fix Match: "aliens" was actually a different, standalone film.
	v := int32(550)
	db.movies[aliens].tmdbID = &v
	if err := svc.RecordMovie(ctx, aliens, 550, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := db.collectionItems(8091); ok {
		t.Fatal("franchise with one remaining member must be removed")
	}
}

func TestSync_NoSnapshotFallsBackToRefName(t *testing.T) {
	db, tm := newFakeDB(), newFakeTMDB()
	tm.collErr = ErrUnavailable
	svc := newSvc(db, tm)
	ctx := context.Background()
	alien := db.addMovie(348, "1979-05-25")
	aliens := db.addMovie(679, "1986-07-18")
	_ = svc.RecordMovie(ctx, alien, 348, alienRef)
	if err := svc.RecordMovie(ctx, aliens, 679, alienRef); err != nil {
		t.Fatal(err)
	}
	name, items, ok := db.collectionItems(8091)
	if !ok || name != "Alien Collection" || len(items) != 2 {
		t.Fatalf("TMDB down: franchise must still be created from the movie's belongs_to_collection name: ok=%v name=%q items=%d", ok, name, len(items))
	}
	// A ref-less re-sync (reconcile, or the old collection of a re-matched
	// movie) still has the name, via the stored placeholder.
	if err := svc.Sync(ctx, 8091); err != nil {
		t.Fatalf("ref-less sync after a placeholder: %v", err)
	}
	// TMDB is back: the next sync replaces the placeholder with the real
	// snapshot (placeholders count as missing).
	tm.collErr = nil
	tm.collections[8091] = alienDetail()
	before := tm.collCalls[8091]
	if err := svc.Sync(ctx, 8091); err != nil {
		t.Fatal(err)
	}
	if tm.collCalls[8091] != before+1 || len(DecodeParts(db.snapshots[8091].Parts)) != 3 {
		t.Fatalf("placeholder not replaced: calls=%d parts=%s", tm.collCalls[8091]-before, db.snapshots[8091].Parts)
	}
}

func TestSync_NoNameIsAnErrorNotAnUnnamedCollection(t *testing.T) {
	db, tm := newFakeDB(), newFakeTMDB()
	tm.collErr = ErrUnavailable
	svc := newSvc(db, tm)
	ctx := context.Background()
	for _, m := range []struct {
		tmdb int
		date string
	}{{348, "1979-05-25"}, {679, "1986-07-18"}} {
		id := db.addMovie(m.tmdb, m.date)
		cid := int32(8091)
		db.links[id] = gen.MediaItemTmdbCollection{MediaItemID: id, TmdbID: int32(m.tmdb), TmdbCollectionID: &cid}
	}
	err := svc.Sync(ctx, 8091)
	if err == nil || !strings.Contains(err.Error(), "no name") {
		t.Fatalf("want a no-name error, got %v", err)
	}
	if _, _, ok := db.collectionItems(8091); ok {
		t.Fatal("must not create an unnamed collection")
	}
}

func TestRecordMovie_IgnoresUnmatchedMovie(t *testing.T) {
	db := newFakeDB()
	svc := newSvc(db, nil)
	if err := svc.RecordMovie(context.Background(), uuid.New(), 0, alienRef); err != nil {
		t.Fatal(err)
	}
	if len(db.links) != 0 {
		t.Fatal("a movie without a TMDB id must not be recorded")
	}
}

// ── Maintain ─────────────────────────────────────────────────────────────────

func TestMaintain_BackfillIsBounded(t *testing.T) {
	db, tm := newFakeDB(), newFakeTMDB()
	svc := newSvc(db, tm)
	// Ten matched movies in five different two-film collections.
	for i := 0; i < 10; i++ {
		tmdbID := 1000 + i
		cid := 5000 + i/2
		db.addMovie(tmdbID, "2000-01-01")
		tm.movies[tmdbID] = &metadata.CollectionRef{TMDBID: cid, Name: "C"}
		// No parts: keeps parts-driven linking out of this test, so every
		// queue exit is a lookup this run paid for.
		tm.collections[cid] = &metadata.CollectionDetail{TMDBID: cid, Name: "C"}
	}

	res, err := svc.Maintain(context.Background(), MaintainOptions{BackfillLimit: 3, FetchBudget: 2})
	if err != nil {
		t.Fatal(err)
	}
	if db.lastLim != 3 {
		t.Errorf("backfill queue limit = %d, want 3", db.lastLim)
	}
	if tm.movieCalls != 3 || res.Checked != 3 {
		t.Errorf("movie lookups = %d (checked %d), want 3", tm.movieCalls, res.Checked)
	}
	if got := tm.totalCollCalls(); got > 2 {
		t.Errorf("collection fetches = %d, want <= FetchBudget (2)", got)
	}
	if res.Remaining != 7 {
		t.Errorf("remaining = %d, want 7", res.Remaining)
	}

	// Ceilings: absurd limits are clamped.
	if _, err := svc.Maintain(context.Background(), MaintainOptions{BackfillLimit: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	if db.lastLim != MaxBackfillLimit {
		t.Errorf("clamped limit = %d, want %d", db.lastLim, MaxBackfillLimit)
	}
}

func TestMaintain_BackfillBuildsFranchises(t *testing.T) {
	db, tm := newFakeDB(), newFakeTMDB()
	tm.collections[8091] = alienDetail()
	tm.movies[348] = alienRef
	tm.movies[679] = alienRef
	tm.movieErr[404404] = ErrNotFound
	svc := newSvc(db, tm)
	db.addMovie(348, "1979-05-25")
	db.addMovie(679, "1986-07-18")
	gone := db.addMovie(404404, "2001-01-01")

	res, err := svc.Maintain(context.Background(), MaintainOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Checked != 3 || res.InCollection != 2 || res.NotFound != 1 || res.Remaining != 0 {
		t.Fatalf("result: %+v", res)
	}
	if _, items, ok := db.collectionItems(8091); !ok || len(items) != 2 {
		t.Fatalf("franchise not built: ok=%v items=%v", ok, items)
	}
	if l, ok := db.links[gone]; !ok || l.TmdbCollectionID != nil {
		t.Fatal("a TMDB 404 must be recorded as no-collection so it leaves the queue")
	}
}

func TestMaintain_TMDBUnavailableStopsLookupsButReconciles(t *testing.T) {
	db, tm := newFakeDB(), newFakeTMDB()
	svc := newSvc(db, tm)
	ctx := context.Background()
	// An existing franchise whose member was deleted since.
	tm.collections[8091] = alienDetail()
	alien := db.addMovie(348, "1979-05-25")
	aliens := db.addMovie(679, "1986-07-18")
	_ = svc.RecordMovie(ctx, alien, 348, alienRef)
	_ = svc.RecordMovie(ctx, aliens, 679, alienRef)
	db.movies[aliens].deleted = true

	for i := 0; i < 3; i++ {
		id := 2000 + i
		db.addMovie(id, "2000-01-01")
		tm.movieErr[id] = ErrUnavailable
	}
	before := tm.movieCalls
	res, err := svc.Maintain(ctx, MaintainOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Aborted || tm.movieCalls-before != 1 {
		t.Fatalf("lookups must stop at the first unavailable answer: aborted=%v calls=%d", res.Aborted, tm.movieCalls-before)
	}
	if _, _, ok := db.collectionItems(8091); ok {
		t.Fatal("reconcile must still remove the franchise that fell below two members")
	}
}

func TestMaintain_NoTMDBConfigured(t *testing.T) {
	db := newFakeDB()
	db.addMovie(348, "1979-05-25")
	res, err := newSvc(db, nil).Maintain(context.Background(), MaintainOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Aborted || res.Checked != 0 || res.Remaining != 1 {
		t.Fatalf("result: %+v", res)
	}
	if !strings.Contains(res.String(), "tmdb unavailable") {
		t.Errorf("summary should say why: %s", res.String())
	}
}

func TestMaintain_RefreshesStaleSnapshots(t *testing.T) {
	db, tm := newFakeDB(), newFakeTMDB()
	tm.collections[8091] = alienDetail()
	svc := newSvc(db, tm)
	ctx := context.Background()
	alien := db.addMovie(348, "1979-05-25")
	aliens := db.addMovie(679, "1986-07-18")
	_ = svc.RecordMovie(ctx, alien, 348, alienRef)
	_ = svc.RecordMovie(ctx, aliens, 679, alienRef)
	if tm.collCalls[8091] != 1 {
		t.Fatalf("precondition: one fetch, got %d", tm.collCalls[8091])
	}

	// Fresh snapshot: not refetched.
	if _, err := svc.Maintain(ctx, MaintainOptions{}); err != nil {
		t.Fatal(err)
	}
	if tm.collCalls[8091] != 1 {
		t.Fatalf("fresh snapshot refetched: %d", tm.collCalls[8091])
	}

	// A new sequel is announced and the snapshot ages past the TTL.
	d := alienDetail()
	d.Parts = append(d.Parts, metadata.CollectionPart{TMDBID: 945961, Title: "Alien: Romulus", ReleaseDate: "2024-08-13", Year: 2024})
	tm.collections[8091] = d
	db.now = db.now.Add(SnapshotTTL + time.Hour)
	res, err := svc.Maintain(ctx, MaintainOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Refreshed != 1 || tm.collCalls[8091] != 2 {
		t.Fatalf("stale snapshot not refreshed: refreshed=%d calls=%d", res.Refreshed, tm.collCalls[8091])
	}
	parts := DecodeParts(db.snapshots[8091].Parts)
	if len(parts) != 4 || parts[3].TMDBID != 945961 {
		t.Fatalf("refreshed parts: %+v", parts)
	}
}

func TestDecodeParts(t *testing.T) {
	if DecodeParts(nil) != nil || DecodeParts([]byte("not json")) != nil {
		t.Fatal("empty / malformed input must decode to no parts")
	}
	p := DecodeParts([]byte(`[{"tmdb_id":348,"title":"Alien","year":1979}]`))
	if len(p) != 1 || p[0].TMDBID != 348 || p[0].Year != 1979 {
		t.Fatalf("decode: %+v", p)
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	if errors.Is(ErrNotFound, ErrUnavailable) {
		t.Fatal("sentinels must differ")
	}
}
