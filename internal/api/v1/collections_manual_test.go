package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/collections"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// mcStore is an in-memory stand-in for the collections queries, with real
// membership. ListVisibleManualCollections / ListLibraryManualCollections
// return what the test sets: their SQL (visibility, covers) is covered by the
// Postgres integration test; here the handler's use of them is.
type mcStore struct {
	cols    map[uuid.UUID]gen.Collection
	items   map[uuid.UUID]gen.GetMediaItemRow
	members map[uuid.UUID][]uuid.UUID

	visible     []gen.ListVisibleManualCollectionsRow
	visibleArgs []gen.ListVisibleManualCollectionsParams
	library     []gen.ListLibraryManualCollectionsRow
	itemsArgs   []gen.ListCollectionItemsParams
	reorders    []gen.ReorderPlaylistItemsParams
	created     []gen.CreateCollectionParams
	posters     map[uuid.UUID]gen.GetCollectionPosterRow
}

func newMCStore() *mcStore {
	return &mcStore{
		cols:    map[uuid.UUID]gen.Collection{},
		items:   map[uuid.UUID]gen.GetMediaItemRow{},
		members: map[uuid.UUID][]uuid.UUID{},
		posters: map[uuid.UUID]gen.GetCollectionPosterRow{},
	}
}

func (s *mcStore) ListCollections(context.Context, pgtype.UUID) ([]gen.Collection, error) {
	out := make([]gen.Collection, 0, len(s.cols))
	for _, c := range s.cols {
		out = append(out, c)
	}
	return out, nil
}
func (s *mcStore) GetCollection(_ context.Context, id uuid.UUID) (gen.Collection, error) {
	c, ok := s.cols[id]
	if !ok {
		return gen.Collection{}, pgx.ErrNoRows
	}
	return c, nil
}
func (s *mcStore) CreateCollection(_ context.Context, p gen.CreateCollectionParams) (gen.Collection, error) {
	s.created = append(s.created, p)
	c := gen.Collection{ID: uuid.New(), UserID: p.UserID, Name: p.Name, Description: p.Description, Type: p.Type, ItemOrder: "custom"}
	s.cols[c.ID] = c
	return c, nil
}
func (s *mcStore) UpdateCollection(_ context.Context, p gen.UpdateCollectionParams) (gen.Collection, error) {
	c, ok := s.cols[p.ID]
	if !ok {
		return gen.Collection{}, pgx.ErrNoRows
	}
	c.Name, c.Description = p.Name, p.Description
	s.cols[p.ID] = c
	return c, nil
}
func (s *mcStore) DeleteCollection(_ context.Context, id uuid.UUID) error {
	delete(s.cols, id)
	return nil
}
func (s *mcStore) ListCollectionItems(_ context.Context, p gen.ListCollectionItemsParams) ([]gen.ListCollectionItemsRow, error) {
	s.itemsArgs = append(s.itemsArgs, p)
	var out []gen.ListCollectionItemsRow
	for i, id := range s.members[p.CollectionID] {
		it := s.items[id]
		out = append(out, gen.ListCollectionItemsRow{ID: id, LibraryID: it.LibraryID, Type: it.Type, Title: it.Title, Position: int32(i)})
	}
	return out, nil
}
func (s *mcStore) CountCollectionItems(_ context.Context, p gen.CountCollectionItemsParams) (int64, error) {
	return int64(len(s.members[p.CollectionID])), nil
}
func (s *mcStore) AddCollectionItem(_ context.Context, p gen.AddCollectionItemParams) (gen.CollectionItem, error) {
	for _, id := range s.members[p.CollectionID] {
		if id == p.MediaItemID {
			return gen.CollectionItem{}, pgx.ErrNoRows // ON CONFLICT DO NOTHING
		}
	}
	s.members[p.CollectionID] = append(s.members[p.CollectionID], p.MediaItemID)
	return gen.CollectionItem{CollectionID: p.CollectionID, MediaItemID: p.MediaItemID}, nil
}
func (s *mcStore) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	it, ok := s.items[id]
	if !ok {
		return gen.GetMediaItemRow{}, pgx.ErrNoRows
	}
	return it, nil
}
func (s *mcStore) RemoveCollectionItem(_ context.Context, p gen.RemoveCollectionItemParams) error {
	kept := s.members[p.CollectionID][:0]
	for _, id := range s.members[p.CollectionID] {
		if id != p.MediaItemID {
			kept = append(kept, id)
		}
	}
	s.members[p.CollectionID] = kept
	return nil
}
func (s *mcStore) ListAutoGenreCollections(context.Context) ([]gen.Collection, error) {
	return nil, nil
}
func (s *mcStore) ListItemsByGenre(context.Context, gen.ListItemsByGenreParams) ([]gen.ListItemsByGenreRow, error) {
	return nil, nil
}
func (s *mcStore) CountItemsByGenre(context.Context, gen.CountItemsByGenreParams) (int64, error) {
	return 0, nil
}
func (s *mcStore) ListDistinctGenres(context.Context, uuid.UUID) ([]string, error) { return nil, nil }

