package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/people"
)

// ── mocks ────────────────────────────────────────────────────────────────────

type fakePeopleService struct {
	credits     []people.Credit
	creditsErr  error
	person      people.Person
	personErr   error
	films       []people.FilmographyEntry
	filmsErr    error
	search      []people.Summary
	searchErr   error
	gotItemID   uuid.UUID
	gotItemType string
	gotTMDB     *int
}

func (f *fakePeopleService) GetCredits(_ context.Context, itemID uuid.UUID, itemType string, tmdbID *int) ([]people.Credit, error) {
	f.gotItemID = itemID
	f.gotItemType = itemType
	f.gotTMDB = tmdbID
	return f.credits, f.creditsErr
}
func (f *fakePeopleService) GetPerson(_ context.Context, _ uuid.UUID) (people.Person, error) {
	return f.person, f.personErr
}
func (f *fakePeopleService) GetFilmography(_ context.Context, _ uuid.UUID) ([]people.FilmographyEntry, error) {
	return f.films, f.filmsErr
}
func (f *fakePeopleService) Search(_ context.Context, _ string, _ int32) ([]people.Summary, error) {
	return f.search, f.searchErr
}

type fakePeopleItems struct {
	itemType string
	tmdbID   *int
	itemErr  error

	resolveTMDB    *int
	resolveErr     error
	resolveCalled  bool
}

func (f *fakePeopleItems) GetItemTypeAndTMDB(_ context.Context, _ uuid.UUID) (string, *int, error) {
	return f.itemType, f.tmdbID, f.itemErr
}
func (f *fakePeopleItems) ResolveTMDBID(_ context.Context, _ uuid.UUID) (*int, error) {
	f.resolveCalled = true
	return f.resolveTMDB, f.resolveErr
}

// ── helpers ──────────────────────────────────────────────────────────────────

