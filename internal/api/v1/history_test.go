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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── mock history DB ─────────────────────────────────────────────────────────

type mockHistoryDB struct {
	rows []gen.ListWatchHistoryRow
	err  error
}

func (m *mockHistoryDB) ListWatchHistory(_ context.Context, _ gen.ListWatchHistoryParams) ([]gen.ListWatchHistoryRow, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.rows, nil
}

func newHistoryHandler(db *mockHistoryDB) *HistoryHandler {
	return NewHistoryHandler(db, slog.Default())
}

func historyAuthedRequest(r *http.Request) *http.Request {
	ctx := middleware.WithClaims(r.Context(), &auth.Claims{
		UserID:   uuid.New(),
		Username: "testuser",
	})
	return r.WithContext(ctx)
}

// ── List tests ──────────────────────────────────────────────────────────────

func TestHistory_List_Success(t *testing.T) {
	now := time.Now()
	dur := int64(7200000)
	db := &mockHistoryDB{
		rows: []gen.ListWatchHistoryRow{
			{
				ID:         uuid.New(),
				UserID:     uuid.New(),
				MediaID:    uuid.New(),
				MediaTitle: "Inception",
				MediaType:  "movie",
				DurationMs: &dur,
				OccurredAt: pgtype.Timestamptz{Time: now, Valid: true},
			},
		},
	}
	h := newHistoryHandler(db)

	rec := httptest.NewRecorder()
	req := historyAuthedRequest(httptest.NewRequest("GET", "/api/v1/history", nil))
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data []WatchHistoryItem `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("items: got %d, want 1", len(resp.Data))
	}
	if resp.Data[0].Title != "Inception" {
		t.Errorf("title: got %q, want %q", resp.Data[0].Title, "Inception")
	}
}

func TestHistory_List_Empty(t *testing.T) {
	db := &mockHistoryDB{rows: []gen.ListWatchHistoryRow{}}
	h := newHistoryHandler(db)

	rec := httptest.NewRecorder()
	req := historyAuthedRequest(httptest.NewRequest("GET", "/api/v1/history", nil))
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	var resp struct {
		Data []WatchHistoryItem `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Data) != 0 {
		t.Errorf("items: got %d, want 0", len(resp.Data))
	}
}

func TestHistory_List_NoAuth(t *testing.T) {
	h := newHistoryHandler(&mockHistoryDB{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/history", nil)
	h.List(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHistory_List_CustomLimitOffset(t *testing.T) {
	db := &mockHistoryDB{rows: []gen.ListWatchHistoryRow{}}
	h := newHistoryHandler(db)

	rec := httptest.NewRecorder()
	req := historyAuthedRequest(httptest.NewRequest("GET", "/api/v1/history?limit=10&offset=5", nil))
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHistory_List_DBError(t *testing.T) {
	db := &mockHistoryDB{err: errors.New("connection refused")}
	h := newHistoryHandler(db)

	rec := httptest.NewRecorder()
	req := historyAuthedRequest(httptest.NewRequest("GET", "/api/v1/history", nil))
	h.List(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
