package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── mock PhotoAlbumDB ────────────────────────────────────────────────────────

type mockPhotoAlbumDB struct {
	listMine    []gen.ListMyPhotoAlbumsRow
	listMineErr error
	listMineArg pgtype.UUID

	listItems    []gen.ListPhotoAlbumItemsRow
	listItemsErr error

	getResult gen.Collection
	getErr    error

	createResult gen.Collection
	createErr    error
	createArg    gen.CreateCollectionParams

	updateResult gen.Collection
	updateErr    error
	updateArg    gen.UpdateCollectionParams

	deleteErr error
	deleteArg uuid.UUID

	addItemArg gen.AddCollectionItemParams
	addItemErr error

	removeItemArg gen.RemoveCollectionItemParams
	removeItemErr error

	getMediaItem    gen.GetMediaItemRow
	getMediaItemErr error
}

func (m *mockPhotoAlbumDB) ListMyPhotoAlbums(_ context.Context, userID pgtype.UUID) ([]gen.ListMyPhotoAlbumsRow, error) {
	m.listMineArg = userID
	return m.listMine, m.listMineErr
}
func (m *mockPhotoAlbumDB) ListPhotoAlbumItems(_ context.Context, _ uuid.UUID) ([]gen.ListPhotoAlbumItemsRow, error) {
	return m.listItems, m.listItemsErr
}
func (m *mockPhotoAlbumDB) GetCollection(_ context.Context, _ uuid.UUID) (gen.Collection, error) {
	return m.getResult, m.getErr
}
func (m *mockPhotoAlbumDB) CreateCollection(_ context.Context, arg gen.CreateCollectionParams) (gen.Collection, error) {
	m.createArg = arg
	return m.createResult, m.createErr
}
func (m *mockPhotoAlbumDB) UpdateCollection(_ context.Context, arg gen.UpdateCollectionParams) (gen.Collection, error) {
	m.updateArg = arg
	return m.updateResult, m.updateErr
}
func (m *mockPhotoAlbumDB) DeleteCollection(_ context.Context, id uuid.UUID) error {
	m.deleteArg = id
	return m.deleteErr
}
func (m *mockPhotoAlbumDB) AddCollectionItem(_ context.Context, arg gen.AddCollectionItemParams) (gen.CollectionItem, error) {
	m.addItemArg = arg
	return gen.CollectionItem{}, m.addItemErr
}
func (m *mockPhotoAlbumDB) RemoveCollectionItem(_ context.Context, arg gen.RemoveCollectionItemParams) error {
	m.removeItemArg = arg
	return m.removeItemErr
}
func (m *mockPhotoAlbumDB) GetMediaItem(_ context.Context, _ uuid.UUID) (gen.GetMediaItemRow, error) {
	return m.getMediaItem, m.getMediaItemErr
}

// ── helpers ──────────────────────────────────────────────────────────────────

// withUser stamps the request context with claims for `uid` so handlers
// that gate on ClaimsFromContext don't reject it as unauthenticated.
func withUser(req *http.Request, uid uuid.UUID) *http.Request {
	ctx := middleware.WithClaims(req.Context(), &auth.Claims{UserID: uid})
	return req.WithContext(ctx)
}

