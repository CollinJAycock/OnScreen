package v1

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

func TestManualCollection_Promote(t *testing.T) {
	f := newMCFixture(t, true)
	rec := f.do(t, "POST", "/collections", f.admin, map[string]any{"name": "Spooky", "type": "manual", "promoted": true})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create promoted = %d (%s)", rec.Code, rec.Body.String())
	}
	var got collectionResponse
	mcDecode(t, rec, &got)
	id := uuid.MustParse(got.ID)
	if !got.Promoted || !f.s.cols[id].Promoted {
		t.Fatalf("not promoted: %+v", got)
	}
	if rec := f.do(t, "PATCH", "/collections/"+got.ID, f.admin, map[string]any{"promoted": false}); rec.Code != http.StatusOK {
		t.Fatalf("unpromote = %d", rec.Code)
	}
	if f.s.cols[id].Promoted {
		t.Fatal("still promoted")
	}
	if rec := f.do(t, "PATCH", "/collections/"+got.ID, f.user, map[string]any{"promoted": true}); rec.Code != http.StatusNotFound {
		t.Fatalf("non-admin promote = %d, want 404", rec.Code)
	}
}

func TestManualCollection_SmartRules(t *testing.T) {
	f := newMCFixture(t, true)
	f.smart.matches = []uuid.UUID{f.movie}

	if rec := f.do(t, "POST", "/collections", f.admin, map[string]any{"name": "Bad", "type": "manual", "rules": map[string]any{"types": []string{"episode"}}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid rules = %d, want 400", rec.Code)
	}
	if len(f.s.cols) != 0 {
		t.Fatal("invalid rules still created a collection")
	}

	rec := f.do(t, "POST", "/collections", f.admin, map[string]any{
		"name": "80s horror", "type": "manual",
		"rules": map[string]any{"types": []string{"movie"}, "genres": []string{"Horror"}, "year_min": 1980, "year_max": 1989},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create smart = %d (%s)", rec.Code, rec.Body.String())
	}
	var got collectionResponse
	mcDecode(t, rec, &got)
	id := uuid.MustParse(got.ID)
	if len(got.Rules) == 0 || got.ItemCount == nil || *got.ItemCount != 1 {
		t.Fatalf("smart create response %+v", got)
	}
	if len(f.smart.refreshed) != 1 || f.smart.refreshed[0].Genres[0] != "Horror" {
		t.Fatalf("refreshes %+v", f.smart.refreshed)
	}
	if m := f.s.members[id]; len(m) != 1 || m[0] != f.movie {
		t.Fatalf("members %v", m)
	}

	// Hand edits of the members are refused.
	base := "/collections/" + got.ID
	if rec := f.do(t, "POST", base+"/items", f.admin, map[string]any{"media_item_id": f.show.String()}); rec.Code != http.StatusBadRequest {
		t.Fatalf("add to smart = %d, want 400", rec.Code)
	}
	if rec := f.do(t, "DELETE", base+"/items/"+f.movie.String(), f.admin, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("remove from smart = %d, want 400", rec.Code)
	}
	if rec := f.do(t, "PUT", base+"/items/order", f.admin, map[string]any{"item_ids": []string{f.movie.String()}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("reorder smart = %d, want 400", rec.Code)
	}

	// New rules refresh; null makes it hand-picked again, keeping members.
	f.smart.matches = []uuid.UUID{f.movie, f.show}
	if rec := f.do(t, "PATCH", base, f.admin, map[string]any{"rules": map[string]any{"types": []string{"movie", "show"}}}); rec.Code != http.StatusOK {
		t.Fatalf("patch rules = %d (%s)", rec.Code, rec.Body.String())
	}
	if len(f.s.members[id]) != 2 || len(f.smart.refreshed) != 2 {
		t.Fatalf("after new rules: members %v refreshes %d", f.s.members[id], len(f.smart.refreshed))
	}
	if rec := f.do(t, "PATCH", base, f.admin, map[string]any{"rules": nil}); rec.Code != http.StatusOK {
		t.Fatalf("clear rules = %d", rec.Code)
	}
	if isSmart(f.s.cols[id]) || len(f.s.members[id]) != 2 {
		t.Fatalf("after clearing: smart=%v members %v", isSmart(f.s.cols[id]), f.s.members[id])
	}
	if rec := f.do(t, "POST", base+"/items", f.admin, map[string]any{"media_item_id": f.secret.String()}); rec.Code != http.StatusNoContent {
		t.Fatalf("add after clearing = %d", rec.Code)
	}
	if rec := f.do(t, "PATCH", base, f.admin, map[string]any{"rules": map[string]any{"types": []string{}}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("patch invalid rules = %d, want 400", rec.Code)
	}
}

func TestManualCollection_SmartNeedsRefresher(t *testing.T) {
	f := newMCFixture(t, false)
	s := f.s
	h := NewCollectionHandler(s, slog.Default()).WithManual(s, nil)
	req := httptest.NewRequest("POST", "/collections", bytes.NewReader([]byte(`{"name":"X","type":"manual","rules":{"types":["movie"]}}`)))
	req = req.WithContext(middleware.WithClaims(req.Context(), f.admin))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("rules without a refresher = %d, want 400", rec.Code)
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (f *mcFixture) upload(t *testing.T, path string, claims *auth.Claims, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("poster", "cover.png")
	_, _ = part.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest("POST", path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func TestManualCollection_Poster(t *testing.T) {
	f := newMCFixture(t, true)
	c := f.manual("Saturday cartoons", f.show)
	hidden := f.manual("Grown-up films", f.movie)
	f.s.visible = []gen.ListVisibleManualCollectionsRow{{ID: c.ID, ItemCount: 1}}
	path := "/collections/" + c.ID.String() + "/poster"

	if rec := f.do(t, "GET", path, f.user, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("no poster yet = %d, want 404", rec.Code)
	}
	if rec := f.upload(t, path, f.user, pngBytes(t, 20, 30)); rec.Code != http.StatusNotFound {
		t.Fatalf("non-admin upload = %d, want 404", rec.Code)
	}
	if rec := f.upload(t, path, f.admin, []byte("not an image")); rec.Code != http.StatusBadRequest {
		t.Fatalf("garbage upload = %d, want 400", rec.Code)
	}
	rec := f.upload(t, path, f.admin, pngBytes(t, 2000, 3000))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d (%s)", rec.Code, rec.Body.String())
	}
	var got collectionResponse
	mcDecode(t, rec, &got)
	if got.PosterVersion == nil {
		t.Fatalf("no poster_version after upload: %+v", got)
	}
	stored := f.s.posters[c.ID]
	if stored.ContentType != "image/jpeg" || len(stored.Data) < 3 || stored.Data[0] != 0xFF || stored.Data[1] != 0xD8 {
		t.Fatalf("stored %q (% x…)", stored.ContentType, stored.Data[:3])
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(stored.Data))
	if err != nil || cfg.Width > 1000 || cfg.Height > 1500 {
		t.Fatalf("stored cover %dx%d (%v), want within 1000x1500", cfg.Width, cfg.Height, err)
	}

	// The list carries the version too.
	var list []collectionResponse
	mcDecode(t, f.do(t, "GET", "/collections", f.user, nil), &list)
	if len(list) != 1 || list[0].PosterVersion == nil || *list[0].PosterVersion != *got.PosterVersion {
		t.Fatalf("list %+v", list)
	}

	// Served to a viewer who can see the collection; 404 for one who can't.
	rec = f.do(t, "GET", path, f.user, nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" || !bytes.Equal(rec.Body.Bytes(), stored.Data) {
		t.Fatalf("serve = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	etag := rec.Header().Get("ETag")
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("If-None-Match", etag)
	req = req.WithContext(middleware.WithClaims(req.Context(), f.user))
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("conditional get = %d, want 304", rr.Code)
	}
	f.s.posters[hidden.ID] = stored
	if rec := f.do(t, "GET", "/collections/"+hidden.ID.String()+"/poster", f.user, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("invisible collection's poster = %d, want 404", rec.Code)
	}
	if rec := f.do(t, "GET", "/collections/"+hidden.ID.String()+"/poster", f.admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin gets any poster = %d", rec.Code)
	}

	if rec := f.do(t, "DELETE", path, f.admin, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	if _, ok := f.s.posters[c.ID]; ok {
		t.Fatal("poster not deleted")
	}

	pl := gen.Collection{ID: uuid.New(), Name: "Mine", Type: "playlist"}
	f.s.cols[pl.ID] = pl
	if rec := f.upload(t, "/collections/"+pl.ID.String()+"/poster", f.admin, pngBytes(t, 10, 10)); rec.Code != http.StatusBadRequest {
		t.Fatalf("playlist upload = %d, want 400", rec.Code)
	}
}

type hubCollFake struct {
	promoted []gen.ListPromotedCollectionsRow
	items    map[uuid.UUID][]gen.ListCollectionItemsRow
	args     []gen.ListCollectionItemsParams
}

func (f *hubCollFake) ListPromotedCollections(context.Context) ([]gen.ListPromotedCollectionsRow, error) {
	return f.promoted, nil
}
func (f *hubCollFake) ListCollectionItems(_ context.Context, p gen.ListCollectionItemsParams) ([]gen.ListCollectionItemsRow, error) {
	f.args = append(f.args, p)
	return f.items[p.CollectionID], nil
}

func TestHubCollectionRows(t *testing.T) {
	allowed, denied := uuid.New(), uuid.New()
	spooky, private, many := uuid.New(), uuid.New(), uuid.New()
	f := &hubCollFake{
		promoted: []gen.ListPromotedCollectionsRow{
			{ID: spooky, Name: "Spooky", ItemOrder: "release"},
			{ID: private, Name: "Private"},
			{ID: many, Name: "Many", ItemOrder: "custom"},
		},
		items: map[uuid.UUID][]gen.ListCollectionItemsRow{
			spooky:  {{ID: uuid.New(), LibraryID: denied, Title: "Hidden"}, {ID: uuid.New(), LibraryID: allowed, Title: "Halloween", Type: "movie"}},
			private: {{ID: uuid.New(), LibraryID: denied, Title: "Nope"}},
		},
	}
	for i := 0; i < 30; i++ {
		f.items[many] = append(f.items[many], gen.ListCollectionItemsRow{ID: uuid.New(), LibraryID: allowed, Title: "x"})
	}
	h := (&HubHandler{logger: slog.Default()}).WithCollections(f)
	rank := int32(3)
	rows := h.collectionRows(context.Background(), &rank, func(id uuid.UUID) bool { return id == allowed })
	if len(rows) != 2 || rows[0].Name != "Spooky" || rows[1].Name != "Many" {
		t.Fatalf("rows %+v, want Spooky and Many (Private has nothing visible)", rows)
	}
	if len(rows[0].Items) != 1 || rows[0].Items[0].Title != "Halloween" || rows[0].CollectionID != spooky.String() {
		t.Fatalf("spooky row %+v", rows[0])
	}
	if len(rows[1].Items) != hubCollectionRowLimit {
		t.Fatalf("many row has %d items, want %d", len(rows[1].Items), hubCollectionRowLimit)
	}
	if a := f.args[0]; a.ItemOrder != "release" || a.MaxRatingRank == nil || *a.MaxRatingRank != 3 {
		t.Fatalf("items query %+v: want the collection's order and the viewer's ceiling", a)
	}
}
