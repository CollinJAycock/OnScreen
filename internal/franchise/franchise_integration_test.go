//go:build integration

// Real-Postgres coverage for the franchise_collections.sql queries and the
// Service driving them (migration 00027).
//
// Run with: go test -tags 'dev integration' ./internal/franchise/
package franchise_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/franchise"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/testdb"
)

type itTMDB struct {
	collections map[int]*metadata.CollectionDetail
	movies      map[int]*metadata.CollectionRef
}

func (t *itTMDB) MovieCollection(_ context.Context, id int) (*metadata.CollectionRef, error) {
	return t.movies[id], nil
}

func (t *itTMDB) GetCollection(_ context.Context, id int) (*metadata.CollectionDetail, error) {
	c, ok := t.collections[id]
	if !ok {
		return nil, franchise.ErrNotFound
	}
	return c, nil
}

type itEnv struct {
	ctx  context.Context
	pool *pgxpool.Pool
	q    *gen.Queries
	svc  *franchise.Service
	tmdb *itTMDB
}

func newEnv(t *testing.T) *itEnv {
	t.Helper()
	pool := testdb.New(t)
	tm := &itTMDB{
		collections: map[int]*metadata.CollectionDetail{
			8091: {
				TMDBID: 8091, Name: "Alien Collection", Overview: "Xenomorphs.",
				PosterURL: "https://image.tmdb.org/t/p/original/alien.jpg",
				Parts: []metadata.CollectionPart{
					{TMDBID: 348, Title: "Alien", ReleaseDate: "1979-05-25", Year: 1979},
					{TMDBID: 679, Title: "Aliens", ReleaseDate: "1986-07-18", Year: 1986},
					{TMDBID: 8077, Title: "Alien³", ReleaseDate: "1992-05-22", Year: 1992},
				},
			},
		},
		movies: map[int]*metadata.CollectionRef{},
	}
	q := gen.New(pool)
	return &itEnv{
		ctx: context.Background(), pool: pool, q: q, tmdb: tm,
		svc: franchise.New(q, func() franchise.TMDB { return tm }, slog.Default()),
	}
}