// withChiParams installs every kv pair into a single chi RouteContext.
// withChiParam (in items_test.go) creates a fresh context per call and
// silently drops earlier params — fine for one-param routes, broken for
// the album-item routes which need both {id} and {itemId}.
func withChiParams(r *http.Request, kv ...string) *http.Request {
	rctx := chi.NewRouteContext()
	for i := 0; i+1 < len(kv); i += 2 {
		rctx.URLParams.Add(kv[i], kv[i+1])
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// ownedAlbum returns a Collection row that loadOwned will accept as
// belonging to `uid`. albumID becomes the {id} URL param.
func ownedAlbum(uid, albumID uuid.UUID) gen.Collection {
	return gen.Collection{
		ID:     albumID,
		UserID: pgtype.UUID{Bytes: [16]byte(uid), Valid: true},
		Type:   "photo_album",
		Name:   "Vacation 2024",
	}
}

// ── List ─────────────────────────────────────────────────────────────────────

func TestPhotoAlbums_List_RequiresAuth(t *testing.T) {
	h := NewPhotoAlbumHandler(&mockPhotoAlbumDB{}, slog.Default())
	req := httptest.NewRequest("GET", "/api/v1/photo-albums", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestPhotoAlbums_List_ReturnsAlbums(t *testing.T) {
	uid := uuid.New()
	cover := "/art/abc.jpg"
	desc := "Trip to Italy"
	m := &mockPhotoAlbumDB{
		listMine: []gen.ListMyPhotoAlbumsRow{
			{ID: uuid.New(), Name: "Italy", Description: &desc, CoverPath: &cover, ItemCount: 42},
			{ID: uuid.New(), Name: "Empty Album", ItemCount: 0},
		},
	}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("GET", "/api/v1/photo-albums", nil), uid)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []photoAlbumResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("data len: got %d, want 2", len(resp.Data))
	}
	if resp.Data[0].ItemCount != 42 || resp.Data[0].CoverPath == nil || *resp.Data[0].CoverPath != cover {
		t.Errorf("first album: %+v", resp.Data[0])
	}
	// Verify the user filter was passed through.
	if !m.listMineArg.Valid || uuid.UUID(m.listMineArg.Bytes) != uid {
		t.Errorf("user filter not passed through; got %v", m.listMineArg)
	}
}

// ── Create ───────────────────────────────────────────────────────────────────

func TestPhotoAlbums_Create_RequiresName(t *testing.T) {
	uid := uuid.New()
	h := NewPhotoAlbumHandler(&mockPhotoAlbumDB{}, slog.Default())
	req := withUser(httptest.NewRequest("POST", "/api/v1/photo-albums", strings.NewReader(`{"name":"   "}`)), uid)
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("blank name should 400; got %d", rec.Code)
	}
}

func TestPhotoAlbums_Create_PersistsAsPhotoAlbum(t *testing.T) {
	uid := uuid.New()
	created := uuid.New()
	m := &mockPhotoAlbumDB{
		createResult: gen.Collection{ID: created, Name: "Italy", Type: "photo_album"},
	}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("POST", "/api/v1/photo-albums",
		bytes.NewReader([]byte(`{"name":"Italy"}`))), uid)
	rec := httptest.NewRecorder()
	h.Create(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if m.createArg.Type != "photo_album" {
		t.Errorf("type: got %q, want photo_album", m.createArg.Type)
	}
	if m.createArg.Name != "Italy" {
		t.Errorf("name: got %q, want Italy", m.createArg.Name)
	}
	if !m.createArg.UserID.Valid || uuid.UUID(m.createArg.UserID.Bytes) != uid {
		t.Errorf("user: got %v, want %s", m.createArg.UserID, uid)
	}
}

func TestPhotoAlbums_Create_RequiresAuth(t *testing.T) {
	h := NewPhotoAlbumHandler(&mockPhotoAlbumDB{}, slog.Default())
	req := httptest.NewRequest("POST", "/api/v1/photo-albums",
		bytes.NewReader([]byte(`{"name":"Italy"}`)))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

// ── Update ───────────────────────────────────────────────────────────────────

func TestPhotoAlbums_Update_RenamesAndPersists(t *testing.T) {
	uid := uuid.New()
	albumID := uuid.New()
	m := &mockPhotoAlbumDB{
		getResult:    ownedAlbum(uid, albumID),
		updateResult: gen.Collection{ID: albumID, Name: "New Name", Type: "photo_album"},
	}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("PATCH", "/",
		bytes.NewReader([]byte(`{"name":"New Name"}`))), uid)
	req = withChiParam(req, "id", albumID.String())
	rec := httptest.NewRecorder()
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if m.updateArg.Name != "New Name" {
		t.Errorf("name not passed through: %q", m.updateArg.Name)
	}
}

func TestPhotoAlbums_Update_BlankNameKeepsOriginal(t *testing.T) {
	uid := uuid.New()
	albumID := uuid.New()
	current := ownedAlbum(uid, albumID)
	current.Name = "Original"
	m := &mockPhotoAlbumDB{
		getResult:    current,
		updateResult: current,
	}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("PATCH", "/",
		bytes.NewReader([]byte(`{"name":"   "}`))), uid)
	req = withChiParam(req, "id", albumID.String())
	rec := httptest.NewRecorder()
	h.Update(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if m.updateArg.Name != "Original" {
		t.Errorf("blank name should preserve original; got %q", m.updateArg.Name)
	}
}

// ── Delete ───────────────────────────────────────────────────────────────────

func TestPhotoAlbums_Delete_Success(t *testing.T) {
	uid := uuid.New()
	albumID := uuid.New()
	m := &mockPhotoAlbumDB{getResult: ownedAlbum(uid, albumID)}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("DELETE", "/", nil), uid)
	req = withChiParam(req, "id", albumID.String())
	rec := httptest.NewRecorder()
	h.Delete(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if m.deleteArg != albumID {
		t.Errorf("delete arg: got %s, want %s", m.deleteArg, albumID)
	}
}

// ── Ownership enforcement ────────────────────────────────────────────────────

func TestPhotoAlbums_LoadOwned_404OnForeignAlbum(t *testing.T) {
	uid := uuid.New()
	otherUser := uuid.New()
	albumID := uuid.New()
	m := &mockPhotoAlbumDB{
		getResult: gen.Collection{
			ID:     albumID,
			UserID: pgtype.UUID{Bytes: [16]byte(otherUser), Valid: true},
			Type:   "photo_album",
		},
	}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("DELETE", "/", nil), uid)
	req = withChiParam(req, "id", albumID.String())
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("foreign album should 404; got %d", rec.Code)
	}
}

