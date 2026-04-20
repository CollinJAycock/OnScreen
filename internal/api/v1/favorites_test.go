package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── mock FavoritesDB ─────────────────────────────────────────────────────────

type mockFavoritesDB struct {
	addErr    error
	addCalled bool
	addArg    gen.AddFavoriteParams

	removeErr    error
	removeCalled bool
	removeArg    gen.RemoveFavoriteParams

	isFav    bool
	isFavErr error

	list    []gen.ListFavoritesRow
	listErr error
	listArg gen.ListFavoritesParams

	count    int64
	countErr error
}

func (m *mockFavoritesDB) AddFavorite(_ context.Context, arg gen.AddFavoriteParams) error {
	m.addCalled = true
	m.addArg = arg
	return m.addErr
}
func (m *mockFavoritesDB) RemoveFavorite(_ context.Context, arg gen.RemoveFavoriteParams) error {
	m.removeCalled = true
	m.removeArg = arg
	return m.removeErr
}
func (m *mockFavoritesDB) IsFavorite(_ context.Context, _ gen.IsFavoriteParams) (bool, error) {
	return m.isFav, m.isFavErr
}
func (m *mockFavoritesDB) ListFavorites(_ context.Context, arg gen.ListFavoritesParams) ([]gen.ListFavoritesRow, error) {
	m.listArg = arg
	return m.list, m.listErr
}
func (m *mockFavoritesDB) CountFavorites(_ context.Context, _ uuid.UUID) (int64, error) {
	return m.count, m.countErr
}

func favReqWithClaims(method, url string, uid uuid.UUID) *http.Request {
	req := httptest.NewRequest(method, url, nil)
	return req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uid}))
}

func favReqWithItem(method string, uid uuid.UUID, itemID uuid.UUID) *http.Request {
	req := favReqWithClaims(method, "/api/v1/items/"+itemID.String()+"/favorite", uid)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", itemID.String())
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestFavorites_List_RequiresAuth(t *testing.T) {
	db := &mockFavoritesDB{}
	h := NewFavoritesHandler(db, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/favorites", nil)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}

func TestFavorites_List_PassesUserIDToDB(t *testing.T) {
	uid := uuid.New()
	db := &mockFavoritesDB{list: []gen.ListFavoritesRow{}, count: 0}
	h := NewFavoritesHandler(db, slog.Default())

	req := favReqWithClaims(http.MethodGet, "/api/v1/favorites", uid)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if db.listArg.UserID != uid {
		t.Errorf("user id: got %s, want %s", db.listArg.UserID, uid)
	}
	if db.listArg.Limit != 50 || db.listArg.Offset != 0 {
		t.Errorf("defaults: got limit=%d offset=%d, want 50/0", db.listArg.Limit, db.listArg.Offset)
	}
}

func TestFavorites_List_ClampsLimitAndOffset(t *testing.T) {
	uid := uuid.New()
	db := &mockFavoritesDB{}
	h := NewFavoritesHandler(db, slog.Default())

	// limit > 200 is rejected, stays at default 50
	req := favReqWithClaims(http.MethodGet, "/api/v1/favorites?limit=500&offset=-5", uid)
	rec := httptest.NewRecorder()
	h.List(rec, req)

	if db.listArg.Limit != 50 {
		t.Errorf("limit out-of-range should not override default: got %d", db.listArg.Limit)
	}
	if db.listArg.Offset != 0 {
		t.Errorf("negative offset should not override default: got %d", db.listArg.Offset)
	}
}

func TestFavorites_Add_RequiresAuth(t *testing.T) {
	db := &mockFavoritesDB{}
	h := NewFavoritesHandler(db, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/items/"+uuid.New().String()+"/favorite", nil)
	rec := httptest.NewRecorder()
	h.Add(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if db.addCalled {
		t.Error("AddFavorite must not be called without claims")
	}
}

func TestFavorites_Add_InvalidItemID(t *testing.T) {
	db := &mockFavoritesDB{}
	h := NewFavoritesHandler(db, slog.Default())

	req := favReqWithClaims(http.MethodPost, "/api/v1/items/not-a-uuid/favorite", uuid.New())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "not-a-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.Add(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rec.Code)
	}
	if db.addCalled {
		t.Error("AddFavorite must not be called for invalid id")
	}
}

func TestFavorites_Add_Success(t *testing.T) {
	uid := uuid.New()
	itemID := uuid.New()
	db := &mockFavoritesDB{}
	h := NewFavoritesHandler(db, slog.Default())

	req := favReqWithItem(http.MethodPost, uid, itemID)
	rec := httptest.NewRecorder()
	h.Add(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", rec.Code)
	}
	if !db.addCalled {
		t.Fatal("expected AddFavorite to be called")
	}
	if db.addArg.UserID != uid || db.addArg.MediaID != itemID {
		t.Errorf("args: got {user=%s, media=%s}, want {user=%s, media=%s}",
			db.addArg.UserID, db.addArg.MediaID, uid, itemID)
	}
}

func TestFavorites_Remove_Success(t *testing.T) {
	uid := uuid.New()
	itemID := uuid.New()
	db := &mockFavoritesDB{}
	h := NewFavoritesHandler(db, slog.Default())

	req := favReqWithItem(http.MethodDelete, uid, itemID)
	rec := httptest.NewRecorder()
	h.Remove(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", rec.Code)
	}
	if !db.removeCalled {
		t.Fatal("expected RemoveFavorite to be called")
	}
	if db.removeArg.UserID != uid || db.removeArg.MediaID != itemID {
		t.Errorf("args mismatch: got %+v", db.removeArg)
	}
}

func TestFavorites_Remove_DBError(t *testing.T) {
	db := &mockFavoritesDB{removeErr: errors.New("db down")}
	h := NewFavoritesHandler(db, slog.Default())

	req := favReqWithItem(http.MethodDelete, uuid.New(), uuid.New())
	rec := httptest.NewRecorder()
	h.Remove(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rec.Code)
	}
}
