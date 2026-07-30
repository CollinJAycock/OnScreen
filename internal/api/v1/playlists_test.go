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
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── mock PlaylistDB ──────────────────────────────────────────────────────────

type mockPlaylistDB struct {
	mediaItem *gen.GetMediaItemRow // optional override for the AddItem ACL gate

	listMine    []gen.Collection
	listMineErr error
	listMineArg pgtype.UUID

	getResult gen.Collection
	getErr    error
	getArg    uuid.UUID

	createResult gen.Collection
	createErr    error
	createArg    gen.CreateCollectionParams

	updateResult gen.Collection
	updateErr    error
	updateArg    gen.UpdateCollectionParams

	deleteErr error
	deleteArg uuid.UUID

	smartItems []gen.ListMediaItemsForSmartPlaylistRow
	smartErr   error
	smartArg   gen.ListMediaItemsForSmartPlaylistParams

	listItems    []gen.ListCollectionItemsRow
	listItemsErr error

	addItemResult gen.CollectionItem
	addItemErr    error
	addItemArg    gen.AddCollectionItemParams

	removeItemErr error
	removeItemArg gen.RemoveCollectionItemParams

	reorderErr error
	reorderArg gen.ReorderPlaylistItemsParams
}

func (m *mockPlaylistDB) ListMyPlaylists(_ context.Context, userID pgtype.UUID) ([]gen.Collection, error) {
	m.listMineArg = userID
	return m.listMine, m.listMineErr
}
func (m *mockPlaylistDB) GetCollection(_ context.Context, id uuid.UUID) (gen.Collection, error) {
	m.getArg = id
	return m.getResult, m.getErr
}
func (m *mockPlaylistDB) CreateCollection(_ context.Context, arg gen.CreateCollectionParams) (gen.Collection, error) {
	m.createArg = arg
	return m.createResult, m.createErr
}
func (m *mockPlaylistDB) UpdateCollection(_ context.Context, arg gen.UpdateCollectionParams) (gen.Collection, error) {
	m.updateArg = arg
	return m.updateResult, m.updateErr
}
func (m *mockPlaylistDB) DeleteCollection(_ context.Context, id uuid.UUID) error {
	m.deleteArg = id
	return m.deleteErr
}
func (m *mockPlaylistDB) ListCollectionItems(_ context.Context, _ gen.ListCollectionItemsParams) ([]gen.ListCollectionItemsRow, error) {
	return m.listItems, m.listItemsErr
}

func (m *mockPlaylistDB) CountCollectionItems(_ context.Context, _ gen.CountCollectionItemsParams) (int64, error) {
	return int64(len(m.listItems)), nil
}

// GetMediaItem backs the library-ACL + content-rating gate AddItem now applies
// to the item being written. Returns a permissive default row; tests that care
// about the gate can set mediaItem.
func (m *mockPlaylistDB) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	if m.mediaItem != nil {
		return *m.mediaItem, nil
	}
	return gen.GetMediaItemRow{ID: id}, nil
}

func (m *mockPlaylistDB) AddCollectionItem(_ context.Context, arg gen.AddCollectionItemParams) (gen.CollectionItem, error) {
	m.addItemArg = arg
	return m.addItemResult, m.addItemErr
}
func (m *mockPlaylistDB) RemoveCollectionItem(_ context.Context, arg gen.RemoveCollectionItemParams) error {
	m.removeItemArg = arg
	return m.removeItemErr
}
func (m *mockPlaylistDB) ReorderPlaylistItems(_ context.Context, arg gen.ReorderPlaylistItemsParams) error {
	m.reorderArg = arg
	return m.reorderErr
}

// Smart-playlist evaluator hook. Tests that don't exercise the smart
// path (the v2.0 majority) leave smartItems nil and get an empty slice
// back, which the handler renders as an empty playlist.
func (m *mockPlaylistDB) ListMediaItemsForSmartPlaylist(_ context.Context, arg gen.ListMediaItemsForSmartPlaylistParams) ([]gen.ListMediaItemsForSmartPlaylistRow, error) {
	m.smartArg = arg
	return m.smartItems, m.smartErr
}

// ── helpers ──────────────────────────────────────────────────────────────────