func TestPhotoAlbums_LoadOwned_404OnPlaylistType(t *testing.T) {
	uid := uuid.New()
	albumID := uuid.New()
	m := &mockPhotoAlbumDB{
		getResult: gen.Collection{
			ID:     albumID,
			UserID: pgtype.UUID{Bytes: [16]byte(uid), Valid: true},
			Type:   "playlist", // wrong type — handler must reject as 404
		},
	}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("DELETE", "/", nil), uid)
	req = withChiParam(req, "id", albumID.String())
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("non-album type should 404; got %d", rec.Code)
	}
}

func TestPhotoAlbums_LoadOwned_404OnNotFound(t *testing.T) {
	uid := uuid.New()
	m := &mockPhotoAlbumDB{getErr: pgx.ErrNoRows}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("DELETE", "/", nil), uid)
	req = withChiParam(req, "id", uuid.New().String())
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing album should 404; got %d", rec.Code)
	}
}

// ── AddItem ──────────────────────────────────────────────────────────────────

func TestPhotoAlbums_AddItem_RejectsNonPhoto(t *testing.T) {
	uid := uuid.New()
	albumID := uuid.New()
	mediaID := uuid.New()
	m := &mockPhotoAlbumDB{
		getResult:    ownedAlbum(uid, albumID),
		getMediaItem: gen.GetMediaItemRow{ID: mediaID, Type: "movie"}, // not a photo
	}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("POST", "/",
		bytes.NewReader([]byte(`{"media_item_id":"`+mediaID.String()+`"}`))), uid)
	req = withChiParam(req, "id", albumID.String())
	rec := httptest.NewRecorder()
	h.AddItem(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("non-photo add should 400; got %d, body=%s", rec.Code, rec.Body.String())
	}
	if m.addItemArg.MediaItemID == mediaID {
		t.Error("AddCollectionItem should NOT have been called for non-photo item")
	}
}

func TestPhotoAlbums_AddItem_AcceptsPhoto(t *testing.T) {
	uid := uuid.New()
	albumID := uuid.New()
	mediaID := uuid.New()
	m := &mockPhotoAlbumDB{
		getResult:    ownedAlbum(uid, albumID),
		getMediaItem: gen.GetMediaItemRow{ID: mediaID, Type: "photo"},
	}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("POST", "/",
		bytes.NewReader([]byte(`{"media_item_id":"`+mediaID.String()+`"}`))), uid)
	req = withChiParam(req, "id", albumID.String())
	rec := httptest.NewRecorder()
	h.AddItem(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	if m.addItemArg.CollectionID != albumID || m.addItemArg.MediaItemID != mediaID {
		t.Errorf("add args wrong: %+v", m.addItemArg)
	}
}

func TestPhotoAlbums_AddItem_404OnMissingMediaItem(t *testing.T) {
	uid := uuid.New()
	albumID := uuid.New()
	mediaID := uuid.New()
	m := &mockPhotoAlbumDB{
		getResult:       ownedAlbum(uid, albumID),
		getMediaItemErr: pgx.ErrNoRows,
	}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("POST", "/",
		bytes.NewReader([]byte(`{"media_item_id":"`+mediaID.String()+`"}`))), uid)
	req = withChiParam(req, "id", albumID.String())
	rec := httptest.NewRecorder()
	h.AddItem(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("missing media item should 404; got %d", rec.Code)
	}
}

func TestPhotoAlbums_AddItem_BadUUID(t *testing.T) {
	uid := uuid.New()
	albumID := uuid.New()
	m := &mockPhotoAlbumDB{getResult: ownedAlbum(uid, albumID)}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("POST", "/",
		bytes.NewReader([]byte(`{"media_item_id":"not-a-uuid"}`))), uid)
	req = withChiParam(req, "id", albumID.String())
	rec := httptest.NewRecorder()
	h.AddItem(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad uuid should 400; got %d", rec.Code)
	}
}

