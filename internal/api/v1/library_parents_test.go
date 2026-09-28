package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
)

type fakeLibraryParentDB struct {
	titles map[uuid.UUID]string
	err    error
	calls  [][]uuid.UUID
	ranks  []*int32
}

func (f *fakeLibraryParentDB) ListMediaItemTitles(_ context.Context, arg gen.ListMediaItemTitlesParams) ([]gen.ListMediaItemTitlesRow, error) {
	ids := arg.Ids
	f.calls = append(f.calls, ids)
	f.ranks = append(f.ranks, arg.MaxRatingRank)
	if f.err != nil {
		return nil, f.err
	}
	var out []gen.ListMediaItemTitlesRow
	for _, id := range ids {
		if t, ok := f.titles[id]; ok {
			out = append(out, gen.ListMediaItemTitlesRow{ID: id, Title: t})
		}
	}
	return out, nil
}

type parentFields struct {
	ID          string  `json:"id"`
	ParentID    *string `json:"parent_id"`
	ParentTitle *string `json:"parent_title"`
}

func listParentFields(t *testing.T, h *LibraryHandler, libID uuid.UUID, target string) []parentFields {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Items(rec, withClaims(withChiParam(httptest.NewRequest(http.MethodGet, target, nil), "id", libID.String())))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []parentFields `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.Data
}

func musicLibrary(id uuid.UUID) *mockLibraryService {
	return &mockLibraryService{lib: &library.Library{ID: id, Name: "Music", Type: "music"}}
}

// An albums index labels every card with its artist from one batched lookup
// per page — the artist ids deduplicated, never a request per album.
func TestLibrary_Items_AlbumsCarryTheirArtist(t *testing.T) {
	libID := uuid.New()
	beatles, zappa := uuid.New(), uuid.New()
	albums := []media.Item{
		{ID: uuid.New(), Title: "Revolver", Type: "album", ParentID: &beatles},
		{ID: uuid.New(), Title: "Hot Rats", Type: "album", ParentID: &zappa},
		{ID: uuid.New(), Title: "Abbey Road", Type: "album", ParentID: &beatles},
	}
	db := &fakeLibraryParentDB{titles: map[uuid.UUID]string{beatles: "The Beatles", zappa: "Frank Zappa"}}
	h := newLibHandler(musicLibrary(libID)).
		WithMedia(&mockMediaLister{items: albums, count: 3}).
		WithParentTitles(db)

	got := listParentFields(t, h, libID, "/?type=album")

	want := []struct{ parentID, parentTitle string }{
		{beatles.String(), "The Beatles"},
		{zappa.String(), "Frank Zappa"},
		{beatles.String(), "The Beatles"},
	}
	if len(got) != len(want) {
		t.Fatalf("rows: got %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].ParentID == nil || *got[i].ParentID != w.parentID {
			t.Errorf("row %d parent_id: got %v, want %s", i, got[i].ParentID, w.parentID)
		}
		if got[i].ParentTitle == nil || *got[i].ParentTitle != w.parentTitle {
			t.Errorf("row %d parent_title: got %v, want %s", i, got[i].ParentTitle, w.parentTitle)
		}
	}
	if len(db.calls) != 1 {
		t.Fatalf("title lookups: got %d, want 1", len(db.calls))
	}
	if !slices.Equal(db.calls[0], []uuid.UUID{beatles, zappa}) {
		t.Errorf("looked up %v, want the two artists once each", db.calls[0])
	}
}

// Top-level rows — every row of a default listing — have no parent: no
// fields on the wire (the response is byte-for-byte what clients got before)
// and no query.
func TestLibrary_Items_TopLevelRowsHaveNoParentFields(t *testing.T) {
	libID := uuid.New()
	db := &fakeLibraryParentDB{}
	h := newLibHandler(musicLibrary(libID)).
		WithMedia(&mockMediaLister{items: []media.Item{{ID: uuid.New(), Title: "ABBA", Type: "artist"}}, count: 1}).
		WithParentTitles(db)

	rec := httptest.NewRecorder()
	h.Items(rec, withClaims(withChiParam(httptest.NewRequest(http.MethodGet, "/", nil), "id", libID.String())))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	var resp struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"parent_id", "parent_title"} {
		if _, present := resp.Data[0][key]; present {
			t.Errorf("top-level row carries %s", key)
		}
	}
	if len(db.calls) != 0 {
		t.Errorf("title lookup ran for a page with no parents: %v", db.calls)
	}
}

// The title is decoration: a failed lookup, or no lookup wired at all, keeps
// the listing and its parent_id and only drops parent_title.
func TestLibrary_Items_ParentTitleDegrades(t *testing.T) {
	artist := uuid.New()
	albums := []media.Item{{ID: uuid.New(), Title: "Arrival", Type: "album", ParentID: &artist}}
	for name, wire := range map[string]func(*LibraryHandler) *LibraryHandler{
		"lookup fails": func(h *LibraryHandler) *LibraryHandler {
			return h.WithParentTitles(&fakeLibraryParentDB{err: errors.New("db down")})
		},
		"not wired": func(h *LibraryHandler) *LibraryHandler { return h },
	} {
		t.Run(name, func(t *testing.T) {
			libID := uuid.New()
			h := wire(newLibHandler(musicLibrary(libID)).WithMedia(&mockMediaLister{items: albums, count: 1}))

			got := listParentFields(t, h, libID, "/?type=album")

			if len(got) != 1 {
				t.Fatalf("rows: got %d, want 1", len(got))
			}
			if got[0].ParentID == nil || *got[0].ParentID != artist.String() {
				t.Errorf("parent_id: got %v, want %s", got[0].ParentID, artist)
			}
			if got[0].ParentTitle != nil {
				t.Errorf("parent_title: got %q, want it omitted", *got[0].ParentTitle)
			}
		})
	}
}

// The parents are named under the caller's rating ceiling, like the rows:
// a capped profile's rank reaches the lookup, an uncapped one sends none.
func TestLibrary_Items_ParentTitlesHonourTheRatingCeiling(t *testing.T) {
	libID, parent := uuid.New(), uuid.New()
	db := &fakeLibraryParentDB{titles: map[uuid.UUID]string{parent: "Show"}}
	h := newLibHandler(musicLibrary(libID)).
		WithMedia(&mockMediaLister{items: []media.Item{{ID: uuid.New(), Type: "album", ParentID: &parent}}, count: 1}).
		WithParentTitles(db)

	listParentFields(t, h, libID, "/?type=album")
	if db.ranks[0] != nil {
		t.Errorf("uncapped caller: got rank %d, want none", *db.ranks[0])
	}

	req := withChiParam(httptest.NewRequest(http.MethodGet, "/?type=album", nil), "id", libID.String())
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG"}))
	rec := httptest.NewRecorder()
	h.Items(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	want := contentrating.MaxRatingRank("PG")
	if got := db.ranks[len(db.ranks)-1]; got == nil || want == nil || int(*got) != *want {
		t.Errorf("capped caller: got rank %v, want %v", got, want)
	}
}