func plReq(method, body string, uid uuid.UUID, idParam, itemParam string) *http.Request {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, "/", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, "/", nil)
	}
	if uid != uuid.Nil {
		r = r.WithContext(middleware.WithClaims(r.Context(), &auth.Claims{UserID: uid}))
	}
	rctx := chi.NewRouteContext()
	if idParam != "" {
		rctx.URLParams.Add("id", idParam)
	}
	if itemParam != "" {
		rctx.URLParams.Add("itemId", itemParam)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func ownedPlaylist(userID uuid.UUID) gen.Collection {
	return gen.Collection{
		ID:     uuid.New(),
		UserID: pgtype.UUID{Bytes: [16]byte(userID), Valid: true},
		Name:   "My List",
		Type:   "playlist",
	}
}

func newPlaylistHandler(db PlaylistDB) *PlaylistHandler {
	return NewPlaylistHandler(db, slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil)))
}

// ── List ─────────────────────────────────────────────────────────────────────

func TestPlaylists_List_RequiresAuth(t *testing.T) {
	h := newPlaylistHandler(&mockPlaylistDB{})
	req := plReq(http.MethodGet, "", uuid.Nil, "", "")
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

func TestPlaylists_List_ScopesToCaller(t *testing.T) {
	uid := uuid.New()
	db := &mockPlaylistDB{listMine: []gen.Collection{ownedPlaylist(uid)}}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodGet, "", uid, "", "")
	rec := httptest.NewRecorder()
	h.List(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !db.listMineArg.Valid || uuid.UUID(db.listMineArg.Bytes) != uid {
		t.Errorf("List did not pass caller uid: got %v", db.listMineArg)
	}
}

// ── Create ───────────────────────────────────────────────────────────────────

func TestPlaylists_Create_RequiresAuth(t *testing.T) {
	h := newPlaylistHandler(&mockPlaylistDB{})
	req := plReq(http.MethodPost, `{"name":"x"}`, uuid.Nil, "", "")
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}

func TestPlaylists_Create_RejectsEmptyName(t *testing.T) {
	h := newPlaylistHandler(&mockPlaylistDB{})
	for _, body := range []string{`{}`, `{"name":""}`, `{"name":"   "}`} {
		req := plReq(http.MethodPost, body, uuid.New(), "", "")
		rec := httptest.NewRecorder()
		h.Create(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: got %d, want 400", body, rec.Code)
		}
	}
}

func TestPlaylists_Create_SetsTypeAndOwner(t *testing.T) {
	uid := uuid.New()
	db := &mockPlaylistDB{createResult: ownedPlaylist(uid)}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodPost, `{"name":"Fresh"}`, uid, "", "")
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if db.createArg.Type != "playlist" {
		t.Errorf("type = %q, want playlist", db.createArg.Type)
	}
	if !db.createArg.UserID.Valid || uuid.UUID(db.createArg.UserID.Bytes) != uid {
		t.Errorf("owner not set to caller: got %v", db.createArg.UserID)
	}
	if db.createArg.Name != "Fresh" {
		t.Errorf("name = %q, want Fresh", db.createArg.Name)
	}
}

func TestPlaylists_Create_TrimsWhitespace(t *testing.T) {
	uid := uuid.New()
	db := &mockPlaylistDB{createResult: ownedPlaylist(uid)}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodPost, `{"name":"  spaced  "}`, uid, "", "")
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if db.createArg.Name != "spaced" {
		t.Errorf("name = %q, want trimmed 'spaced'", db.createArg.Name)
	}
}

// ── Update/Delete: ownership ────────────────────────────────────────────────

func TestPlaylists_Update_RejectsForeignOwner(t *testing.T) {
	owner := uuid.New()
	other := uuid.New()
	db := &mockPlaylistDB{getResult: ownedPlaylist(owner)}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodPatch, `{"name":"hax"}`, other, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404 (foreign-owned masked as not found)", rec.Code)
	}
	if db.updateArg.Name != "" {
		t.Error("Update was called despite ownership mismatch")
	}
}

func TestPlaylists_Update_RejectsNonPlaylistType(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	col.Type = "auto_genre" // not a playlist
	db := &mockPlaylistDB{getResult: col}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodPatch, `{"name":"hax"}`, uid, col.ID.String(), "")
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404 (type != playlist)", rec.Code)
	}
}

func TestPlaylists_Update_NotFoundWhenMissing(t *testing.T) {
	uid := uuid.New()
	db := &mockPlaylistDB{getErr: pgx.ErrNoRows}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodPatch, `{"name":"hax"}`, uid, uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestPlaylists_Update_AppliesRename(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	db := &mockPlaylistDB{getResult: col, updateResult: col}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodPatch, `{"name":"Renamed"}`, uid, col.ID.String(), "")
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if db.updateArg.Name != "Renamed" {
		t.Errorf("update name = %q, want Renamed", db.updateArg.Name)
	}
}

