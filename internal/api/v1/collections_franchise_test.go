package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── fakes ────────────────────────────────────────────────────────────────────

// frCollectionDB is a minimal CollectionDB: collections by id plus a listing.
type frCollectionDB struct {
	cols    map[uuid.UUID]gen.Collection
	listed  []gen.Collection
	mutated bool
}

func (f *frCollectionDB) ListCollections(context.Context, pgtype.UUID) ([]gen.Collection, error) {
	return f.listed, nil
}
func (f *frCollectionDB) GetCollection(_ context.Context, id uuid.UUID) (gen.Collection, error) {
	c, ok := f.cols[id]
	if !ok {
		return gen.Collection{}, pgx.ErrNoRows
	}
	return c, nil
}
func (f *frCollectionDB) CreateCollection(context.Context, gen.CreateCollectionParams) (gen.Collection, error) {
	f.mutated = true
	return gen.Collection{}, nil
}
func (f *frCollectionDB) UpdateCollection(context.Context, gen.UpdateCollectionParams) (gen.Collection, error) {
	f.mutated = true
	return gen.Collection{}, nil
}
func (f *frCollectionDB) DeleteCollection(context.Context, uuid.UUID) error {
	f.mutated = true
	return nil
}
func (f *frCollectionDB) ListCollectionItems(context.Context, gen.ListCollectionItemsParams) ([]gen.ListCollectionItemsRow, error) {
	return nil, nil
}
func (f *frCollectionDB) CountCollectionItems(context.Context, gen.CountCollectionItemsParams) (int64, error) {
	return 0, nil
}
func (f *frCollectionDB) AddCollectionItem(context.Context, gen.AddCollectionItemParams) (gen.CollectionItem, error) {
	f.mutated = true
	return gen.CollectionItem{}, nil
}
func (f *frCollectionDB) GetMediaItem(context.Context, uuid.UUID) (gen.GetMediaItemRow, error) {
	return gen.GetMediaItemRow{}, nil
}
func (f *frCollectionDB) RemoveCollectionItem(context.Context, gen.RemoveCollectionItemParams) error {
	f.mutated = true
	return nil
}
func (f *frCollectionDB) ListAutoGenreCollections(context.Context) ([]gen.Collection, error) {
	return nil, nil
}
func (f *frCollectionDB) ListItemsByGenre(context.Context, gen.ListItemsByGenreParams) ([]gen.ListItemsByGenreRow, error) {
	return nil, nil
}
func (f *frCollectionDB) CountItemsByGenre(context.Context, gen.CountItemsByGenreParams) (int64, error) {
	return 0, nil
}
func (f *frCollectionDB) ListDistinctGenres(context.Context, uuid.UUID) ([]string, error) {
	return nil, nil
}

// frItem is a media item as the franchise queries see it.
type frItem struct {
	id     uuid.UUID
	lib    uuid.UUID
	tmdb   int32
	rating string
}

// frFranchiseDB implements CollectionFranchiseDB and ItemFranchiseDB,
// emulating the SQL visibility predicates (library ids + rating rank).
type frFranchiseDB struct {
	snapshots   map[int32]gen.TmdbCollection
	items       []frItem
	visible     []uuid.UUID
	visibleCall *gen.ListVisibleFranchiseCollectionIDsParams
	libRows     []gen.ListLibraryFranchiseCollectionsRow
	libParams   *gen.ListLibraryFranchiseCollectionsParams
	forItem     map[uuid.UUID]gen.GetFranchiseForMediaItemRow
	count       int64
}

