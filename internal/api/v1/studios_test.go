package v1

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/studios"
)

// fakeStudioSvc records the scope it was asked with and answers from a
// fixed list of items per studio.
type fakeStudioSvc struct {
	studios   map[uuid.UUID]*studios.Studio
	items     map[uuid.UUID][]media.Item
	lastScope studios.Scope
	lastItems studios.ItemsParams
	listKind  string
	listLib   *uuid.UUID
}

func (f *fakeStudioSvc) Get(_ context.Context, id uuid.UUID) (*studios.Studio, error) {
	if s, ok := f.studios[id]; ok {
		return s, nil
	}
	return nil, studios.ErrNotFound
}

func (f *fakeStudioSvc) List(_ context.Context, lib *uuid.UUID, kind string, sc studios.Scope) ([]studios.Studio, error) {
	f.lastScope, f.listKind, f.listLib = sc, kind, lib
	var out []studios.Studio
	for _, s := range f.studios {
		out = append(out, *s)
	}
	return out, nil
}

func (f *fakeStudioSvc) Items(_ context.Context, p studios.ItemsParams) ([]media.Item, int64, error) {
	f.lastScope, f.lastItems = p.Scope, p
	// The real query filters by scope; the fake treats "no grants" as
	// "sees nothing" so the 404 path can be exercised.
	if p.Scope.LibraryIDs != nil && len(p.Scope.LibraryIDs) == 0 {
		return nil, 0, nil
	}
	items := f.items[p.StudioID]
	return items, int64(len(items)), nil
}

func studioReq(method, target string, c *auth.Claims, id string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	if id != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}
	return withRatingClaims(req, c)
}

func TestStudios_ListPassesCallerScope(t *testing.T) {
	lib := uuid.New()
	svc := &fakeStudioSvc{studios: map[uuid.UUID]*studios.Studio{}}
	h := NewStudioHandler(svc, slog.Default()).
		WithLibraryAccess(fakePeopleAccess{allowed: map[uuid.UUID]struct{}{lib: {}}})

	c := &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG-13", BlockedLabels: []string{"horror"}}
	rec := httptest.NewRecorder()
	h.List(rec, studioReq(http.MethodGet, "/studios?kind=network&library_id="+lib.String(), c, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	sc := svc.lastScope
	if len(sc.LibraryIDs) != 1 || sc.LibraryIDs[0] != lib {
		t.Errorf("library scope = %v, want the caller's grants", sc.LibraryIDs)
	}
	if sc.MaxRatingRank == nil {
		t.Error("rating ceiling not applied")
	}
	if !sc.Labels.Active() {
		t.Error("label restriction not applied")
	}
	if svc.listKind != "network" || svc.listLib == nil || *svc.listLib != lib {
		t.Errorf("kind %q library %v", svc.listKind, svc.listLib)
	}

	// A library the caller can't open is a 404, like one that doesn't exist.
	rec = httptest.NewRecorder()
	h.List(rec, studioReq(http.MethodGet, "/studios?library_id="+uuid.NewString(), c, ""))
	if rec.Code != http.StatusNotFound {
		t.Errorf("foreign library: status = %d, want 404", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.List(rec, studioReq(http.MethodGet, "/studios?kind=label", c, ""))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad kind: status = %d, want 400", rec.Code)
	}

	// Admin: no library scope (nil = every library).
	rec = httptest.NewRecorder()
	h.List(rec, studioReq(http.MethodGet, "/studios", &auth.Claims{UserID: uuid.New(), IsAdmin: true}, ""))
	if rec.Code != http.StatusOK || svc.lastScope.LibraryIDs != nil {
		t.Errorf("admin: status %d scope %v", rec.Code, svc.lastScope.LibraryIDs)
	}
}

func TestStudios_ItemsPagingSortAndVisibility(t *testing.T) {
	sid := uuid.New()
	movie := media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", Title: "Alien"}
	svc := &fakeStudioSvc{
		studios: map[uuid.UUID]*studios.Studio{sid: {ID: sid, Name: "20th Century Fox", Kind: "studio"}},
		items:   map[uuid.UUID][]media.Item{sid: {movie}},
	}
	h := NewStudioHandler(svc, slog.Default()).WithLibraryAccess(fakePeopleAccess{})
	admin := &auth.Claims{UserID: uuid.New(), IsAdmin: true}

	rec := httptest.NewRecorder()
	h.Items(rec, studioReq(http.MethodGet, "/studios/x/items?sort=year&limit=10&offset=20", admin, sid.String()))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var env struct {
		Data []MediaItemResponse `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 1 || env.Data[0].Title != "Alien" || env.Meta.Total != 1 {
		t.Errorf("body = %+v", env)
	}
	p := svc.lastItems
	if p.Sort != "year" || p.SortAsc || p.Limit != 10 || p.Offset != 20 {
		t.Errorf("params = %+v; year sorts newest first by default, paging passed through", p)
	}

	rec = httptest.NewRecorder()
	h.Items(rec, studioReq(http.MethodGet, "/studios/x/items?sort=popularity", admin, sid.String()))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown sort: status = %d, want 400", rec.Code)
	}

	// A user with no grants sees none of the studio's titles: 404 on both
	// the studio and its items, the same as an unknown id.
	none := &auth.Claims{UserID: uuid.New()}
	h = NewStudioHandler(svc, slog.Default()).WithLibraryAccess(fakePeopleAccess{allowed: map[uuid.UUID]struct{}{}})
	for _, fn := range []http.HandlerFunc{h.Items, h.Get} {
		rec = httptest.NewRecorder()
		fn(rec, studioReq(http.MethodGet, "/studios/x", none, sid.String()))
		if rec.Code != http.StatusNotFound {
			t.Errorf("invisible studio: status = %d, want 404", rec.Code)
		}
	}
	rec = httptest.NewRecorder()
	h.Get(rec, studioReq(http.MethodGet, "/studios/x", admin, uuid.NewString()))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown studio: status = %d, want 404", rec.Code)
	}
}

func TestStudios_LibraryAccessWired(t *testing.T) {
	if NewStudioHandler(&fakeStudioSvc{}, slog.Default()).LibraryAccessWired() {
		t.Error("unwired handler reports wired")
	}
}