func (e *itEnv) library(t *testing.T, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.pool.QueryRow(e.ctx,
		`INSERT INTO libraries (name, type, scan_paths) VALUES ($1, 'movie', ARRAY['/m/' || $1]) RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("insert library: %v", err)
	}
	return id
}

// movie inserts a movie; rating "" = unrated (NULL).
func (e *itEnv) movie(t *testing.T, lib uuid.UUID, title string, tmdbID int, released, rating string) uuid.UUID {
	t.Helper()
	var cr *string
	if rating != "" {
		cr = &rating
	}
	poster := "movies/" + title + "/poster.jpg"
	var id uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `
		INSERT INTO media_items (library_id, type, title, sort_title, tmdb_id, originally_available_at, content_rating, poster_path)
		VALUES ($1, 'movie', $2, $2, $3, $4::date, $5, $6) RETURNING id`,
		lib, title, tmdbID, released, cr, poster).Scan(&id); err != nil {
		t.Fatalf("insert movie %s: %v", title, err)
	}
	return id
}

func (e *itEnv) franchiseRow(t *testing.T, cid int32) (gen.Collection, bool) {
	t.Helper()
	cols, err := e.q.ListCollections(e.ctx, pgtype.UUID{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cols {
		if c.Type == "franchise" && c.TmdbCollectionID != nil && *c.TmdbCollectionID == cid {
			return c, true
		}
	}
	return gen.Collection{}, false
}

func (e *itEnv) members(t *testing.T, colID uuid.UUID) []uuid.UUID {
	t.Helper()
	rows, err := e.q.ListCollectionItems(e.ctx, gen.ListCollectionItemsParams{CollectionID: colID})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

var alienRef = &metadata.CollectionRef{TMDBID: 8091, Name: "Alien Collection"}

func TestFranchise_Integration_CreateOrderAndRemove(t *testing.T) {
	e := newEnv(t)
	lib := e.library(t, "films")
	aliens := e.movie(t, lib, "Aliens", 679, "1986-07-18", "R")

	if err := e.svc.RecordMovie(e.ctx, aliens, 679, alienRef); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.franchiseRow(t, 8091); ok {
		t.Fatal("one movie must not create a franchise")
	}
	// The earlier film arrives later (a new scan) and is enriched.
	alien := e.movie(t, lib, "Alien", 348, "1979-05-25", "R")
	if err := e.svc.RecordMovie(e.ctx, alien, 348, alienRef); err != nil {
		t.Fatal(err)
	}
	col, ok := e.franchiseRow(t, 8091)
	if !ok {
		t.Fatal("two movies must create the franchise")
	}
	if col.Name != "Alien Collection" || col.Description == nil || *col.Description != "Xenomorphs." || col.UserID.Valid {
		t.Fatalf("franchise row = %+v", col)
	}
	if m := e.members(t, col.ID); len(m) != 2 || m[0] != alien || m[1] != aliens {
		t.Fatalf("members = %v, want [alien aliens] in release order", m)
	}

	// Idempotent: re-recording doesn't duplicate the collection or items.
	if err := e.svc.RecordMovie(e.ctx, alien, 348, alienRef); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT COUNT(*) FROM collections WHERE tmdb_collection_id = 8091`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("franchise rows = %d (%v), want exactly 1", n, err)
	}

	// Snapshot stored with parts.
	snap, err := e.q.GetTMDBCollectionSnapshot(e.ctx, 8091)
	if err != nil {
		t.Fatal(err)
	}
	if parts := franchise.DecodeParts(snap.Parts); len(parts) != 3 || parts[2].TMDBID != 8077 {
		t.Fatalf("snapshot parts = %+v", parts)
	}

	// Soft-delete a member: the nightly reconcile removes the franchise.
	if _, err := e.pool.Exec(e.ctx, `UPDATE media_items SET deleted_at = NOW() WHERE id = $1`, aliens); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Maintain(e.ctx, franchise.MaintainOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.franchiseRow(t, 8091); ok {
		t.Fatalf("franchise below two live members must be removed (result %+v)", res)
	}
}

// The same film in two libraries is one part; a second distinct film makes it
// a franchise with all three rows as members.
func TestFranchise_Integration_DistinctFilmsAcrossLibraries(t *testing.T) {
	e := newEnv(t)
	hd, uhd := e.library(t, "hd"), e.library(t, "uhd")
	a1 := e.movie(t, hd, "Alien", 348, "1979-05-25", "R")
	a2 := e.movie(t, uhd, "Alien", 348, "1979-05-25", "R")
	for _, id := range []uuid.UUID{a1, a2} {
		if err := e.svc.RecordMovie(e.ctx, id, 348, alienRef); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := e.franchiseRow(t, 8091); ok {
		t.Fatal("two copies of one film are not a franchise")
	}
	b := e.movie(t, uhd, "Aliens", 679, "1986-07-18", "R")
	if err := e.svc.RecordMovie(e.ctx, b, 679, alienRef); err != nil {
		t.Fatal(err)
	}
	col, ok := e.franchiseRow(t, 8091)
	if !ok || len(e.members(t, col.ID)) != 3 {
		t.Fatalf("franchise with both libraries' rows expected: ok=%v", ok)
	}
}

// Parts-driven linking fills a film recorded as "no collection" but never
// steals a film freshly linked to a different collection.
func TestFranchise_Integration_LinkByParts(t *testing.T) {
	e := newEnv(t)
	lib := e.library(t, "films")
	alien := e.movie(t, lib, "Alien", 348, "1979-05-25", "R")
	other := e.movie(t, lib, "Alien³", 8077, "1992-05-22", "R")
	if err := e.svc.RecordMovie(e.ctx, alien, 348, nil); err != nil {
		t.Fatal(err)
	}
	// Alien³ is (wrongly, per this fixture) linked to another collection.
	if err := e.svc.RecordMovie(e.ctx, other, 8077, &metadata.CollectionRef{TMDBID: 999, Name: "Elsewhere"}); err != nil {
		t.Fatal(err)
	}
	aliens := e.movie(t, lib, "Aliens", 679, "1986-07-18", "R")
	if err := e.svc.RecordMovie(e.ctx, aliens, 679, alienRef); err != nil {
		t.Fatal(err)
	}
	link, err := e.q.GetMovieTMDBCollectionLink(e.ctx, alien)
	if err != nil || link.TmdbCollectionID == nil || *link.TmdbCollectionID != 8091 {
		t.Fatalf("Alien must be linked through the parts list: %+v %v", link, err)
	}
	link, err = e.q.GetMovieTMDBCollectionLink(e.ctx, other)
	if err != nil || link.TmdbCollectionID == nil || *link.TmdbCollectionID != 999 {
		t.Fatalf("a fresh link to another collection must be left alone: %+v %v", link, err)
	}
	col, ok := e.franchiseRow(t, 8091)
	if !ok || len(e.members(t, col.ID)) != 2 {
		t.Fatal("franchise should hold Alien + Aliens")
	}
}

// Backfill queue: unchecked movies, and movies whose TMDB id changed since
// they were checked; bounded by the limit.
func TestFranchise_Integration_BackfillQueue(t *testing.T) {
	e := newEnv(t)
	lib := e.library(t, "films")
	a := e.movie(t, lib, "Alien", 348, "1979-05-25", "R")
	b := e.movie(t, lib, "Aliens", 679, "1986-07-18", "R")
	c := e.movie(t, lib, "Heat", 949, "1995-12-15", "R")
	// Unmatched movies never enter the queue.
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO media_items (library_id, type, title, sort_title) VALUES ($1, 'movie', 'Unmatched', 'Unmatched')`, lib); err != nil {
		t.Fatal(err)
	}

	rows, err := e.q.ListMoviesNeedingTMDBCollectionCheck(e.ctx, 2)
	if err != nil || len(rows) != 2 {
		t.Fatalf("bounded queue: %d rows (%v), want 2", len(rows), err)
	}
	if n, _ := e.q.CountMoviesNeedingTMDBCollectionCheck(e.ctx); n != 3 {
		t.Fatalf("queue size = %d, want 3", n)
	}

	e.tmdb.movies[348] = alienRef
	e.tmdb.movies[679] = alienRef
	res, err := e.svc.Maintain(e.ctx, franchise.MaintainOptions{BackfillLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Checked != 3 || res.InCollection != 2 || res.Remaining != 0 {
		t.Fatalf("maintain = %+v", res)
	}
	if _, ok := e.franchiseRow(t, 8091); !ok {
		t.Fatal("backfill must build the franchise")
	}

	// Fix Match on Heat: a new TMDB id re-queues it.
	if _, err := e.pool.Exec(e.ctx, `UPDATE media_items SET tmdb_id = 950 WHERE id = $1`, c); err != nil {
		t.Fatal(err)
	}
	rows, err = e.q.ListMoviesNeedingTMDBCollectionCheck(e.ctx, 10)
	if err != nil || len(rows) != 1 || rows[0].ID != c {
		t.Fatalf("re-matched movie must be re-queued: %+v %v", rows, err)
	}
	_, _ = a, b
}

// Visibility queries: library grants and the rating ceiling (unrated ranks
// 4, most restrictive), plus the per-library tab listing and the per-movie
// franchise lookup.
func TestFranchise_Integration_VisibilityQueries(t *testing.T) {
	e := newEnv(t)
	libA, libB := e.library(t, "a"), e.library(t, "b")
	alien := e.movie(t, libA, "Alien", 348, "1979-05-25", "R")
	aliens := e.movie(t, libB, "Aliens", 679, "1986-07-18", "") // unrated
	for id, tm := range map[uuid.UUID]int{alien: 348, aliens: 679} {
		if err := e.svc.RecordMovie(e.ctx, id, tm, alienRef); err != nil {
			t.Fatal(err)
		}
	}
	col, ok := e.franchiseRow(t, 8091)
	if !ok {
		t.Fatal("franchise missing")
	}
	rank := func(r int32) *int32 { return &r }

	for _, tc := range []struct {
		name string
		libs []uuid.UUID
		max  *int32
		want bool
	}{
		{"admin", nil, nil, true},
		{"lib A, no ceiling", []uuid.UUID{libA}, nil, true},
		{"lib A, PG-13 ceiling (Alien is R)", []uuid.UUID{libA}, rank(2), false},
		{"lib B, R ceiling (Aliens unrated = 4)", []uuid.UUID{libB}, rank(3), false},
		{"lib B, rank-4 ceiling", []uuid.UUID{libB}, rank(4), true},
		{"no grants", []uuid.UUID{}, nil, false},
	} {
		ids, err := e.q.ListVisibleFranchiseCollectionIDs(e.ctx, gen.ListVisibleFranchiseCollectionIDsParams{LibraryIds: tc.libs, MaxRatingRank: tc.max})
		if err != nil {
			t.Fatal(err)
		}
		got := len(ids) == 1 && ids[0] == col.ID
		if got != tc.want {
			t.Errorf("%s: visible=%v, want %v", tc.name, got, tc.want)
		}
	}

	rows, err := e.q.ListLibraryFranchiseCollections(e.ctx, gen.ListLibraryFranchiseCollectionsParams{LibraryID: libA})
	if err != nil || len(rows) != 1 {
		t.Fatalf("library tab rows = %+v (%v)", rows, err)
	}
	if rows[0].ItemCount != 1 || rows[0].FirstPosterPath != "movies/Alien/poster.jpg" ||
		rows[0].PosterUrl == nil || *rows[0].PosterUrl != "https://image.tmdb.org/t/p/original/alien.jpg" {
		t.Errorf("library tab row = %+v", rows[0])
	}
	if rows, _ := e.q.ListLibraryFranchiseCollections(e.ctx, gen.ListLibraryFranchiseCollectionsParams{LibraryID: libA, MaxRatingRank: rank(2)}); len(rows) != 0 {
		t.Errorf("PG-13 ceiling must hide the R-only franchise in library A: %+v", rows)
	}

	ref, err := e.q.GetFranchiseForMediaItem(e.ctx, aliens)
	if err != nil || ref.ID != col.ID || len(franchise.DecodeParts(ref.Parts)) != 3 {
		t.Fatalf("franchise for movie = %+v (%v)", ref, err)
	}
}

// Franchise rows are managed; the partial unique index keeps one per TMDB
// collection while leaving other collection types unconstrained.
func TestFranchise_Integration_UniqueTMDBCollection(t *testing.T) {
	e := newEnv(t)
	id1, err := e.q.UpsertFranchiseCollection(e.ctx, gen.UpsertFranchiseCollectionParams{Name: "A", TmdbCollectionID: 1})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := e.q.UpsertFranchiseCollection(e.ctx, gen.UpsertFranchiseCollectionParams{Name: "A renamed", TmdbCollectionID: 1})
	if err != nil || id2 != id1 {
		t.Fatalf("upsert must converge on one row: %s vs %s (%v)", id1, id2, err)
	}
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO collections (name, type, tmdb_collection_id) VALUES ('dupe', 'franchise', 1)`); err == nil {
		t.Fatal("a second row for the same TMDB collection must be rejected")
	}
	for i := 0; i < 2; i++ {
		if _, err := e.pool.Exec(e.ctx, `INSERT INTO collections (name, type) VALUES ('Action', 'auto_genre')`); err != nil {
			t.Fatalf("NULL tmdb_collection_id rows must not collide: %v", err)
		}
	}
	got, err := e.q.GetCollection(e.ctx, id1)
	if err != nil || got.Name != "A renamed" || got.TmdbCollectionID == nil || *got.TmdbCollectionID != 1 {
		t.Fatalf("GetCollection = %+v (%v)", got, err)
	}
}

// TMDB unreachable on first sight: the franchise is named from the movie's
// belongs_to_collection via an epoch-dated placeholder snapshot, which the
// stale-refresh queue picks up first.
func TestFranchise_Integration_PlaceholderSnapshot(t *testing.T) {
	e := newEnv(t)
	delete(e.tmdb.collections, 8091) // GetCollection → ErrNotFound
	lib := e.library(t, "films")
	ref := &metadata.CollectionRef{TMDBID: 8091, Name: "Alien Collection", PosterURL: "https://image.tmdb.org/t/p/original/ph.jpg"}
	for tm, title := range map[int]string{348: "Alien", 679: "Aliens"} {
		id := e.movie(t, lib, title, tm, "1980-01-01", "R")
		if err := e.svc.RecordMovie(e.ctx, id, tm, ref); err != nil {
			t.Fatal(err)
		}
	}
	col, ok := e.franchiseRow(t, 8091)
	if !ok || col.Name != "Alien Collection" {
		t.Fatalf("franchise must be named from the ref: ok=%v %+v", ok, col)
	}
	snap, err := e.q.GetTMDBCollectionSnapshot(e.ctx, 8091)
	if err != nil {
		t.Fatal(err)
	}
	if snap.FetchedAt.Time.Unix() != 0 || snap.PosterUrl == nil || *snap.PosterUrl != ref.PosterURL || string(snap.Parts) != "[]" {
		t.Fatalf("placeholder = %+v parts=%s", snap, snap.Parts)
	}
	stale, err := e.q.ListStaleFranchiseSnapshots(e.ctx, gen.ListStaleFranchiseSnapshotsParams{
		OlderThan: pgtype.Timestamptz{Time: snap.FetchedAt.Time.AddDate(1, 0, 0), Valid: true}, Lim: 10,
	})
	if err != nil || len(stale) != 1 || stale[0] != 8091 {
		t.Fatalf("placeholder must be queued for refresh: %v %v", stale, err)
	}
}
