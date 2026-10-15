package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/domain/media"
)

type fakeItemCaches struct{ calls int }

func (f *fakeItemCaches) InvalidateItemCaches() { f.calls++ }

// patchItem runs PATCH /items/{id} with body as the given caller.
func patchItem(t *testing.T, h *ItemHandler, id uuid.UUID, body string, admin bool) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("PATCH", "/", strings.NewReader(body)), "id", id.String())
	req = req.WithContext(middleware.WithClaims(req.Context(),
		&auth.Claims{UserID: uuid.New(), Username: "u", IsAdmin: admin}))
	h.UpdateMetadata(rec, req)
	return rec
}

func TestUpdateMetadata_NonAdminForbidden(t *testing.T) {
	ms := &mockItemMedia{item: &media.Item{ID: uuid.New(), Type: "movie"}}
	rec := patchItem(t, newItemHandler(ms), ms.item.ID, `{"title":"X"}`, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if len(ms.edits) != 0 {
		t.Error("non-admin edit reached the service")
	}
}

func TestUpdateMetadata_EditsAndLocksMovieFields(t *testing.T) {
	id := uuid.New()
	ms := &mockItemMedia{item: &media.Item{ID: id, Type: "movie", Title: "Old"}}
	caches := &fakeItemCaches{}
	h := newItemHandler(ms).WithCacheInvalidator(caches)

	rec := patchItem(t, h, id, `{
		"title": " Blade Runner ", "original_title": "", "tagline": "More human than human",
		"year": 1982, "rating": 8.1, "content_rating": "R",
		"genres": ["Sci-Fi", " sci-fi ", "", "Noir"], "taken_at": "1982-06-25"
	}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	p := ms.edits[0]
	if p.Title != "Blade Runner" || p.SortTitle != "Blade Runner" {
		t.Errorf("title/sort = %q/%q", p.Title, p.SortTitle)
	}
	if p.OriginalTitle != nil {
		t.Errorf("empty original_title should clear, got %q", *p.OriginalTitle)
	}
	if p.Year == nil || *p.Year != 1982 || p.Rating == nil || *p.Rating != 8.1 {
		t.Errorf("year/rating = %v/%v", p.Year, p.Rating)
	}
	if !slices.Equal(p.Genres, []string{"Sci-Fi", "Noir"}) {
		t.Errorf("genres = %v", p.Genres)
	}
	if p.OriginallyAvailableAt == nil || p.OriginallyAvailableAt.Format("2006-01-02") != "1982-06-25" {
		t.Errorf("date = %v", p.OriginallyAvailableAt)
	}
	// Every field the body sets is locked; the sort title derived from the
	// title is written but not locked.
	want := []string{"title", "original_title", "tagline", "originally_available_at",
		"year", "rating", "content_rating", "genres"}
	for _, f := range want {
		if !slices.Contains(p.Lock, f) || !slices.Contains(p.Set, f) {
			t.Errorf("%s: set=%v lock=%v", f, p.Set, p.Lock)
		}
	}
	if !slices.Contains(p.Set, "sort_title") || slices.Contains(p.Lock, "sort_title") {
		t.Errorf("derived sort title: set=%v lock=%v", p.Set, p.Lock)
	}
	if caches.calls != 1 {
		t.Errorf("cache invalidations = %d, want 1", caches.calls)
	}

	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"id", "title", "summary", "originally_available_at", "locked_fields"} {
		if _, ok := env.Data[k]; !ok {
			t.Errorf("response lacks %q", k)
		}
	}
}

func TestUpdateMetadata_SortTitleRules(t *testing.T) {
	cases := []struct {
		name       string
		locked     []string
		body       string
		wantSort   string
		wantSet    bool
		wantLock   bool
		wantUnlock bool
	}{
		{"explicit sort title is locked", nil, `{"title":"The Thing","sort_title":"Thing, The"}`, "Thing, The", true, true, false},
		{"locked sort title survives a title edit", []string{"sort_title"}, `{"title":"New"}`, "", false, false, false},
		{"empty sort title follows the title and unlocks", []string{"sort_title"}, `{"sort_title":""}`, "Current", true, false, true},
		{"summary-only edit leaves sort title", nil, `{"summary":"x"}`, "", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			ms := &mockItemMedia{item: &media.Item{ID: id, Type: "movie", Title: "Current", LockedFields: tc.locked}}
			rec := patchItem(t, newItemHandler(ms), id, tc.body, true)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body)
			}
			p := ms.edits[0]
			if got := slices.Contains(p.Set, "sort_title"); got != tc.wantSet {
				t.Errorf("sort_title set = %v, want %v", got, tc.wantSet)
			}
			if tc.wantSet && p.SortTitle != tc.wantSort {
				t.Errorf("sort title = %q, want %q", p.SortTitle, tc.wantSort)
			}
			if got := slices.Contains(p.Lock, "sort_title"); got != tc.wantLock {
				t.Errorf("sort_title locked = %v, want %v", got, tc.wantLock)
			}
			if got := slices.Contains(p.Unlock, "sort_title"); got != tc.wantUnlock {
				t.Errorf("sort_title unlocked = %v, want %v", got, tc.wantUnlock)
			}
		})
	}
}

func TestUpdateMetadata_NullClearsNumbers(t *testing.T) {
	id := uuid.New()
	ms := &mockItemMedia{item: &media.Item{ID: id, Type: "episode", Title: "E1"}}
	rec := patchItem(t, newItemHandler(ms), id, `{"year":null,"rating":null}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	p := ms.edits[0]
	if p.Year != nil || p.Rating != nil || !slices.Contains(p.Set, "year") || !slices.Contains(p.Set, "rating") {
		t.Errorf("null should set and clear: %+v", p)
	}
}

func TestUpdateMetadata_UnlockOnly(t *testing.T) {
	id := uuid.New()
	ms := &mockItemMedia{item: &media.Item{ID: id, Type: "show", Title: "S", LockedFields: []string{"title", "genres"}}}
	rec := patchItem(t, newItemHandler(ms), id, `{"unlock_fields":["title","genres","genres"]}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	p := ms.edits[0]
	if len(p.Set) != 0 || !slices.Equal(p.Unlock, []string{"title", "genres"}) {
		t.Errorf("set=%v unlock=%v", p.Set, p.Unlock)
	}
}

func TestUpdateMetadata_BadRequests(t *testing.T) {
	for name, body := range map[string]string{
		"blank title":             `{"title":"  "}`,
		"year out of range":       `{"year":82}`,
		"rating out of range":     `{"rating":11}`,
		"tags not editable":       `{"tags":["x"]}`,
		"unknown unlock":          `{"unlock_fields":["poster_path"]}`,
		"edit and unlock at once": `{"title":"A","unlock_fields":["title"]}`,
		"bad date":                `{"taken_at":"yesterday"}`,
		"too many genres":         `{"genres":[` + genresJSON(51) + `]}`,
	} {
		t.Run(name, func(t *testing.T) {
			id := uuid.New()
			ms := &mockItemMedia{item: &media.Item{ID: id, Type: "movie", Title: "M"}}
			rec := patchItem(t, newItemHandler(ms), id, body, true)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
			if len(ms.edits) != 0 {
				t.Error("invalid edit reached the service")
			}
		})
	}
}

func genresJSON(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = `"g` + strings.Repeat("x", i) + `"`
	}
	return strings.Join(parts, ",")
}

func TestUpdateMetadata_TitleYearConflictIs409(t *testing.T) {
	id := uuid.New()
	ms := &mockItemMedia{
		item:    &media.Item{ID: id, Type: "movie", Title: "M"},
		editErr: &pgconn.PgError{Code: "23505"},
	}
	rec := patchItem(t, newItemHandler(ms), id, `{"title":"Taken"}`, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestUpdateMetadata_NotFound(t *testing.T) {
	ms := &mockItemMedia{itemErr: media.ErrNotFound}
	rec := patchItem(t, newItemHandler(ms), uuid.New(), `{"title":"X"}`, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestItemGet_LockedFieldsAlwaysPresent(t *testing.T) {
	id := uuid.New()
	ms := &mockItemMedia{item: &media.Item{ID: id, Type: "movie", Title: "M", SortTitle: "m"}}
	rec := httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest("GET", "/", nil), "id", id.String())
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), IsAdmin: true}))
	newItemHandler(ms).Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"locked_fields":[]`) {
		t.Errorf("detail should carry an empty locked_fields: %s", rec.Body)
	}
}