func TestPlaylists_Delete_RejectsForeignOwner(t *testing.T) {
	db := &mockPlaylistDB{getResult: ownedPlaylist(uuid.New())}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodDelete, "", uuid.New(), uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
	if db.deleteArg != uuid.Nil {
		t.Error("Delete was called despite ownership mismatch")
	}
}

func TestPlaylists_Delete_Success(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	db := &mockPlaylistDB{getResult: col}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodDelete, "", uid, col.ID.String(), "")
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("got %d, want 204", rec.Code)
	}
	if db.deleteArg != col.ID {
		t.Errorf("deleted %v, expected %v", db.deleteArg, col.ID)
	}
}

// ── Items / Add / Remove ─────────────────────────────────────────────────────

func TestPlaylists_Items_RejectsForeignOwner(t *testing.T) {
	db := &mockPlaylistDB{getResult: ownedPlaylist(uuid.New())}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodGet, "", uuid.New(), uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Items(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

type fakePlaylistAccess struct {
	allowed map[uuid.UUID]struct{}
	err     error
}

func (f *fakePlaylistAccess) CanAccessLibrary(_ context.Context, _, libID uuid.UUID, _ bool) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	_, ok := f.allowed[libID]
	return ok, nil
}
func (f *fakePlaylistAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, _ bool) (map[uuid.UUID]struct{}, error) {
	return f.allowed, f.err
}

func TestPlaylists_Items_FiltersByLibraryAccess(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	visibleLib := uuid.New()
	hiddenLib := uuid.New()
	db := &mockPlaylistDB{
		getResult: col,
		listItems: []gen.ListCollectionItemsRow{
			{ID: uuid.New(), LibraryID: visibleLib, Title: "Visible", Type: "movie", Position: 0},
			{ID: uuid.New(), LibraryID: hiddenLib, Title: "Hidden", Type: "movie", Position: 1},
		},
	}
	access := &fakePlaylistAccess{allowed: map[uuid.UUID]struct{}{visibleLib: {}}}
	h := newPlaylistHandler(db).WithLibraryAccess(access)
	req := plReq(http.MethodGet, "", uid, col.ID.String(), "")
	rec := httptest.NewRecorder()
	h.Items(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Visible") {
		t.Errorf("visible library item missing:\n%s", body)
	}
	if strings.Contains(body, "Hidden") {
		t.Errorf("hidden library item should have been filtered out:\n%s", body)
	}
}

func TestPlaylists_AddItem_RejectsBadBody(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	db := &mockPlaylistDB{getResult: col}
	h := newPlaylistHandler(db)
	for _, body := range []string{`not json`, `{}`, `{"media_item_id":"not-a-uuid"}`} {
		req := plReq(http.MethodPost, body, uid, col.ID.String(), "")
		rec := httptest.NewRecorder()
		h.AddItem(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: got %d, want 400", body, rec.Code)
		}
	}
}

func TestPlaylists_AddItem_PassesBothIDs(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	itemID := uuid.New()
	db := &mockPlaylistDB{getResult: col}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodPost, `{"media_item_id":"`+itemID.String()+`"}`, uid, col.ID.String(), "")
	rec := httptest.NewRecorder()
	h.AddItem(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if db.addItemArg.CollectionID != col.ID || db.addItemArg.MediaItemID != itemID {
		t.Errorf("AddItem args wrong: %+v", db.addItemArg)
	}
}

func TestPlaylists_AddItem_RejectsForeignOwner(t *testing.T) {
	db := &mockPlaylistDB{getResult: ownedPlaylist(uuid.New())}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodPost, `{"media_item_id":"`+uuid.NewString()+`"}`, uuid.New(), uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.AddItem(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
	if db.addItemArg.MediaItemID != uuid.Nil {
		t.Error("AddItem was called despite ownership mismatch")
	}
}

func TestPlaylists_RemoveItem_RejectsForeignOwner(t *testing.T) {
	db := &mockPlaylistDB{getResult: ownedPlaylist(uuid.New())}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodDelete, "", uuid.New(), uuid.New().String(), uuid.New().String())
	rec := httptest.NewRecorder()
	h.RemoveItem(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
}

func TestPlaylists_RemoveItem_Success(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	itemID := uuid.New()
	db := &mockPlaylistDB{getResult: col}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodDelete, "", uid, col.ID.String(), itemID.String())
	rec := httptest.NewRecorder()
	h.RemoveItem(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("got %d, want 204", rec.Code)
	}
	if db.removeItemArg.MediaItemID != itemID {
		t.Errorf("removed %v, expected %v", db.removeItemArg.MediaItemID, itemID)
	}
}

// ── Reorder ──────────────────────────────────────────────────────────────────

func TestPlaylists_Reorder_RejectsForeignOwner(t *testing.T) {
	db := &mockPlaylistDB{getResult: ownedPlaylist(uuid.New())}
	h := newPlaylistHandler(db)
	body := `{"item_ids":["` + uuid.NewString() + `"]}`
	req := plReq(http.MethodPut, body, uuid.New(), uuid.New().String(), "")
	rec := httptest.NewRecorder()
	h.Reorder(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
	if db.reorderArg.CollectionID != uuid.Nil {
		t.Error("Reorder was called despite ownership mismatch")
	}
}

func TestPlaylists_Reorder_RejectsDuplicates(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	db := &mockPlaylistDB{getResult: col}
	h := newPlaylistHandler(db)
	dup := uuid.NewString()
	body := `{"item_ids":["` + dup + `","` + dup + `"]}`
	req := plReq(http.MethodPut, body, uid, col.ID.String(), "")
	rec := httptest.NewRecorder()
	h.Reorder(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
	if db.reorderArg.CollectionID != uuid.Nil {
		t.Error("reorder should not have been called")
	}
}

func TestPlaylists_Reorder_RejectsBadUUID(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	db := &mockPlaylistDB{getResult: col}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodPut, `{"item_ids":["not-a-uuid"]}`, uid, col.ID.String(), "")
	rec := httptest.NewRecorder()
	h.Reorder(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestPlaylists_Reorder_PassesIDsInOrder(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	db := &mockPlaylistDB{getResult: col}
	h := newPlaylistHandler(db)
	body := `{"item_ids":["` + a.String() + `","` + b.String() + `","` + c.String() + `"]}`
	req := plReq(http.MethodPut, body, uid, col.ID.String(), "")
	rec := httptest.NewRecorder()
	h.Reorder(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if db.reorderArg.CollectionID != col.ID {
		t.Errorf("collection id = %v, want %v", db.reorderArg.CollectionID, col.ID)
	}
	got := db.reorderArg.ItemIds
	if len(got) != 3 || got[0] != a || got[1] != b || got[2] != c {
		t.Errorf("ids passed out of order: %+v", got)
	}
}

func TestPlaylists_Reorder_EmptyArrayIsNoop(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	db := &mockPlaylistDB{getResult: col}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodPut, `{"item_ids":[]}`, uid, col.ID.String(), "")
	rec := httptest.NewRecorder()
	h.Reorder(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("got %d, want 204 (empty reorder is allowed)", rec.Code)
	}
	if len(db.reorderArg.ItemIds) != 0 {
		t.Errorf("ids = %+v, want empty", db.reorderArg.ItemIds)
	}
}

// Sanity: /playlists/{id}/* routes reject malformed UUIDs with 400 before any DB call.
func TestPlaylists_Mutations_RejectBadUUIDs(t *testing.T) {
	db := &mockPlaylistDB{getResult: ownedPlaylist(uuid.New())}
	h := newPlaylistHandler(db)

	cases := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
	}{
		{"Update", h.Update},
		{"Delete", h.Delete},
		{"Items", h.Items},
		{"AddItem", h.AddItem},
		{"RemoveItem", h.RemoveItem},
		{"Reorder", h.Reorder},
	}
	for _, c := range cases {
		req := plReq(http.MethodGet, `{"name":"x","media_item_id":"`+uuid.NewString()+`","item_ids":[]}`, uuid.New(), "not-a-uuid", uuid.New().String())
		rec := httptest.NewRecorder()
		c.fn(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", c.name, rec.Code)
		}
	}
}

// Sanity: all write endpoints surface a 500 when DB blows up mid-call.
func TestPlaylists_DBErrorBecomes500(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	db := &mockPlaylistDB{
		getResult:    col,
		createErr:    errors.New("boom"),
		updateErr:    errors.New("boom"),
		addItemErr:   errors.New("boom"),
		reorderErr:   errors.New("boom"),
		listItemsErr: errors.New("boom"),
		listMineErr:  errors.New("boom"),
		deleteErr:    errors.New("boom"),
	}
	h := newPlaylistHandler(db)

	// Create
	rec := httptest.NewRecorder()
	h.Create(rec, plReq(http.MethodPost, `{"name":"x"}`, uid, "", ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Create: got %d, want 500", rec.Code)
	}
	// List
	rec = httptest.NewRecorder()
	h.List(rec, plReq(http.MethodGet, "", uid, "", ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("List: got %d, want 500", rec.Code)
	}
	// Update
	rec = httptest.NewRecorder()
	h.Update(rec, plReq(http.MethodPatch, `{"name":"x"}`, uid, col.ID.String(), ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Update: got %d, want 500", rec.Code)
	}
	// Delete
	rec = httptest.NewRecorder()
	h.Delete(rec, plReq(http.MethodDelete, "", uid, col.ID.String(), ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Delete: got %d, want 500", rec.Code)
	}
	// Items
	rec = httptest.NewRecorder()
	h.Items(rec, plReq(http.MethodGet, "", uid, col.ID.String(), ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Items: got %d, want 500", rec.Code)
	}
	// AddItem
	rec = httptest.NewRecorder()
	h.AddItem(rec, plReq(http.MethodPost, `{"media_item_id":"`+uuid.NewString()+`"}`, uid, col.ID.String(), ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("AddItem: got %d, want 500", rec.Code)
	}
	// Reorder
	rec = httptest.NewRecorder()
	h.Reorder(rec, plReq(http.MethodPut, `{"item_ids":["`+uuid.NewString()+`"]}`, uid, col.ID.String(), ""))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("Reorder: got %d, want 500", rec.Code)
	}
}

// Defensive: make sure response parses and carries the right fields.
func TestPlaylists_Create_ResponseFields(t *testing.T) {
	uid := uuid.New()
	db := &mockPlaylistDB{createResult: ownedPlaylist(uid)}
	h := newPlaylistHandler(db)
	req := plReq(http.MethodPost, `{"name":"x"}`, uid, "", "")
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	var out struct {
		Data playlistResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response parse: %v body=%s", err, rec.Body.String())
	}
	if out.Data.ID == "" || out.Data.Name == "" {
		t.Errorf("response missing fields: %+v", out.Data)
	}
}

// ── AddItem access gate ──────────────────────────────────────────────────────

// plRatedReq is plReq with a content-rating ceiling on the claims.
func plRatedReq(body string, uid uuid.UUID, idParam, maxRating string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(middleware.WithClaims(r.Context(),
		&auth.Claims{UserID: uid, MaxContentRating: maxRating}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", idParam)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// The read path filters a playlist by library ACL, but the write path used to
// accept any item id at all. A restricted profile could add an item from a
// library it cannot see to its own playlist and read the title, poster and
// metadata straight back out of its own list.
func TestPlaylists_AddItem_RejectsItemFromInvisibleLibrary(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	hiddenLib := uuid.New()
	db := &mockPlaylistDB{
		getResult: col,
		mediaItem: &gen.GetMediaItemRow{ID: uuid.New(), LibraryID: hiddenLib},
	}
	access := &fakePlaylistAccess{allowed: map[uuid.UUID]struct{}{uuid.New(): {}}}
	h := newPlaylistHandler(db).WithLibraryAccess(access)

	rec := httptest.NewRecorder()
	h.AddItem(rec, plReq(http.MethodPost,
		`{"media_item_id":"`+uuid.NewString()+`"}`, uid, col.ID.String(), ""))

	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404 (fail-closed, not an existence oracle)", rec.Code)
	}
	if db.addItemArg.MediaItemID != uuid.Nil {
		t.Error("item from an invisible library was written to the playlist")
	}
}

// Same gate, content-rating axis: a ceiling must block the write, not just the
// read.
func TestPlaylists_AddItem_RejectsItemAboveRatingCeiling(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	lib := uuid.New()
	rating := "R"
	db := &mockPlaylistDB{
		getResult: col,
		mediaItem: &gen.GetMediaItemRow{ID: uuid.New(), LibraryID: lib, ContentRating: &rating},
	}
	access := &fakePlaylistAccess{allowed: map[uuid.UUID]struct{}{lib: {}}}
	h := newPlaylistHandler(db).WithLibraryAccess(access)

	rec := httptest.NewRecorder()
	h.AddItem(rec, plRatedReq(
		`{"media_item_id":"`+uuid.NewString()+`"}`, uid, col.ID.String(), "PG"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rec.Code)
	}
	if db.addItemArg.MediaItemID != uuid.Nil {
		t.Error("an R-rated item was added to a PG-restricted profile's playlist")
	}
}

// An unrated item must be gated the same way the streaming path gates it:
// contentrating ranks an absent rating as 4, so a PG ceiling blocks it. An
// open-coded "no rating means unrestricted" check would let it through here and
// then refuse to play it.
func TestPlaylists_AddItem_UnratedItemFollowsIsAllowed(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	lib := uuid.New()
	db := &mockPlaylistDB{
		getResult: col,
		mediaItem: &gen.GetMediaItemRow{ID: uuid.New(), LibraryID: lib}, // ContentRating nil
	}
	access := &fakePlaylistAccess{allowed: map[uuid.UUID]struct{}{lib: {}}}
	h := newPlaylistHandler(db).WithLibraryAccess(access)

	rec := httptest.NewRecorder()
	h.AddItem(rec, plRatedReq(
		`{"media_item_id":"`+uuid.NewString()+`"}`, uid, col.ID.String(), "PG"))

	allowedByStreamPath := contentrating.IsAllowed("", "PG")
	added := db.addItemArg.MediaItemID != uuid.Nil
	if added != allowedByStreamPath {
		t.Errorf("unrated item added=%v but the streaming path allows=%v — "+
			"the list-add gate and the play gate disagree", added, allowedByStreamPath)
	}
}

// A permitted item must still go through.
func TestPlaylists_AddItem_AllowsVisibleInCeilingItem(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	lib := uuid.New()
	rating := "G"
	itemID := uuid.New()
	db := &mockPlaylistDB{
		getResult: col,
		mediaItem: &gen.GetMediaItemRow{ID: itemID, LibraryID: lib, ContentRating: &rating},
	}
	access := &fakePlaylistAccess{allowed: map[uuid.UUID]struct{}{lib: {}}}
	h := newPlaylistHandler(db).WithLibraryAccess(access)

	rec := httptest.NewRecorder()
	h.AddItem(rec, plRatedReq(
		`{"media_item_id":"`+itemID.String()+`"}`, uid, col.ID.String(), "PG"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if db.addItemArg.MediaItemID != itemID {
		t.Error("a permitted item was not added")
	}
}

// ── smart playlists ─────────────────────────────────────────────────────────

// Create stores type='smart_playlist' as soon as a `rules` body is supplied,
// and ListMyPlaylists selects both types — but loadOwned only accepted
// "playlist", so a smart playlist appeared in the list and then 404'd on every
// sub-route. It could be created and seen and nothing else.
func TestPlaylists_SmartPlaylistIsReachable(t *testing.T) {
	uid := uuid.New()
	col := ownedPlaylist(uid)
	col.Type = "smart_playlist"
	db := &mockPlaylistDB{getResult: col}
	h := newPlaylistHandler(db)

	rec := httptest.NewRecorder()
	h.Items(rec, plReq(http.MethodGet, "", uid, col.ID.String(), ""))

	if rec.Code == http.StatusNotFound {
		t.Fatal("a smart playlist 404s on its own items route; every sub-route " +
			"(rename, delete, add/remove item, reorder) is unreachable")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// Collection types this handler does NOT own must still be hidden — the fix
// widens the set by exactly one type, it does not open the door.
func TestPlaylists_ForeignCollectionTypesStill404(t *testing.T) {
	for _, typ := range []string{"collection", "photo_album", "", "smart_collection"} {
		t.Run(typ, func(t *testing.T) {
			uid := uuid.New()
			col := ownedPlaylist(uid)
			col.Type = typ
			h := newPlaylistHandler(&mockPlaylistDB{getResult: col})
			rec := httptest.NewRecorder()
			h.Items(rec, plReq(http.MethodGet, "", uid, col.ID.String(), ""))
			if rec.Code != http.StatusNotFound {
				t.Errorf("type %q: got %d, want 404", typ, rec.Code)
			}
		})
	}
}

// Ownership still governs both types.
func TestPlaylists_SmartPlaylistStillOwnerScoped(t *testing.T) {
	col := ownedPlaylist(uuid.New()) // owned by someone else
	col.Type = "smart_playlist"
	h := newPlaylistHandler(&mockPlaylistDB{getResult: col})
	rec := httptest.NewRecorder()
	h.Items(rec, plReq(http.MethodGet, "", uuid.New(), col.ID.String(), ""))
	if rec.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404 for a foreign-owned smart playlist", rec.Code)
	}
}
