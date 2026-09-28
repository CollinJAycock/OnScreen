package uat

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── Franchise collections — any signed-in user; franchise rows read-only ─────

var (
	uatFranchiseCol = uuid.MustParse("00000000-0000-0000-0000-00000000f001")
	uatFranchiseCID = int32(8091)
)

// stubCollectionDB satisfies v1.CollectionDB with one franchise collection.
type stubCollectionDB struct{}

func (stubCollectionDB) franchise() gen.Collection {
	cid := uatFranchiseCID
	return gen.Collection{ID: uatFranchiseCol, Name: "Alien Collection", Type: "franchise", TmdbCollectionID: &cid}
}
func (s stubCollectionDB) ListCollections(context.Context, pgtype.UUID) ([]gen.Collection, error) {
	return []gen.Collection{s.franchise()}, nil
}
func (s stubCollectionDB) GetCollection(_ context.Context, id uuid.UUID) (gen.Collection, error) {
	if id == uatFranchiseCol {
		return s.franchise(), nil
	}
	return gen.Collection{}, pgx.ErrNoRows
}
func (stubCollectionDB) CreateCollection(context.Context, gen.CreateCollectionParams) (gen.Collection, error) {
	return gen.Collection{}, nil
}
func (stubCollectionDB) UpdateCollection(context.Context, gen.UpdateCollectionParams) (gen.Collection, error) {
	return gen.Collection{}, nil
}
func (stubCollectionDB) DeleteCollection(context.Context, uuid.UUID) error { return nil }
func (stubCollectionDB) ListCollectionItems(context.Context, gen.ListCollectionItemsParams) ([]gen.ListCollectionItemsRow, error) {
	return nil, nil
}
func (stubCollectionDB) CountCollectionItems(context.Context, gen.CountCollectionItemsParams) (int64, error) {
	return 0, nil
}
func (stubCollectionDB) AddCollectionItem(context.Context, gen.AddCollectionItemParams) (gen.CollectionItem, error) {
	return gen.CollectionItem{}, nil
}
func (stubCollectionDB) GetMediaItem(context.Context, uuid.UUID) (gen.GetMediaItemRow, error) {
	return gen.GetMediaItemRow{}, pgx.ErrNoRows
}
func (stubCollectionDB) RemoveCollectionItem(context.Context, gen.RemoveCollectionItemParams) error {
	return nil
}
func (stubCollectionDB) ListAutoGenreCollections(context.Context) ([]gen.Collection, error) {
	return nil, nil
}
func (stubCollectionDB) ListItemsByGenre(context.Context, gen.ListItemsByGenreParams) ([]gen.ListItemsByGenreRow, error) {
	return nil, nil
}
func (stubCollectionDB) CountItemsByGenre(context.Context, gen.CountItemsByGenreParams) (int64, error) {
	return 0, nil
}
func (stubCollectionDB) ListDistinctGenres(context.Context, uuid.UUID) ([]string, error) {
	return nil, nil
}

// stubFranchiseDB satisfies v1.CollectionFranchiseDB: the collection is
// visible, has a two-part snapshot, and nothing is owned.
type stubFranchiseDB struct{}

func (stubFranchiseDB) GetTMDBCollectionSnapshot(_ context.Context, id int32) (gen.TmdbCollection, error) {
	return gen.TmdbCollection{
		TmdbCollectionID: id, Name: "Alien Collection",
		Parts: []byte(`[{"tmdb_id":348,"title":"Alien","year":1979},{"tmdb_id":679,"title":"Aliens","year":1986}]`),
	}, nil
}
func (stubFranchiseDB) ListTMDBCollectionPosters(context.Context, []int32) ([]gen.ListTMDBCollectionPostersRow, error) {
	return nil, nil
}
func (stubFranchiseDB) ListVisibleFranchiseCollectionIDs(context.Context, gen.ListVisibleFranchiseCollectionIDsParams) ([]uuid.UUID, error) {
	return []uuid.UUID{uatFranchiseCol}, nil
}
func (stubFranchiseDB) ListLibraryFranchiseCollections(context.Context, gen.ListLibraryFranchiseCollectionsParams) ([]gen.ListLibraryFranchiseCollectionsRow, error) {
	cid := uatFranchiseCID
	return []gen.ListLibraryFranchiseCollectionsRow{{ID: uatFranchiseCol, Name: "Alien Collection", Type: "franchise", TmdbCollectionID: &cid, ItemCount: 2}}, nil
}
func (stubFranchiseDB) ListMediaItemsByTMDBIDs(context.Context, gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error) {
	return nil, nil
}

func newCollectionsServer(t *testing.T) *testServer {
	return newExtrasServer(t, func(h *api.Handlers) {
		h.Collections = v1.NewCollectionHandler(stubCollectionDB{}, slog.Default()).
			WithLibraryAccess(stubUpcomingAccess{}).
			WithFranchise(stubFranchiseDB{}, nil)
	})
}

func TestLibraryCollections_RequiresAuth(t *testing.T) {
	ts := newCollectionsServer(t)
	resp := ts.do("GET", "/api/v1/libraries/"+uuid.NewString()+"/collections", "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
}

func TestLibraryCollections_OpenToNonAdmin(t *testing.T) {
	ts := newCollectionsServer(t)
	resp := ts.do("GET", "/api/v1/libraries/"+uuid.NewString()+"/collections", ts.userToken(), nil)
	assertStatus(t, resp, http.StatusOK)
	var env struct {
		Data []struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			ItemCount int64  `json:"item_count"`
		} `json:"data"`
	}
	mustDecode(t, resp, &env)
	if len(env.Data) != 1 || env.Data[0].Type != "franchise" || env.Data[0].ItemCount != 2 {
		t.Fatalf("data = %+v", env.Data)
	}
}

func TestFranchiseCollection_DetailCarriesParts(t *testing.T) {
	ts := newCollectionsServer(t)
	resp := ts.do("GET", "/api/v1/collections/"+uatFranchiseCol.String(), ts.userToken(), nil)
	assertStatus(t, resp, http.StatusOK)
	var env struct {
		Data struct {
			Type  string `json:"type"`
			Parts []struct {
				TMDBID int     `json:"tmdb_id"`
				ItemID *string `json:"item_id"`
			} `json:"parts"`
		} `json:"data"`
	}
	mustDecode(t, resp, &env)
	if env.Data.Type != "franchise" || len(env.Data.Parts) != 2 || env.Data.Parts[0].ItemID != nil {
		t.Fatalf("detail = %+v", env.Data)
	}
}

func TestFranchiseCollection_DetailRequiresAuth(t *testing.T) {
	ts := newCollectionsServer(t)
	resp := ts.do("GET", "/api/v1/collections/"+uatFranchiseCol.String(), "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
}

// Franchise collections are rebuilt from TMDB on every sync, so even an
// admin's edit is refused rather than silently reverted later.
func TestFranchiseCollection_AdminEditsRejected(t *testing.T) {
	ts := newCollectionsServer(t)
	path := "/api/v1/collections/" + uatFranchiseCol.String()
	assertStatus(t, ts.do("PATCH", path, ts.adminToken(), map[string]any{"name": "Mine now"}), http.StatusConflict)
	assertStatus(t, ts.do("DELETE", path, ts.adminToken(), nil), http.StatusConflict)
	// Non-admins can't mutate a server-owned collection at all (404, as before).
	assertStatus(t, ts.do("DELETE", path, ts.userToken(), nil), http.StatusNotFound)
}