// CollectionManualDB.
func (s *mcStore) CreateManualCollection(_ context.Context, p gen.CreateManualCollectionParams) (gen.Collection, error) {
	c := gen.Collection{ID: uuid.New(), Name: p.Name, Description: p.Description, Type: "manual", ItemOrder: p.ItemOrder}
	s.cols[c.ID] = c
	return c, nil
}
func (s *mcStore) SetCollectionItemOrder(_ context.Context, p gen.SetCollectionItemOrderParams) error {
	c := s.cols[p.ID]
	c.ItemOrder = p.ItemOrder
	s.cols[p.ID] = c
	return nil
}
func (s *mcStore) SetCollectionPosterItem(_ context.Context, p gen.SetCollectionPosterItemParams) error {
	c := s.cols[p.ID]
	c.PosterItemID = p.PosterItemID
	s.cols[p.ID] = c
	return nil
}
func (s *mcStore) ListVisibleManualCollections(_ context.Context, p gen.ListVisibleManualCollectionsParams) ([]gen.ListVisibleManualCollectionsRow, error) {
	s.visibleArgs = append(s.visibleArgs, p)
	return s.visible, nil
}
func (s *mcStore) ListLibraryManualCollections(context.Context, gen.ListLibraryManualCollectionsParams) ([]gen.ListLibraryManualCollectionsRow, error) {
	return s.library, nil
}
func (s *mcStore) ListManualCollectionsForItem(_ context.Context, itemID uuid.UUID) ([]gen.ListManualCollectionsForItemRow, error) {
	var out []gen.ListManualCollectionsForItemRow
	for cid, ids := range s.members {
		if s.cols[cid].Type != "manual" {
			continue
		}
		for _, id := range ids {
			if id == itemID {
				out = append(out, gen.ListManualCollectionsForItemRow{ID: cid, Name: s.cols[cid].Name})
			}
		}
	}
	return out, nil
}
func (s *mcStore) ReorderPlaylistItems(_ context.Context, p gen.ReorderPlaylistItemsParams) error {
	s.reorders = append(s.reorders, p)
	s.members[p.CollectionID] = append([]uuid.UUID(nil), p.ItemIds...)
	return nil
}

// Phase 2.
func (s *mcStore) SetCollectionPromoted(_ context.Context, p gen.SetCollectionPromotedParams) error {
	c := s.cols[p.ID]
	c.Promoted = p.Promoted
	s.cols[p.ID] = c
	return nil
}
func (s *mcStore) SetCollectionRules(_ context.Context, p gen.SetCollectionRulesParams) error {
	c := s.cols[p.ID]
	c.Rules = p.Rules
	s.cols[p.ID] = c
	return nil
}
func (s *mcStore) UpsertCollectionPoster(_ context.Context, p gen.UpsertCollectionPosterParams) error {
	s.posters[p.CollectionID] = gen.GetCollectionPosterRow{ContentType: p.ContentType, Data: p.Data,
		UpdatedAt: pgtype.Timestamptz{Time: time.UnixMilli(1700000000000), Valid: true}}
	return nil
}
func (s *mcStore) GetCollectionPoster(_ context.Context, id uuid.UUID) (gen.GetCollectionPosterRow, error) {
	p, ok := s.posters[id]
	if !ok {
		return p, pgx.ErrNoRows
	}
	return p, nil
}
func (s *mcStore) DeleteCollectionPoster(_ context.Context, id uuid.UUID) error {
	delete(s.posters, id)
	return nil
}
func (s *mcStore) ListCollectionPosterVersions(_ context.Context, ids []uuid.UUID) ([]gen.ListCollectionPosterVersionsRow, error) {
	var out []gen.ListCollectionPosterVersionsRow
	for _, id := range ids {
		if p, ok := s.posters[id]; ok {
			out = append(out, gen.ListCollectionPosterVersionsRow{CollectionID: id, UpdatedAt: p.UpdatedAt})
		}
	}
	return out, nil
}