func peopleReq(method, path, idParam string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	rctx := chi.NewRouteContext()
	if idParam != "" {
		rctx.URLParams.Add("id", idParam)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// ── Credits ──────────────────────────────────────────────────────────────────

func TestPeople_Credits_LazyResolveOnMissingTMDB(t *testing.T) {
	itemID := uuid.New()
	resolved := 603
	items := &fakePeopleItems{itemType: "movie", tmdbID: nil, resolveTMDB: &resolved}
	svc := &fakePeopleService{
		credits: []people.Credit{
			{Person: people.Summary{ID: uuid.New(), Name: "Keanu Reeves"}, Role: "cast", Character: "Neo", Order: 0},
		},
	}
	h := NewPeopleHandler(svc, items, slog.Default())

	rec := httptest.NewRecorder()
	h.Credits(rec, peopleReq(http.MethodGet, "/items/"+itemID.String()+"/credits", itemID.String()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rec.Code, rec.Body.String())
	}
	if !items.resolveCalled {
		t.Error("missing tmdb_id should trigger ResolveTMDBID")
	}
	if svc.gotTMDB == nil || *svc.gotTMDB != 603 {
		t.Errorf("svc.GetCredits got tmdbID = %v, want 603 from resolve", svc.gotTMDB)
	}
}

func TestPeople_Credits_DoesNotResolveWhenTMDBPresent(t *testing.T) {
	itemID := uuid.New()
	tmdb := 11
	items := &fakePeopleItems{itemType: "movie", tmdbID: &tmdb}
	svc := &fakePeopleService{}
	h := NewPeopleHandler(svc, items, slog.Default())

	rec := httptest.NewRecorder()
	h.Credits(rec, peopleReq(http.MethodGet, "/", itemID.String()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if items.resolveCalled {
		t.Error("ResolveTMDBID should NOT be called when tmdb_id already set")
	}
	if svc.gotTMDB == nil || *svc.gotTMDB != 11 {
		t.Errorf("got tmdbID = %v, want 11", svc.gotTMDB)
	}
}

func TestPeople_Credits_BadUUIDIs400(t *testing.T) {
	h := NewPeopleHandler(&fakePeopleService{}, &fakePeopleItems{}, slog.Default())
	rec := httptest.NewRecorder()
	h.Credits(rec, peopleReq(http.MethodGet, "/", "not-a-uuid"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestPeople_Credits_ItemLookupFailureIs404(t *testing.T) {
	items := &fakePeopleItems{itemErr: errors.New("not found")}
	h := NewPeopleHandler(&fakePeopleService{}, items, slog.Default())
	rec := httptest.NewRecorder()
	h.Credits(rec, peopleReq(http.MethodGet, "/", uuid.New().String()))
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

// ── GetPerson ────────────────────────────────────────────────────────────────

func TestPeople_GetPerson_ReturnsAllFields(t *testing.T) {
	tmdb := 6384
	bio := "American actor"
	svc := &fakePeopleService{
		person: people.Person{
			ID:     uuid.New(),
			TMDBID: &tmdb,
			Name:   "Keanu Reeves",
			Bio:    &bio,
		},
	}
	h := NewPeopleHandler(svc, &fakePeopleItems{}, slog.Default())
	rec := httptest.NewRecorder()
	h.GetPerson(rec, peopleReq(http.MethodGet, "/", uuid.New().String()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data personResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.Name != "Keanu Reeves" || env.Data.TMDBID == nil || *env.Data.TMDBID != 6384 {
		t.Errorf("got %+v", env.Data)
	}
	if env.Data.Bio == nil || *env.Data.Bio != "American actor" {
		t.Error("bio should be passed through")
	}
}

func TestPeople_GetPerson_NotFoundReturns404(t *testing.T) {
	svc := &fakePeopleService{personErr: people.ErrNotFound}
	h := NewPeopleHandler(svc, &fakePeopleItems{}, slog.Default())
	rec := httptest.NewRecorder()
	h.GetPerson(rec, peopleReq(http.MethodGet, "/", uuid.New().String()))
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestPeople_GetPerson_OtherErrorReturns500(t *testing.T) {
	svc := &fakePeopleService{personErr: errors.New("db down")}
	h := NewPeopleHandler(svc, &fakePeopleItems{}, slog.Default())
	rec := httptest.NewRecorder()
	h.GetPerson(rec, peopleReq(http.MethodGet, "/", uuid.New().String()))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500 (only ErrNotFound should 404)", rec.Code)
	}
}

func TestPeople_GetPerson_BadUUIDIs400(t *testing.T) {
	h := NewPeopleHandler(&fakePeopleService{}, &fakePeopleItems{}, slog.Default())
	rec := httptest.NewRecorder()
	h.GetPerson(rec, peopleReq(http.MethodGet, "/", "bogus"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

// ── Filmography ──────────────────────────────────────────────────────────────

func TestPeople_Filmography_ReturnsEntries(t *testing.T) {
	yr := 1999
	svc := &fakePeopleService{
		films: []people.FilmographyEntry{
			{ItemID: uuid.New(), Title: "The Matrix", Type: "movie", Year: &yr, Role: "cast", Character: "Neo"},
			{ItemID: uuid.New(), Title: "Speed", Type: "movie", Role: "cast", Character: "Jack Traven"},
		},
	}
	h := NewPeopleHandler(svc, &fakePeopleItems{}, slog.Default())
	rec := httptest.NewRecorder()
	h.Filmography(rec, peopleReq(http.MethodGet, "/", uuid.New().String()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var env struct {
		Data []filmographyEntryResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data) != 2 {
		t.Fatalf("got %d entries, want 2", len(env.Data))
	}
	if env.Data[0].Title != "The Matrix" || env.Data[0].Character != "Neo" {
		t.Errorf("first entry = %+v", env.Data[0])
	}
}

// ── Search ───────────────────────────────────────────────────────────────────

func TestPeople_Search_ReturnsSummaries(t *testing.T) {
	svc := &fakePeopleService{
		search: []people.Summary{
			{ID: uuid.New(), Name: "Keanu Reeves"},
			{ID: uuid.New(), Name: "Keanu Other"},
		},
	}
	h := NewPeopleHandler(svc, &fakePeopleItems{}, slog.Default())
	rec := httptest.NewRecorder()
	h.Search(rec, httptest.NewRequest(http.MethodGet, "/people?q=keanu", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var env struct {
		Data []personSummaryResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data) != 2 {
		t.Errorf("got %d results", len(env.Data))
	}
}

func TestPeople_Search_EmptyResultIsEmptyArray(t *testing.T) {
	// Want [] not null for clients that map directly into a list type.
	svc := &fakePeopleService{search: nil}
	h := NewPeopleHandler(svc, &fakePeopleItems{}, slog.Default())
	rec := httptest.NewRecorder()
	h.Search(rec, httptest.NewRequest(http.MethodGet, "/people?q=zzzz", nil))

	var env struct {
		Data []personSummaryResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// `make([]X, 0)` for empty slice path.
	if env.Data == nil || len(env.Data) != 0 {
		t.Errorf("got %v, want empty array", env.Data)
	}
}

func TestPeople_Search_ServiceErrorIs500(t *testing.T) {
	svc := &fakePeopleService{searchErr: errors.New("db boom")}
	h := NewPeopleHandler(svc, &fakePeopleItems{}, slog.Default())
	rec := httptest.NewRecorder()
	h.Search(rec, httptest.NewRequest(http.MethodGet, "/people?q=x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rec.Code)
	}
}