// ── RemoveItem ───────────────────────────────────────────────────────────────

func TestPhotoAlbums_RemoveItem_PassesThroughToCollections(t *testing.T) {
	uid := uuid.New()
	albumID := uuid.New()
	mediaID := uuid.New()
	m := &mockPhotoAlbumDB{getResult: ownedAlbum(uid, albumID)}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("DELETE", "/", nil), uid)
	req = withChiParams(req, "id", albumID.String(), "itemId", mediaID.String())
	rec := httptest.NewRecorder()
	h.RemoveItem(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d", rec.Code)
	}
	if m.removeItemArg.CollectionID != albumID || m.removeItemArg.MediaItemID != mediaID {
		t.Errorf("remove args: %+v", m.removeItemArg)
	}
}

func TestPhotoAlbums_RemoveItem_404OnDBError(t *testing.T) {
	uid := uuid.New()
	albumID := uuid.New()
	mediaID := uuid.New()
	m := &mockPhotoAlbumDB{
		getResult:     ownedAlbum(uid, albumID),
		removeItemErr: errors.New("not found"),
	}
	h := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("DELETE", "/", nil), uid)
	req = withChiParams(req, "id", albumID.String(), "itemId", mediaID.String())
	rec := httptest.NewRecorder()
	h.RemoveItem(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
}

// ── Items ────────────────────────────────────────────────────────────────────

func TestPhotoAlbums_Items_ReturnsPhotos(t *testing.T) {
	uid := uuid.New()
	albumID := uuid.New()
	libID := uuid.New()
	w, h := int32(800), int32(600)
	m := &mockPhotoAlbumDB{
		getResult: ownedAlbum(uid, albumID),
		listItems: []gen.ListPhotoAlbumItemsRow{
			{ID: uuid.New(), LibraryID: libID, Title: "IMG_001", Width: &w, Height: &h},
			{ID: uuid.New(), LibraryID: libID, Title: "IMG_002"},
		},
	}
	hh := NewPhotoAlbumHandler(m, slog.Default())
	req := withUser(httptest.NewRequest("GET", "/", nil), uid)
	req = withChiParam(req, "id", albumID.String())
	rec := httptest.NewRecorder()
	hh.Items(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data []photoAlbumItemResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("items: got %d, want 2", len(resp.Data))
	}
	if resp.Data[0].Width == nil || *resp.Data[0].Width != 800 {
		t.Errorf("width passthrough: %+v", resp.Data[0])
	}
}

func TestPhotoAlbums_Items_FilteredByLibraryAccess(t *testing.T) {
	uid := uuid.New()
	albumID := uuid.New()
	allowedLib := uuid.New()
	deniedLib := uuid.New()
	m := &mockPhotoAlbumDB{
		getResult: ownedAlbum(uid, albumID),
		listItems: []gen.ListPhotoAlbumItemsRow{
			{ID: uuid.New(), LibraryID: allowedLib, Title: "visible"},
			{ID: uuid.New(), LibraryID: deniedLib, Title: "filtered"},
		},
	}
	allowed := map[uuid.UUID]struct{}{allowedLib: {}}
	hh := NewPhotoAlbumHandler(m, slog.Default()).
		WithLibraryAccess(&mockAlbumLibraryAccess{allowed: allowed})

	req := withUser(httptest.NewRequest("GET", "/", nil), uid)
	req = withChiParam(req, "id", albumID.String())
	rec := httptest.NewRecorder()
	hh.Items(rec, req)

	var resp struct {
		Data []photoAlbumItemResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 visible item after ACL filter; got %d", len(resp.Data))
	}
	if resp.Data[0].LibraryID != allowedLib.String() {
		t.Errorf("wrong item survived filter: %+v", resp.Data[0])
	}
}

// mockAlbumLibraryAccess is a LibraryAccessChecker that always returns the
// fixed allow-set without consulting any backing store.
type mockAlbumLibraryAccess struct {
	allowed map[uuid.UUID]struct{}
}

func (m *mockAlbumLibraryAccess) CanAccessLibrary(_ context.Context, _ uuid.UUID, lib uuid.UUID, _ bool) (bool, error) {
	_, ok := m.allowed[lib]
	return ok, nil
}

func (m *mockAlbumLibraryAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, _ bool) (map[uuid.UUID]struct{}, error) {
	return m.allowed, nil
}