// mcSmart stands in for collections.Smart: a refresh makes the members
// whatever the test put in matches.
type mcSmart struct {
	s         *mcStore
	matches   []uuid.UUID
	refreshed []collections.Rules
}

func (m *mcSmart) Refresh(_ context.Context, id uuid.UUID, rules collections.Rules) (int, error) {
	m.refreshed = append(m.refreshed, rules)
	m.s.members[id] = append([]uuid.UUID(nil), m.matches...)
	return len(m.matches), nil
}

var (
	mcLibMovies = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	mcLibKids   = uuid.MustParse("00000000-0000-0000-0000-0000000000a2")
)

type mcFixture struct {
	s      *mcStore
	router http.Handler
	admin  *auth.Claims
	user   *auth.Claims
	movie  uuid.UUID // movies library, R
	show   uuid.UUID // kids library, PG
	ep     uuid.UUID // an episode (not a collection member type)
	secret uuid.UUID // a library the user has no grant on
	smart  *mcSmart
}

func newMCFixture(t *testing.T, withManual bool) *mcFixture {
	t.Helper()
	s := newMCStore()
	rating := func(r string) *string { return &r }
	f := &mcFixture{
		s:      s,
		admin:  &auth.Claims{UserID: uuid.New(), IsAdmin: true},
		user:   &auth.Claims{UserID: uuid.New()},
		movie:  uuid.New(),
		show:   uuid.New(),
		ep:     uuid.New(),
		secret: uuid.New(),
	}
	s.items[f.movie] = gen.GetMediaItemRow{ID: f.movie, LibraryID: mcLibMovies, Type: "movie", Title: "Halloween", ContentRating: rating("R")}
	s.items[f.show] = gen.GetMediaItemRow{ID: f.show, LibraryID: mcLibKids, Type: "show", Title: "Bluey", ContentRating: rating("TV-Y")}
	s.items[f.ep] = gen.GetMediaItemRow{ID: f.ep, LibraryID: mcLibKids, Type: "episode", Title: "Keepy Uppy", ContentRating: rating("TV-Y")}
	s.items[f.secret] = gen.GetMediaItemRow{ID: f.secret, LibraryID: uuid.New(), Type: "movie", Title: "Private", ContentRating: rating("PG")}

	access := frAccess{allowed: map[uuid.UUID]struct{}{mcLibMovies: {}, mcLibKids: {}}}
	h := NewCollectionHandler(s, slog.Default()).WithLibraryAccess(access)
	f.smart = &mcSmart{s: s}
	if withManual {
		h = h.WithManual(s, f.smart)
	}
	r := chi.NewRouter()
	r.Get("/collections", h.List)
	r.Post("/collections", h.Create)
	r.Get("/collections/{id}", h.Get)
	r.Patch("/collections/{id}", h.Update)
	r.Delete("/collections/{id}", h.Delete)
	r.Get("/collections/{id}/items", h.Items)
	r.Post("/collections/{id}/items", h.AddItem)
	r.Put("/collections/{id}/items/order", h.Reorder)
	r.Get("/libraries/{id}/collections", h.LibraryCollections)
	r.Delete("/collections/{id}/items/{itemId}", h.RemoveItem)
	r.Post("/collections/{id}/poster", h.UploadPoster)
	r.Delete("/collections/{id}/poster", h.DeletePoster)
	r.Get("/collections/{id}/poster", h.Poster)
	f.router = r
	return f
}