func (f *frFranchiseDB) GetTMDBCollectionSnapshot(_ context.Context, id int32) (gen.TmdbCollection, error) {
	s, ok := f.snapshots[id]
	if !ok {
		return gen.TmdbCollection{}, pgx.ErrNoRows
	}
	return s, nil
}
func (f *frFranchiseDB) ListTMDBCollectionPosters(_ context.Context, ids []int32) ([]gen.ListTMDBCollectionPostersRow, error) {
	var out []gen.ListTMDBCollectionPostersRow
	for _, id := range ids {
		if s, ok := f.snapshots[id]; ok {
			out = append(out, gen.ListTMDBCollectionPostersRow{TmdbCollectionID: id, PosterUrl: s.PosterUrl})
		}
	}
	return out, nil
}
func (f *frFranchiseDB) ListVisibleFranchiseCollectionIDs(_ context.Context, arg gen.ListVisibleFranchiseCollectionIDsParams) ([]uuid.UUID, error) {
	f.visibleCall = &arg
	return f.visible, nil
}
func (f *frFranchiseDB) ListLibraryFranchiseCollections(_ context.Context, arg gen.ListLibraryFranchiseCollectionsParams) ([]gen.ListLibraryFranchiseCollectionsRow, error) {
	f.libParams = &arg
	return f.libRows, nil
}
func (f *frFranchiseDB) ListMediaItemsByTMDBIDs(_ context.Context, arg gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error) {
	want := map[int32]bool{}
	for _, id := range arg.TmdbIds {
		want[id] = true
	}
	var out []gen.ListMediaItemsByTMDBIDsRow
	for _, it := range f.items {
		if !want[it.tmdb] {
			continue
		}
		if arg.LibraryIds != nil {
			ok := false
			for _, l := range arg.LibraryIds {
				ok = ok || l == it.lib
			}
			if !ok {
				continue
			}
		}
		if arg.MaxRatingRank != nil && int32(contentrating.Rank(it.rating)) > *arg.MaxRatingRank {
			continue
		}
		tm := it.tmdb
		out = append(out, gen.ListMediaItemsByTMDBIDsRow{ID: it.id, LibraryID: it.lib, TmdbID: &tm})
	}
	return out, nil
}
func (f *frFranchiseDB) GetFranchiseForMediaItem(_ context.Context, id uuid.UUID) (gen.GetFranchiseForMediaItemRow, error) {
	r, ok := f.forItem[id]
	if !ok {
		return gen.GetFranchiseForMediaItemRow{}, pgx.ErrNoRows
	}
	return r, nil
}
func (f *frFranchiseDB) CountCollectionItems(context.Context, gen.CountCollectionItemsParams) (int64, error) {
	return f.count, nil
}

// frAccess grants the listed libraries; admin = everything (nil map).
type frAccess struct{ allowed map[uuid.UUID]struct{} }

func (a frAccess) CanAccessLibrary(_ context.Context, _, lib uuid.UUID, isAdmin bool) (bool, error) {
	if isAdmin || a.allowed == nil {
		return true, nil
	}
	_, ok := a.allowed[lib]
	return ok, nil
}
func (a frAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, isAdmin bool) (map[uuid.UUID]struct{}, error) {
	if isAdmin {
		return nil, nil
	}
	return a.allowed, nil
}

type frReqs struct{ reqs []gen.MediaRequest }

func (r frReqs) FindActiveForUserBatch(context.Context, uuid.UUID, []int) ([]gen.MediaRequest, error) {
	return r.reqs, nil
}

// ── fixture ──────────────────────────────────────────────────────────────────

var (
	frLibA   = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	frLibB   = uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	frAlien  = uuid.MustParse("00000000-0000-0000-0000-000000000348")
	frAliens = uuid.MustParse("00000000-0000-0000-0000-000000000679")
)

