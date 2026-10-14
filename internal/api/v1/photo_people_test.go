package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// fakePeopleDB implements PhotoPeopleDB; the embedded nil interface makes
// any method a test doesn't expect panic.
type fakePeopleDB struct {
	PhotoPeopleDB
	items      map[uuid.UUID]gen.GetMediaItemRow
	faces      map[uuid.UUID]gen.PhotoFace
	people     map[uuid.UUID]gen.PhotoPerson
	personN    int64
	photoLibs  []uuid.UUID
	dated      int64
	candidates []gen.ListPhotoMemoryCandidatesRow
	setFace    *gen.SetPhotoFacePersonParams
	memArgs    *gen.ListPhotoMemoryCandidatesParams
}

func (f *fakePeopleDB) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	if it, ok := f.items[id]; ok {
		return it, nil
	}
	return gen.GetMediaItemRow{}, pgx.ErrNoRows
}

func (f *fakePeopleDB) ListPhotoFacesForItem(_ context.Context, id uuid.UUID) ([]gen.ListPhotoFacesForItemRow, error) {
	var out []gen.ListPhotoFacesForItemRow
	for _, fc := range f.faces {
		if fc.ItemID == id {
			out = append(out, gen.ListPhotoFacesForItemRow{ID: fc.ID, ItemID: fc.ItemID, X: fc.X, Y: fc.Y, W: fc.W, H: fc.H})
		}
	}
	return out, nil
}

func (f *fakePeopleDB) GetPhotoFace(_ context.Context, id uuid.UUID) (gen.PhotoFace, error) {
	if fc, ok := f.faces[id]; ok {
		return fc, nil
	}
	return gen.PhotoFace{}, pgx.ErrNoRows
}

func (f *fakePeopleDB) FindOrCreatePhotoPerson(_ context.Context, a gen.FindOrCreatePhotoPersonParams) (gen.PhotoPerson, error) {
	for _, p := range f.people {
		if p.LibraryID == a.LibraryID && strings.EqualFold(p.Name, a.Name) {
			return p, nil
		}
	}
	p := gen.PhotoPerson{ID: uuid.New(), LibraryID: a.LibraryID, Name: a.Name}
	f.people[p.ID] = p
	return p, nil
}

func (f *fakePeopleDB) GetPhotoPerson(_ context.Context, id uuid.UUID) (gen.PhotoPerson, error) {
	if p, ok := f.people[id]; ok {
		return p, nil
	}
	return gen.PhotoPerson{}, pgx.ErrNoRows
}

func (f *fakePeopleDB) SetPhotoFacePerson(_ context.Context, a gen.SetPhotoFacePersonParams) error {
	f.setFace = &a
	return nil
}

func (f *fakePeopleDB) CountPhotoPersonItems(context.Context, gen.CountPhotoPersonItemsParams) (int64, error) {
	return f.personN, nil
}

func (f *fakePeopleDB) ListPhotoLibraryIDs(context.Context) ([]uuid.UUID, error) {
	return append([]uuid.UUID(nil), f.photoLibs...), nil
}

func (f *fakePeopleDB) CountDatedPhotos(context.Context, []uuid.UUID) (int64, error) { return f.dated, nil }

func (f *fakePeopleDB) ListPhotoMemoryCandidates(_ context.Context, a gen.ListPhotoMemoryCandidatesParams) ([]gen.ListPhotoMemoryCandidatesRow, error) {
	f.memArgs = &a
	return f.candidates, nil
}

// allowAccess grants the listed libraries (nil = every library, as for an
// admin).
type allowAccess struct{ libs map[uuid.UUID]struct{} }

func (a allowAccess) CanAccessLibrary(_ context.Context, _, lib uuid.UUID, _ bool) (bool, error) {
	if a.libs == nil {
		return true, nil
	}
	_, ok := a.libs[lib]
	return ok, nil
}

func (a allowAccess) AllowedLibraryIDs(context.Context, uuid.UUID, bool) (map[uuid.UUID]struct{}, error) {
	return a.libs, nil
}

func photoPeopleReq(method, target, body string, claims *auth.Claims, params map[string]string) *http.Request {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rd)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	if claims != nil {
		ctx = middleware.WithClaims(ctx, claims)
	}
	return r.WithContext(ctx)
}

func newPeopleHandler(db *fakePeopleDB, access LibraryAccessChecker) *PhotoPeopleHandler {
	return NewPhotoPeopleHandler(db, slog.New(slog.NewTextHandler(io.Discard, nil))).WithLibraryAccess(access)
}

