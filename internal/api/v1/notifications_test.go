package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/notification"
)

// ── mock notification DB ────────────────────────────────────────────────────

type mockNotifDB struct {
	listRows   []gen.Notification
	listErr    error
	countVal   int64
	countErr   error
	markErr    error
	markAllErr error
}

func (m *mockNotifDB) ListNotifications(_ context.Context, _ gen.ListNotificationsParams) ([]gen.Notification, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.listRows, nil
}

func (m *mockNotifDB) CountUnreadNotifications(_ context.Context, _ uuid.UUID) (int64, error) {
	if m.countErr != nil {
		return 0, m.countErr
	}
	return m.countVal, nil
}

func (m *mockNotifDB) MarkNotificationRead(_ context.Context, _ gen.MarkNotificationReadParams) error {
	return m.markErr
}

func (m *mockNotifDB) MarkAllNotificationsRead(_ context.Context, _ uuid.UUID) error {
	return m.markAllErr
}

// ── helpers ─────────────────────────────────────────────────────────────────

func newNotifHandler(db *mockNotifDB) *NotificationHandler {
	broker := notification.NewBroker()
	return NewNotificationHandler(db, broker, slog.Default())
}

func notifAuthedRequest(r *http.Request) *http.Request {
	ctx := middleware.WithClaims(r.Context(), &auth.Claims{
		UserID:   uuid.New(),
		Username: "testuser",
	})
	return r.WithContext(ctx)
}

// ── List ────────────────────────────────────────────────────────────────────

func TestNotifications_List_Success(t *testing.T) {
	now := time.Now()
	nid := uuid.New()
	db := &mockNotifDB{
		listRows: []gen.Notification{
			{
				ID:        nid,
				Type:      "system",
				Title:     "Test",
				Body:      "Body",
				Read:      false,
				CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
			},
		},
	}
	h := newNotifHandler(db)

	rec := httptest.NewRecorder()
	req := notifAuthedRequest(httptest.NewRequest("GET", "/api/v1/notifications?limit=10&offset=0", nil))
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}

	var resp struct {
		Data []notificationResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("len: got %d, want 1", len(resp.Data))
	}
	if resp.Data[0].Title != "Test" {
		t.Errorf("title: got %q, want %q", resp.Data[0].Title, "Test")
	}
	if resp.Data[0].Read {
		t.Error("expected Read=false")
	}
}

func TestNotifications_List_NoAuth(t *testing.T) {
	h := newNotifHandler(&mockNotifDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/notifications", nil)
	h.List(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestNotifications_List_DBError(t *testing.T) {
	db := &mockNotifDB{listErr: errors.New("db down")}
	h := newNotifHandler(db)

	rec := httptest.NewRecorder()
	req := notifAuthedRequest(httptest.NewRequest("GET", "/api/v1/notifications", nil))
	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestNotifications_List_WithItemID(t *testing.T) {
	itemID := uuid.New()
	db := &mockNotifDB{
		listRows: []gen.Notification{
			{
				ID:        uuid.New(),
				Type:      "new_content",
				Title:     "New Movie",
				ItemID:    pgtype.UUID{Bytes: [16]byte(itemID), Valid: true},
				CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			},
		},
	}
	h := newNotifHandler(db)

	rec := httptest.NewRecorder()
	req := notifAuthedRequest(httptest.NewRequest("GET", "/api/v1/notifications", nil))
	h.List(rec, req)

	var resp struct {
		Data []notificationResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data[0].ItemID == nil {
		t.Fatal("expected item_id to be set")
	}
	if *resp.Data[0].ItemID != itemID.String() {
		t.Errorf("item_id: got %q, want %q", *resp.Data[0].ItemID, itemID.String())
	}
}

func TestNotifications_List_DefaultPagination(t *testing.T) {
	db := &mockNotifDB{listRows: []gen.Notification{}}
	h := newNotifHandler(db)

	rec := httptest.NewRecorder()
	// No limit/offset params — should use defaults.
	req := notifAuthedRequest(httptest.NewRequest("GET", "/api/v1/notifications", nil))
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

// ── UnreadCount ─────────────────────────────────────────────────────────────

func TestNotifications_UnreadCount_Success(t *testing.T) {
	db := &mockNotifDB{countVal: 5}
	h := newNotifHandler(db)

	rec := httptest.NewRecorder()
	req := notifAuthedRequest(httptest.NewRequest("GET", "/api/v1/notifications/unread-count", nil))
	h.UnreadCount(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}

	var resp struct {
		Data struct {
			Count int64 `json:"count"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Data.Count != 5 {
		t.Errorf("count: got %d, want 5", resp.Data.Count)
	}
}

func TestNotifications_UnreadCount_NoAuth(t *testing.T) {
	h := newNotifHandler(&mockNotifDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/notifications/unread-count", nil)
	h.UnreadCount(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// ── MarkRead ────────────────────────────────────────────────────────────────

func TestNotifications_MarkRead_Success(t *testing.T) {
	db := &mockNotifDB{}
	h := newNotifHandler(db)

	nid := uuid.New()
	rec := httptest.NewRecorder()
	req := notifAuthedRequest(httptest.NewRequest("POST", "/api/v1/notifications/"+nid.String()+"/read", nil))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", nid.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	h.MarkRead(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestNotifications_MarkRead_InvalidID(t *testing.T) {
	h := newNotifHandler(&mockNotifDB{})

	rec := httptest.NewRecorder()
	req := notifAuthedRequest(httptest.NewRequest("POST", "/", nil))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "not-a-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	h.MarkRead(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestNotifications_MarkRead_NoAuth(t *testing.T) {
	h := newNotifHandler(&mockNotifDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", uuid.New().String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	h.MarkRead(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// ── MarkAllRead ─────────────────────────────────────────────────────────────

func TestNotifications_MarkAllRead_Success(t *testing.T) {
	h := newNotifHandler(&mockNotifDB{})

	rec := httptest.NewRecorder()
	req := notifAuthedRequest(httptest.NewRequest("POST", "/api/v1/notifications/read-all", nil))
	h.MarkAllRead(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestNotifications_MarkAllRead_NoAuth(t *testing.T) {
	h := newNotifHandler(&mockNotifDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/notifications/read-all", nil)
	h.MarkAllRead(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestNotifications_MarkAllRead_DBError(t *testing.T) {
	db := &mockNotifDB{markAllErr: errors.New("db error")}
	h := newNotifHandler(db)

	rec := httptest.NewRecorder()
	req := notifAuthedRequest(httptest.NewRequest("POST", "/api/v1/notifications/read-all", nil))
	h.MarkAllRead(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