func frPartsJSON(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal([]map[string]any{
		{"tmdb_id": 348, "title": "Alien", "year": 1979, "release_date": "1979-05-25", "poster_url": "https://image.tmdb.org/t/p/original/a.jpg"},
		{"tmdb_id": 679, "title": "Aliens", "year": 1986},
		{"tmdb_id": 8077, "title": "Alien³", "year": 1992, "overview": "Ripley crash-lands."},
		{"tmdb_id": 945961, "title": "Alien: Romulus", "year": 2024},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type frFixture struct {
	colDB  *frCollectionDB
	frDB   *frFranchiseDB
	h      *CollectionHandler
	router http.Handler
	col    gen.Collection
}

func newFrFixture(t *testing.T, access frAccess, reqs frReqs) *frFixture {
	t.Helper()
	cid := int32(8091)
	poster := "https://image.tmdb.org/t/p/original/col.jpg"
	col := gen.Collection{ID: uuid.New(), Name: "Alien Collection", Type: "franchise", TmdbCollectionID: &cid}
	colDB := &frCollectionDB{cols: map[uuid.UUID]gen.Collection{col.ID: col}}
	frDB := &frFranchiseDB{
		snapshots: map[int32]gen.TmdbCollection{cid: {TmdbCollectionID: cid, Name: "Alien Collection", PosterUrl: &poster, Parts: frPartsJSON(t)}},
		items: []frItem{
			{id: frAlien, lib: frLibA, tmdb: 348, rating: "R"},
			{id: frAliens, lib: frLibB, tmdb: 679, rating: "PG"},
		},
		visible: []uuid.UUID{col.ID},
	}
	h := NewCollectionHandler(colDB, slog.Default()).WithLibraryAccess(access).WithFranchise(frDB, reqs)
	r := chi.NewRouter()
	r.Get("/collections", h.List)
	r.Get("/collections/{id}", h.Get)
	r.Patch("/collections/{id}", h.Update)
	r.Delete("/collections/{id}", h.Delete)
	r.Post("/collections/{id}/items", h.AddItem)
	r.Delete("/collections/{id}/items/{itemId}", h.RemoveItem)
	r.Get("/libraries/{id}/collections", h.LibraryCollections)
	return &frFixture{colDB: colDB, frDB: frDB, h: h, router: r, col: col}
}

func (f *frFixture) do(t *testing.T, method, path string, claims *auth.Claims, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if claims != nil {
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

type frPart struct {
	TMDBID        int     `json:"tmdb_id"`
	Title         string  `json:"title"`
	ItemID        *string `json:"item_id"`
	RequestStatus *string `json:"request_status"`
}

func frDecodeDetail(t *testing.T, rec *httptest.ResponseRecorder) (map[string]json.RawMessage, []frPart) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var parts []frPart
	if raw, ok := env.Data["parts"]; ok {
		if err := json.Unmarshal(raw, &parts); err != nil {
			t.Fatal(err)
		}
	}
	return env.Data, parts
}

// ── detail ───────────────────────────────────────────────────────────────────

// An uncapped user with a grant on library A only: owned parts resolve to
// items only where visible; everything else is a missing part carrying the
// caller's own request status.
func TestCollectionGet_FranchiseParts_Uncapped(t *testing.T) {
	f := newFrFixture(t,
		frAccess{allowed: map[uuid.UUID]struct{}{frLibA: {}}},
		frReqs{reqs: []gen.MediaRequest{
			{Type: "movie", TmdbID: 8077, Status: "pending"},
			{Type: "show", TmdbID: 945961, Status: "approved"}, // same id, other type: ignored
		}})
	rec := f.do(t, "GET", "/collections/"+f.col.ID.String(), &auth.Claims{UserID: uuid.New()}, nil)
	data, parts := frDecodeDetail(t, rec)

	if len(parts) != 4 {
		t.Fatalf("parts = %+v, want all 4 (uncapped caller sees missing parts)", parts)
	}
	if parts[0].ItemID == nil || *parts[0].ItemID != frAlien.String() {
		t.Errorf("Alien is owned + visible: %+v", parts[0])
	}
	if parts[1].ItemID != nil {
		t.Errorf("Aliens sits in a library without a grant — must read as missing: %+v", parts[1])
	}
	if parts[2].RequestStatus == nil || *parts[2].RequestStatus != "pending" {
		t.Errorf("Alien³ should carry the caller's pending request: %+v", parts[2])
	}
	if parts[3].RequestStatus != nil {
		t.Errorf("a show request must not mark a movie part: %+v", parts[3])
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"item_id":null`)) || !bytes.Contains(rec.Body.Bytes(), []byte(`"request_status":null`)) {
		t.Error("item_id / request_status must be explicit nulls on missing parts")
	}
	if string(data["poster_url"]) != `"https://image.tmdb.org/t/p/original/col.jpg"` {
		t.Errorf("poster_url = %s", data["poster_url"])
	}
	if f.frDB.visibleCall == nil || len(f.frDB.visibleCall.LibraryIds) != 1 {
		t.Errorf("visibility must be checked against the caller's libraries: %+v", f.frDB.visibleCall)
	}
}

// A capped profile sees only owned parts within its ceiling; missing parts
// (unrated on TMDB = most restrictive) are hidden entirely.
func TestCollectionGet_FranchiseParts_CappedHidesMissingAndOverCeiling(t *testing.T) {
	f := newFrFixture(t, frAccess{}, frReqs{})
	claims := &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG-13"}
	_, parts := frDecodeDetail(t, f.do(t, "GET", "/collections/"+f.col.ID.String(), claims, nil))
	if len(parts) != 1 || parts[0].TMDBID != 679 || parts[0].ItemID == nil {
		t.Fatalf("capped caller must see only the PG film it owns: %+v", parts)
	}
}

// Even the most permissive explicit ceiling doesn't reveal missing parts
// unless it admits unrated titles (the "Rx" ceiling ranks like no ceiling).
func TestCollectionGet_FranchiseParts_RCeilingStillHidesMissing(t *testing.T) {
	f := newFrFixture(t, frAccess{}, frReqs{})
	_, parts := frDecodeDetail(t, f.do(t, "GET", "/collections/"+f.col.ID.String(),
		&auth.Claims{UserID: uuid.New(), MaxContentRating: "NC-17"}, nil))
	for _, p := range parts {
		if p.ItemID == nil {
			t.Fatalf("missing part leaked to an NC-17-capped profile: %+v", p)
		}
	}
	if len(parts) != 2 {
		t.Fatalf("both owned films are within NC-17: %+v", parts)
	}
}

func TestCollectionGet_FranchiseInvisibleIs404(t *testing.T) {
	f := newFrFixture(t, frAccess{allowed: map[uuid.UUID]struct{}{}}, frReqs{})
	f.frDB.visible = nil
	rec := f.do(t, "GET", "/collections/"+f.col.ID.String(), &auth.Claims{UserID: uuid.New()}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a franchise with no visible member", rec.Code)
	}
}

func TestCollectionGet_FranchiseWithoutSnapshot(t *testing.T) {
	f := newFrFixture(t, frAccess{}, frReqs{})
	f.frDB.snapshots = map[int32]gen.TmdbCollection{}
	data, parts := frDecodeDetail(t, f.do(t, "GET", "/collections/"+f.col.ID.String(), &auth.Claims{UserID: uuid.New(), IsAdmin: true}, nil))
	if len(parts) != 0 {
		t.Fatalf("no snapshot → no parts, got %+v", parts)
	}
	if string(data["type"]) != `"franchise"` {
		t.Errorf("type = %s", data["type"])
	}
}

// ── listing ──────────────────────────────────────────────────────────────────

func TestCollectionList_FiltersInvisibleFranchisesAndAddsPosters(t *testing.T) {
	f := newFrFixture(t, frAccess{allowed: map[uuid.UUID]struct{}{frLibA: {}}}, frReqs{})
	hiddenCID := int32(1575)
	hidden := gen.Collection{ID: uuid.New(), Name: "Saw Collection", Type: "franchise", TmdbCollectionID: &hiddenCID}
	genre := gen.Collection{ID: uuid.New(), Name: "Action", Type: "auto_genre"}
	f.colDB.listed = []gen.Collection{genre, f.col, hidden}

	rec := f.do(t, "GET", "/collections?include=franchise", &auth.Claims{UserID: uuid.New()}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var env struct {
		Data []collectionResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 2 || env.Data[0].Name != "Action" || env.Data[1].Name != "Alien Collection" {
		t.Fatalf("listing = %+v (the Saw collection has no visible member)", env.Data)
	}
	if env.Data[1].PosterURL == nil || *env.Data[1].PosterURL == "" || env.Data[1].TMDBCollectionID == nil {
		t.Errorf("franchise card needs TMDB poster + id: %+v", env.Data[1])
	}
}

func TestCollectionList_UnrestrictedSkipsVisibilityQuery(t *testing.T) {
	f := newFrFixture(t, frAccess{}, frReqs{})
	f.colDB.listed = []gen.Collection{f.col}
	rec := f.do(t, "GET", "/collections?include=franchise", &auth.Claims{UserID: uuid.New(), IsAdmin: true}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if f.frDB.visibleCall != nil {
		t.Fatal("an unrestricted caller must not pay for the visibility query")
	}
}

func TestCollectionList_FranchiseNotWiredFailsClosed(t *testing.T) {
	colDB := &frCollectionDB{}
	cid := int32(1)
	colDB.listed = []gen.Collection{{ID: uuid.New(), Name: "X Collection", Type: "franchise", TmdbCollectionID: &cid}}
	h := NewCollectionHandler(colDB, slog.Default())
	req := httptest.NewRequest("GET", "/collections?include=franchise", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New()}))
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"data":[]`)) {
		t.Fatalf("franchise rows must be dropped when visibility can't be checked: %s", rec.Body.String())
	}
}

// TestCollectionList_FranchisesOptIn: the Android TV / phone home screens
// build a row per collection, so franchises only appear when asked for.
func TestCollectionList_FranchisesOptIn(t *testing.T) {
	f := newFrFixture(t, frAccess{allowed: map[uuid.UUID]struct{}{frLibA: {}}}, frReqs{})
	genre := gen.Collection{ID: uuid.New(), Name: "Action", Type: "auto_genre"}
	f.colDB.listed = []gen.Collection{genre, f.col}
	names := func(path string) []string {
		t.Helper()
		rec := f.do(t, "GET", path, &auth.Claims{UserID: uuid.New()}, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, rec.Code)
		}
		var env struct {
			Data []collectionResponse `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(env.Data))
		for _, c := range env.Data {
			out = append(out, c.Name)
		}
		return out
	}

	if got := names("/collections"); len(got) != 1 || got[0] != "Action" {
		t.Fatalf("default listing = %v, want only the non-franchise row", got)
	}
	if f.frDB.visibleCall != nil {
		t.Error("the default listing must not run the franchise visibility query")
	}
	if got := names("/collections?include=other"); len(got) != 1 {
		t.Fatalf("unrelated include = %v, want franchises still excluded", got)
	}
	for _, path := range []string{
		"/collections?include=franchise",
		"/collections?include=other,%20franchise",
		"/collections?include=other&include=franchise",
	} {
		if got := names(path); len(got) != 2 || got[1] != "Alien Collection" {
			t.Errorf("%s = %v, want the franchise included", path, got)
		}
	}
}

// ── mutations ────────────────────────────────────────────────────────────────

func TestCollectionMutations_FranchiseIsManaged(t *testing.T) {
	f := newFrFixture(t, frAccess{}, frReqs{})
	admin := &auth.Claims{UserID: uuid.New(), IsAdmin: true}
	base := "/collections/" + f.col.ID.String()
	for _, tc := range []struct {
		method, path string
		body         string
	}{
		{"PATCH", base, `{"name":"Renamed"}`},
		{"DELETE", base, ``},
		{"POST", base + "/items", `{"media_item_id":"` + uuid.NewString() + `"}`},
		{"DELETE", base + "/items/" + uuid.NewString(), ``},
	} {
		rec := f.do(t, tc.method, tc.path, admin, []byte(tc.body))
		if rec.Code != http.StatusConflict || !bytes.Contains(rec.Body.Bytes(), []byte("MANAGED_COLLECTION")) {
			t.Errorf("%s %s: status %d body %s, want 409 MANAGED_COLLECTION", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	if f.colDB.mutated {
		t.Fatal("no write may reach the DB for a franchise collection")
	}
}

// ── per-library listing ──────────────────────────────────────────────────────

func TestLibraryCollections(t *testing.T) {
	f := newFrFixture(t, frAccess{allowed: map[uuid.UUID]struct{}{frLibA: {}}}, frReqs{})
	cid := int32(8091)
	poster := "https://image.tmdb.org/t/p/original/col.jpg"
	f.frDB.libRows = []gen.ListLibraryFranchiseCollectionsRow{
		{ID: uuid.New(), Name: "Toy Story Collection", Type: "franchise", ItemCount: 3, FirstPosterPath: "movies/ts/poster.jpg"},
		{ID: f.col.ID, Name: "Alien Collection", Type: "franchise", TmdbCollectionID: &cid, PosterUrl: &poster, ItemCount: 2},
	}
	claims := &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG-13"}

	if rec := f.do(t, "GET", "/libraries/"+frLibB.String()+"/collections", claims, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no grant on the library → 404, got %d", rec.Code)
	}
	if rec := f.do(t, "GET", "/libraries/not-a-uuid/collections", claims, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id → 400, got %d", rec.Code)
	}

	rec := f.do(t, "GET", "/libraries/"+frLibA.String()+"/collections", claims, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []libraryCollectionResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 2 || env.Data[0].Name != "Alien Collection" {
		t.Fatalf("rows = %+v (sorted by name)", env.Data)
	}
	if env.Data[0].PosterURL == nil || env.Data[0].PosterPath != nil || env.Data[0].ItemCount != 2 {
		t.Errorf("alien row = %+v", env.Data[0])
	}
	if env.Data[1].PosterPath == nil || *env.Data[1].PosterPath != "movies/ts/poster.jpg" {
		t.Errorf("fallback cover missing: %+v", env.Data[1])
	}
	p := f.frDB.libParams
	if p == nil || p.LibraryID != frLibA || p.MaxRatingRank == nil || *p.MaxRatingRank != int32(contentrating.Rank("PG-13")) {
		t.Errorf("query params = %+v (library + caller's ceiling)", p)
	}
}

func TestLibraryCollections_RequiresClaims(t *testing.T) {
	f := newFrFixture(t, frAccess{}, frReqs{})
	if rec := f.do(t, "GET", "/libraries/"+frLibA.String()+"/collections", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

// ── movie detail reference ───────────────────────────────────────────────────

func TestItemFranchiseRef(t *testing.T) {
	colID := uuid.New()
	cid := int32(8091)
	frDB := &frFranchiseDB{
		items: []frItem{
			{id: frAlien, lib: frLibA, tmdb: 348, rating: "R"},
			{id: frAliens, lib: frLibA, tmdb: 679, rating: "PG"},
		},
		forItem: map[uuid.UUID]gen.GetFranchiseForMediaItemRow{
			frAliens: {ID: colID, Name: "Alien Collection", TmdbCollectionID: &cid, Parts: frPartsJSON(t)},
		},
	}
	h := (&ItemHandler{logger: slog.Default()}).WithFranchise(frDB)
	h.access = frAccess{}

	ref := h.franchiseRef(context.Background(), &auth.Claims{UserID: uuid.New()}, frAliens)
	if ref == nil || ref.ID != colID.String() || ref.Name != "Alien Collection" || ref.PartCount != 4 || ref.OwnedCount != 2 {
		t.Fatalf("uncapped ref = %+v, want 4 parts / 2 owned", ref)
	}

	ref = h.franchiseRef(context.Background(), &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG"}, frAliens)
	if ref == nil || ref.PartCount != 1 || ref.OwnedCount != 1 {
		t.Fatalf("PG-capped ref = %+v, want 1/1 (R film and missing parts hidden)", ref)
	}

	if ref := h.franchiseRef(context.Background(), &auth.Claims{UserID: uuid.New()}, uuid.New()); ref != nil {
		t.Fatalf("a movie outside any franchise has no ref: %+v", ref)
	}

	// No snapshot yet: fall back to the visible member count.
	frDB.forItem[frAliens] = gen.GetFranchiseForMediaItemRow{ID: colID, Name: "Alien Collection"}
	frDB.count = 2
	ref = h.franchiseRef(context.Background(), &auth.Claims{UserID: uuid.New()}, frAliens)
	if ref == nil || ref.PartCount != 2 || ref.OwnedCount != 2 {
		t.Fatalf("no-snapshot ref = %+v", ref)
	}
}

type frFailingAccess struct{ frAccess }

func (frFailingAccess) AllowedLibraryIDs(context.Context, uuid.UUID, bool) (map[uuid.UUID]struct{}, error) {
	return nil, errors.New("db down")
}

func TestItemFranchiseRef_ACLFailureOmits(t *testing.T) {
	cid := int32(8091)
	frDB := &frFranchiseDB{forItem: map[uuid.UUID]gen.GetFranchiseForMediaItemRow{
		frAlien: {ID: uuid.New(), Name: "Alien Collection", TmdbCollectionID: &cid, Parts: frPartsJSON(t)},
	}}
	h := (&ItemHandler{logger: slog.Default()}).WithFranchise(frDB)
	h.access = frFailingAccess{}
	if ref := h.franchiseRef(context.Background(), &auth.Claims{UserID: uuid.New()}, frAlien); ref != nil {
		t.Fatalf("ACL failure must omit the ref (fail closed), got %+v", ref)
	}
}