func (f *mcFixture) do(t *testing.T, method, path string, claims *auth.Claims, body any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if claims != nil {
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// manual adds a manual collection with the given members directly.
func (f *mcFixture) manual(name string, members ...uuid.UUID) gen.Collection {
	c := gen.Collection{ID: uuid.New(), Name: name, Type: "manual", ItemOrder: "custom"}
	f.s.cols[c.ID] = c
	f.s.members[c.ID] = members
	return c
}

func mcDecode(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if err := json.Unmarshal(env.Data, into); err != nil {
		t.Fatalf("decode data: %v (%s)", err, env.Data)
	}
}

func TestManualCollection_Create(t *testing.T) {
	f := newMCFixture(t, true)
	if rec := f.do(t, "POST", "/collections", f.user, map[string]any{"name": "Spooky", "type": "manual"}); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin create = %d, want 403", rec.Code)
	}
	if rec := f.do(t, "POST", "/collections", f.admin, map[string]any{"name": "Spooky", "type": "manual", "item_order": "random"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad item_order = %d, want 400", rec.Code)
	}
	if rec := f.do(t, "POST", "/collections", f.admin, map[string]any{"name": "Spooky", "type": "franchise"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("other type = %d, want 400", rec.Code)
	}
	if rec := f.do(t, "POST", "/collections", f.admin, map[string]any{"name": "   ", "type": "manual"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank name = %d, want 400", rec.Code)
	}
	rec := f.do(t, "POST", "/collections", f.admin, map[string]any{"name": "  Spooky  ", "type": "manual", "item_order": "release"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin create = %d (%s)", rec.Code, rec.Body.String())
	}
	var got collectionResponse
	mcDecode(t, rec, &got)
	if got.Type != "manual" || got.Name != "Spooky" || got.ItemOrder != "release" || got.ItemCount == nil || *got.ItemCount != 0 {
		t.Fatalf("created %+v", got)
	}
	// Without a type: the caller's own playlist, as before.
	if rec := f.do(t, "POST", "/collections", f.user, map[string]any{"name": "Mine"}); rec.Code != http.StatusCreated {
		t.Fatalf("playlist create = %d", rec.Code)
	}
	last := f.s.created[len(f.s.created)-1]
	if last.Type != "playlist" || uuid.UUID(last.UserID.Bytes) != f.user.UserID {
		t.Fatalf("untyped create made %+v", last)
	}
}

func TestManualCollection_CreateNotWired(t *testing.T) {
	f := newMCFixture(t, false)
	if rec := f.do(t, "POST", "/collections", f.admin, map[string]any{"name": "X", "type": "manual"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("create without WithManual = %d, want 400", rec.Code)
	}
}

func TestManualCollection_ListVisibility(t *testing.T) {
	f := newMCFixture(t, true)
	visible := f.manual("Saturday cartoons", f.show)
	hidden := f.manual("Grown-up films", f.movie)
	empty := f.manual("Empty")
	poster := "/artwork/bluey.jpg"
	f.s.visible = []gen.ListVisibleManualCollectionsRow{{ID: visible.ID, ItemCount: 1, CoverPosterPath: poster}}

	// A kid profile: only the collection with a member it can see, with that
	// member's poster and the visible count.
	kid := &auth.Claims{UserID: uuid.New(), MaxContentRating: "TV-Y7"}
	rec := f.do(t, "GET", "/collections", kid, nil)
	var list []collectionResponse
	mcDecode(t, rec, &list)
	if len(list) != 1 || list[0].ID != visible.ID.String() {
		t.Fatalf("kid sees %+v, want only %q", list, visible.Name)
	}
	if list[0].PosterPath == nil || *list[0].PosterPath != poster || list[0].ItemCount == nil || *list[0].ItemCount != 1 {
		t.Fatalf("cover/count = %v / %v", list[0].PosterPath, list[0].ItemCount)
	}
	args := f.s.visibleArgs[len(f.s.visibleArgs)-1]
	if args.MaxRatingRank == nil || len(args.LibraryIds) != 2 {
		t.Fatalf("visibility query args %+v: want the ceiling and the two granted libraries", args)
	}

	// An admin sees every manual collection, empty ones included.
	rec = f.do(t, "GET", "/collections", f.admin, nil)
	mcDecode(t, rec, &list)
	seen := map[string]bool{}
	for _, c := range list {
		seen[c.ID] = true
	}
	for _, c := range []gen.Collection{visible, hidden, empty} {
		if !seen[c.ID.String()] {
			t.Errorf("admin list misses %q", c.Name)
		}
	}
}

func TestManualCollection_ListNotWiredFailsClosed(t *testing.T) {
	f := newMCFixture(t, false)
	f.manual("Saturday cartoons", f.show)
	var list []collectionResponse
	mcDecode(t, f.do(t, "GET", "/collections", f.user, nil), &list)
	if len(list) != 0 {
		t.Fatalf("unwired handler listed %d manual collections", len(list))
	}
}

func TestManualCollection_Get(t *testing.T) {
	f := newMCFixture(t, true)
	visible := f.manual("Saturday cartoons", f.show)
	hidden := f.manual("Grown-up films", f.movie)
	f.s.visible = []gen.ListVisibleManualCollectionsRow{{ID: visible.ID, ItemCount: 1}}
	if rec := f.do(t, "GET", "/collections/"+hidden.ID.String(), f.user, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("invisible manual collection = %d, want 404", rec.Code)
	}
	if rec := f.do(t, "GET", "/collections/"+visible.ID.String(), f.user, nil); rec.Code != http.StatusOK {
		t.Fatalf("visible manual collection = %d", rec.Code)
	}
	if rec := f.do(t, "GET", "/collections/"+hidden.ID.String(), f.admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin get = %d", rec.Code)
	}
}

func TestManualCollection_Update(t *testing.T) {
	f := newMCFixture(t, true)
	desc := "Scary ones"
	c := f.manual("Spooky", f.movie, f.show)
	c.Description = &desc
	f.s.cols[c.ID] = c
	path := "/collections/" + c.ID.String()

	if rec := f.do(t, "PATCH", path, f.user, map[string]any{"name": "Mine now"}); rec.Code != http.StatusNotFound {
		t.Fatalf("non-admin patch = %d, want 404", rec.Code)
	}
	if rec := f.do(t, "PATCH", path, f.admin, map[string]any{"item_order": "sideways"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad item_order = %d, want 400", rec.Code)
	}
	if rec := f.do(t, "PATCH", path, f.admin, map[string]any{"poster_item_id": f.secret.String()}); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-member poster = %d, want 400", rec.Code)
	}
	if rec := f.do(t, "PATCH", path, f.admin, map[string]any{"poster_item_id": "nope"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed poster = %d, want 400", rec.Code)
	}

	// Only the order changes: name and description stay.
	rec := f.do(t, "PATCH", path, f.admin, map[string]any{"item_order": "title", "poster_item_id": f.show.String()})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d (%s)", rec.Code, rec.Body.String())
	}
	got := f.s.cols[c.ID]
	if got.Name != "Spooky" || got.Description == nil || *got.Description != desc {
		t.Fatalf("order-only patch changed name/description: %q / %v", got.Name, got.Description)
	}
	if got.ItemOrder != "title" || !got.PosterItemID.Valid || uuid.UUID(got.PosterItemID.Bytes) != f.show {
		t.Fatalf("settings not saved: %+v", got)
	}
	var resp collectionResponse
	mcDecode(t, rec, &resp)
	if resp.ItemOrder != "title" || resp.PosterItemID == nil || *resp.PosterItemID != f.show.String() {
		t.Fatalf("response %+v", resp)
	}

	// An empty poster_item_id goes back to the first member's poster.
	if rec := f.do(t, "PATCH", path, f.admin, map[string]any{"poster_item_id": ""}); rec.Code != http.StatusOK {
		t.Fatalf("clear poster = %d", rec.Code)
	}
	if f.s.cols[c.ID].PosterItemID.Valid {
		t.Fatal("poster_item_id not cleared")
	}

	// Rename keeps the settings.
	if rec := f.do(t, "PATCH", path, f.admin, map[string]any{"name": "Spookier"}); rec.Code != http.StatusOK {
		t.Fatalf("rename = %d", rec.Code)
	}
	if got := f.s.cols[c.ID]; got.Name != "Spookier" || got.ItemOrder != "title" {
		t.Fatalf("after rename %+v", got)
	}

	// The manual settings don't apply to a playlist.
	pl := gen.Collection{ID: uuid.New(), Name: "Mine", Type: "playlist",
		UserID: pgtype.UUID{Bytes: [16]byte(f.user.UserID), Valid: true}}
	f.s.cols[pl.ID] = pl
	if rec := f.do(t, "PATCH", "/collections/"+pl.ID.String(), f.user, map[string]any{"item_order": "title"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("item_order on a playlist = %d, want 400", rec.Code)
	}
}

func TestManualCollection_AddItems(t *testing.T) {
	f := newMCFixture(t, true)
	c := f.manual("Spooky")
	path := "/collections/" + c.ID.String() + "/items"
	members := func() []uuid.UUID { return f.s.members[c.ID] }

	if rec := f.do(t, "POST", path, f.user, map[string]any{"media_item_id": f.movie.String()}); rec.Code != http.StatusNotFound {
		t.Fatalf("non-admin add = %d, want 404", rec.Code)
	}
	// One bad item refuses the whole batch.
	if rec := f.do(t, "POST", path, f.admin, map[string]any{"media_item_ids": []string{f.movie.String(), f.ep.String()}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("episode in batch = %d, want 400", rec.Code)
	}
	if rec := f.do(t, "POST", path, f.admin, map[string]any{"media_item_ids": []string{f.movie.String(), uuid.New().String()}}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown item in batch = %d, want 404", rec.Code)
	}
	if rec := f.do(t, "POST", path, f.admin, map[string]any{"media_item_ids": []string{f.movie.String(), f.movie.String()}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate ids = %d, want 400", rec.Code)
	}
	if rec := f.do(t, "POST", path, f.admin, map[string]any{"media_item_ids": []string{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty batch = %d, want 400", rec.Code)
	}
	if n := len(members()); n != 0 {
		t.Fatalf("refused batches added %d items", n)
	}

	if rec := f.do(t, "POST", path, f.admin, map[string]any{"media_item_ids": []string{f.movie.String(), f.show.String()}}); rec.Code != http.StatusNoContent {
		t.Fatalf("batch add = %d (%s)", rec.Code, rec.Body.String())
	}
	if m := members(); len(m) != 2 || m[0] != f.movie || m[1] != f.show {
		t.Fatalf("members %v", m)
	}
	// Re-adding is a no-op; a single media_item_id still works.
	if rec := f.do(t, "POST", path, f.admin, map[string]any{"media_item_id": f.movie.String()}); rec.Code != http.StatusNoContent {
		t.Fatalf("re-add = %d", rec.Code)
	}
	if n := len(members()); n != 2 {
		t.Fatalf("re-add changed membership to %d", n)
	}

	// Too large a batch.
	many := make([]string, maxBulkCollectionAdd+1)
	for i := range many {
		many[i] = uuid.New().String()
	}
	if rec := f.do(t, "POST", path, f.admin, map[string]any{"media_item_ids": many}); rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized batch = %d, want 400", rec.Code)
	}
}

// The batch path checks every item's library grant, refusing the whole batch
// when one fails. Admins pass every grant, so the check is driven through a
// (hand-made) manual collection a non-admin owns, which lets that user past
// the mutate check into the batch path.
func TestManualCollection_AddItemsRespectsLibraryGrant(t *testing.T) {
	f := newMCFixture(t, true)
	c := f.manual("Spooky")
	nonAdminOwned := c
	nonAdminOwned.UserID = pgtype.UUID{Bytes: [16]byte(f.user.UserID), Valid: true}
	f.s.cols[c.ID] = nonAdminOwned
	rec := f.do(t, "POST", "/collections/"+c.ID.String()+"/items", f.user,
		map[string]any{"media_item_ids": []string{f.show.String(), f.secret.String()}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("item from an ungranted library = %d, want 404", rec.Code)
	}
	if n := len(f.s.members[c.ID]); n != 0 {
		t.Fatalf("partial batch added %d items", n)
	}
}

func TestManualCollection_Reorder(t *testing.T) {
	f := newMCFixture(t, true)
	c := f.manual("Spooky", f.movie, f.show)
	path := "/collections/" + c.ID.String() + "/items/order"
	if rec := f.do(t, "PUT", path, f.user, map[string]any{"item_ids": []string{f.show.String(), f.movie.String()}}); rec.Code != http.StatusNotFound {
		t.Fatalf("non-admin reorder = %d, want 404", rec.Code)
	}
	if rec := f.do(t, "PUT", path, f.admin, map[string]any{"item_ids": []string{f.show.String(), f.show.String()}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("duplicate ids = %d, want 400", rec.Code)
	}
	if rec := f.do(t, "PUT", path, f.admin, map[string]any{"item_ids": []string{f.show.String(), f.movie.String()}}); rec.Code != http.StatusNoContent {
		t.Fatalf("reorder = %d (%s)", rec.Code, rec.Body.String())
	}
	if len(f.s.reorders) != 1 || f.s.reorders[0].ItemIds[0] != f.show {
		t.Fatalf("reorders %+v", f.s.reorders)
	}

	pl := gen.Collection{ID: uuid.New(), Name: "Mine", Type: "playlist",
		UserID: pgtype.UUID{Bytes: [16]byte(f.user.UserID), Valid: true}}
	f.s.cols[pl.ID] = pl
	if rec := f.do(t, "PUT", "/collections/"+pl.ID.String()+"/items/order", f.user, map[string]any{"item_ids": []string{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("reorder a playlist here = %d, want 400", rec.Code)
	}

	fr := gen.Collection{ID: uuid.New(), Name: "Alien Collection", Type: "franchise"}
	f.s.cols[fr.ID] = fr
	if rec := f.do(t, "PUT", "/collections/"+fr.ID.String()+"/items/order", f.admin, map[string]any{"item_ids": []string{}}); rec.Code != http.StatusConflict {
		t.Fatalf("reorder a franchise = %d, want 409", rec.Code)
	}
}

func TestManualCollection_ItemsUseTheChosenOrder(t *testing.T) {
	f := newMCFixture(t, true)
	c := f.manual("Spooky", f.movie)
	c.ItemOrder = "release"
	f.s.cols[c.ID] = c
	f.s.visible = []gen.ListVisibleManualCollectionsRow{{ID: c.ID, ItemCount: 1}}
	if rec := f.do(t, "GET", "/collections/"+c.ID.String()+"/items", f.admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("items = %d", rec.Code)
	}
	if got := f.s.itemsArgs[len(f.s.itemsArgs)-1].ItemOrder; got != "release" {
		t.Fatalf("manual items listed with order %q, want release", got)
	}

	pl := gen.Collection{ID: uuid.New(), Name: "Mine", Type: "playlist", ItemOrder: "title",
		UserID: pgtype.UUID{Bytes: [16]byte(f.user.UserID), Valid: true}}
	f.s.cols[pl.ID] = pl
	if rec := f.do(t, "GET", "/collections/"+pl.ID.String()+"/items", f.user, nil); rec.Code != http.StatusOK {
		t.Fatalf("playlist items = %d", rec.Code)
	}
	if got := f.s.itemsArgs[len(f.s.itemsArgs)-1].ItemOrder; got != "" {
		t.Fatalf("playlist listed with order %q, want stored positions", got)
	}
}

func TestManualCollection_LibraryTab(t *testing.T) {
	f := newMCFixture(t, true)
	cover := "/artwork/bluey.jpg"
	f.s.library = []gen.ListLibraryManualCollectionsRow{
		{ID: uuid.New(), Name: "Zoo animals", Type: "manual", ItemCount: 2},
		{ID: uuid.New(), Name: "Bedtime", Type: "manual", ItemCount: 1, CoverPosterPath: cover},
	}
	rec := f.do(t, "GET", "/libraries/"+mcLibKids.String()+"/collections", f.user, nil)
	var got []libraryCollectionResponse
	mcDecode(t, rec, &got)
	if len(got) != 2 || got[0].Name != "Bedtime" || got[1].Name != "Zoo animals" {
		t.Fatalf("library tab %+v, want both by name", got)
	}
	if got[0].PosterPath == nil || *got[0].PosterPath != cover || got[1].PosterPath != nil {
		t.Fatalf("covers %v / %v", got[0].PosterPath, got[1].PosterPath)
	}
}

func TestManualCollection_ItemDetailRefs(t *testing.T) {
	s := newMCStore()
	item := uuid.New()
	a := gen.Collection{ID: uuid.New(), Name: "Spooky", Type: "manual"}
	pl := gen.Collection{ID: uuid.New(), Name: "My list", Type: "playlist"}
	s.cols[a.ID], s.cols[pl.ID] = a, pl
	s.members[a.ID] = []uuid.UUID{item}
	s.members[pl.ID] = []uuid.UUID{item}
	h := &ItemHandler{logger: slog.Default()}
	if refs := h.manualCollectionRefs(context.Background(), item); refs != nil {
		t.Fatalf("unwired refs = %v, want nil", refs)
	}
	h.WithManualCollections(s)
	refs := h.manualCollectionRefs(context.Background(), item)
	if len(refs) != 1 || refs[0].ID != a.ID.String() || refs[0].Name != "Spooky" {
		t.Fatalf("refs %+v, want only the manual collection", refs)
	}
	if refs := h.manualCollectionRefs(context.Background(), uuid.New()); refs != nil {
		t.Fatalf("refs for a non-member = %v, want nil", refs)
	}
}