func TestItemFaces_RatingCeilingHidesPhoto(t *testing.T) {
	lib, photo := uuid.New(), uuid.New()
	r := "R"
	db := &fakePeopleDB{items: map[uuid.UUID]gen.GetMediaItemRow{
		photo: {ID: photo, LibraryID: lib, Type: "photo", ContentRating: &r},
	}}
	h := newPeopleHandler(db, allowAccess{})

	w := httptest.NewRecorder()
	h.ItemFaces(w, photoPeopleReq("GET", "/", "", &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG"},
		map[string]string{"id": photo.String()}))
	if w.Code != http.StatusNotFound {
		t.Fatalf("capped viewer: status %d, want 404", w.Code)
	}

	w = httptest.NewRecorder()
	h.ItemFaces(w, photoPeopleReq("GET", "/", "", &auth.Claims{UserID: uuid.New()}, map[string]string{"id": photo.String()}))
	if w.Code != http.StatusOK {
		t.Fatalf("unrestricted viewer: status %d", w.Code)
	}
	var env struct {
		Data struct {
			CanEdit bool `json:"can_edit"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if !env.Data.CanEdit {
		t.Fatal("an unrestricted member should be able to name faces")
	}
}

func TestUpdateFace_NamesAndPermissions(t *testing.T) {
	lib, photo, face := uuid.New(), uuid.New(), uuid.New()
	g := "G"
	db := &fakePeopleDB{
		items:  map[uuid.UUID]gen.GetMediaItemRow{photo: {ID: photo, LibraryID: lib, Type: "photo", ContentRating: &g}},
		faces:  map[uuid.UUID]gen.PhotoFace{face: {ID: face, ItemID: photo, LibraryID: lib, W: 0.1, H: 0.1}},
		people: map[uuid.UUID]gen.PhotoPerson{},
	}
	h := newPeopleHandler(db, allowAccess{})
	params := map[string]string{"id": face.String()}

	// A managed (rating-capped) profile may see the photo but not name it.
	w := httptest.NewRecorder()
	h.UpdateFace(w, photoPeopleReq("PUT", "/", `{"name":"Alice"}`, &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG-13"}, params))
	if w.Code != http.StatusForbidden {
		t.Fatalf("capped profile: status %d, want 403", w.Code)
	}

	w = httptest.NewRecorder()
	h.UpdateFace(w, photoPeopleReq("PUT", "/", `{"name":"  Alice   Smith "}`, &auth.Claims{UserID: uuid.New()}, params))
	if w.Code != http.StatusOK {
		t.Fatalf("name: status %d body %s", w.Code, w.Body)
	}
	if db.setFace == nil || !db.setFace.PersonID.Valid {
		t.Fatal("face not assigned")
	}
	if p := db.people[uuid.UUID(db.setFace.PersonID.Bytes)]; p.Name != "Alice Smith" {
		t.Fatalf("person name %q", p.Name)
	}

	// Unname.
	w = httptest.NewRecorder()
	h.UpdateFace(w, photoPeopleReq("PUT", "/", `{"person_id":null}`, &auth.Claims{UserID: uuid.New()}, params))
	if w.Code != http.StatusOK || db.setFace.PersonID.Valid {
		t.Fatalf("unname: status %d valid %v", w.Code, db.setFace.PersonID.Valid)
	}

	// A person from another library is refused.
	other := gen.PhotoPerson{ID: uuid.New(), LibraryID: uuid.New(), Name: "Bob"}
	db.people[other.ID] = other
	w = httptest.NewRecorder()
	h.UpdateFace(w, photoPeopleReq("PUT", "/", `{"person_id":"`+other.ID.String()+`"}`, &auth.Claims{UserID: uuid.New()}, params))
	if w.Code != http.StatusUnprocessableEntity && w.Code != http.StatusBadRequest {
		t.Fatalf("cross-library person: status %d", w.Code)
	}
}

func TestPerson_HiddenWhenNoVisiblePhotos(t *testing.T) {
	lib := uuid.New()
	p := gen.PhotoPerson{ID: uuid.New(), LibraryID: lib, Name: "Alice"}
	db := &fakePeopleDB{people: map[uuid.UUID]gen.PhotoPerson{p.ID: p}, personN: 0}
	h := newPeopleHandler(db, allowAccess{})
	params := map[string]string{"id": p.ID.String()}

	w := httptest.NewRecorder()
	h.Person(w, photoPeopleReq("GET", "/", "", &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG"}, params))
	if w.Code != http.StatusNotFound {
		t.Fatalf("restricted viewer with no visible photos: %d, want 404", w.Code)
	}
	// No access to the library at all.
	h2 := newPeopleHandler(&fakePeopleDB{people: db.people, personN: 5}, allowAccess{libs: map[uuid.UUID]struct{}{}})
	w = httptest.NewRecorder()
	h2.Person(w, photoPeopleReq("GET", "/", "", &auth.Claims{UserID: uuid.New()}, params))
	if w.Code != http.StatusNotFound {
		t.Fatalf("no library access: %d, want 404", w.Code)
	}
}

func TestNormalizePersonName(t *testing.T) {
	if n, err := normalizePersonName("  Ada \t Lovelace "); err != nil || n != "Ada Lovelace" {
		t.Fatalf("got %q, %v", n, err)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 101), "a\x00b"} {
		if _, err := normalizePersonName(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestMemories_OnlyAccessibleLibrariesAndGroups(t *testing.T) {
	mine, theirs := uuid.New(), uuid.New()
	loc := time.Local
	at := func(y int, m time.Month, d, hh, mm int) pgtype.Timestamptz {
		return pgtype.Timestamptz{Time: time.Date(y, m, d, hh, mm, 0, 0, loc), Valid: true}
	}
	db := &fakePeopleDB{
		photoLibs: []uuid.UUID{mine, theirs},
		dated:     10,
		candidates: []gen.ListPhotoMemoryCandidatesRow{
			{ID: uuid.New(), LibraryID: mine, TakenAt: at(2023, 10, 14, 9, 0)},
			{ID: uuid.New(), LibraryID: mine, TakenAt: at(2023, 10, 14, 18, 0), FaceCount: 2},
			// Small library: ±1 day counts as today.
			{ID: uuid.New(), LibraryID: mine, TakenAt: at(2020, 10, 13, 12, 0)},
			// Three days off, three photos: this week.
			{ID: uuid.New(), LibraryID: mine, TakenAt: at(2016, 10, 11, 10, 0)},
			{ID: uuid.New(), LibraryID: mine, TakenAt: at(2016, 10, 11, 11, 0)},
			{ID: uuid.New(), LibraryID: mine, TakenAt: at(2016, 10, 17, 12, 0)},
			// One stray photo three days off: not enough for a memory.
			{ID: uuid.New(), LibraryID: mine, TakenAt: at(2010, 10, 17, 12, 0)},
		},
	}
	h := newPeopleHandler(db, allowAccess{libs: map[uuid.UUID]struct{}{mine: {}}})
	w := httptest.NewRecorder()
	h.Memories(w, photoPeopleReq("GET", "/api/v1/photos/memories?date=2026-10-14", "", &auth.Claims{UserID: uuid.New()}, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if len(db.memArgs.LibraryIds) != 1 || db.memArgs.LibraryIds[0] != mine {
		t.Fatalf("queried libraries %v, want only the accessible one", db.memArgs.LibraryIds)
	}
	var env struct {
		Data MemoriesResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	g := env.Data.Groups
	if len(g) != 3 {
		t.Fatalf("groups = %+v", g)
	}
	if g[0].Title != "3 years ago today" || len(g[0].Items) != 2 || g[0].Cover.ID != db.candidates[1].ID {
		t.Fatalf("first group %+v", g[0])
	}
	if g[1].Kind != "on_this_day" || g[1].YearsAgo != 6 {
		t.Fatalf("second group %+v", g[1])
	}
	if g[2].Kind != "this_week" || g[2].Title != "This week 10 years ago" || g[2].Total != 3 {
		t.Fatalf("third group %+v", g[2])
	}

	w = httptest.NewRecorder()
	h.Memories(w, photoPeopleReq("GET", "/api/v1/photos/memories?date=14-10-2026", "", &auth.Claims{UserID: uuid.New()}, nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad date: %d", w.Code)
	}
}

func TestSpreadPick_AvoidsBursts(t *testing.T) {
	base := time.Date(2020, 1, 1, 8, 0, 0, 0, time.UTC)
	var ps []memoryPhoto
	// A 100-shot burst at 8:00, then one photo an hour for ten hours.
	for i := 0; i < 100; i++ {
		ps = append(ps, memoryPhoto{MemoryPhotoResponse: MemoryPhotoResponse{TakenAt: base.Add(time.Duration(i) * time.Second)}})
	}
	for i := 1; i <= 10; i++ {
		ps = append(ps, memoryPhoto{MemoryPhotoResponse: MemoryPhotoResponse{TakenAt: base.Add(time.Duration(i) * time.Hour)}})
	}
	got := spreadPick(ps, 12)
	if len(got) != 12 {
		t.Fatalf("picked %d", len(got))
	}
	inBurst := 0
	for i, p := range got {
		if p.TakenAt.Sub(base) < 2*time.Minute {
			inBurst++
		}
		if i > 0 && p.TakenAt.Before(got[i-1].TakenAt) {
			t.Fatal("not in time order")
		}
	}
	// All 10 hourly photos plus the burst's head, and one more burst shot.
	if inBurst != 2 {
		t.Fatalf("%d burst photos picked, want 2", inBurst)
	}
}

func TestSameDayIn_LeapDay(t *testing.T) {
	d := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	if got := sameDayIn(d, 2023); got.Month() != 2 || got.Day() != 28 {
		t.Fatalf("2023: %v", got)
	}
	if got := sameDayIn(d, 2020); got.Day() != 29 {
		t.Fatalf("2020: %v", got)
	}
}
