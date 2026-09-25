package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/watchstatus"
)

type wsFakeSvc struct{ setCalls int }

func (f *wsFakeSvc) Get(context.Context, uuid.UUID, uuid.UUID) (watchstatus.Status, error) {
	return watchstatus.Status{}, nil
}
func (f *wsFakeSvc) Set(_ context.Context, _, _ uuid.UUID, st string) (watchstatus.Status, error) {
	f.setCalls++
	return watchstatus.Status{Status: st, CreatedAt: time.Now(), UpdatedAt: time.Now()}, nil
}
func (f *wsFakeSvc) Clear(context.Context, uuid.UUID, uuid.UUID) error { return nil }

type wsFakeItems struct{ item gen.GetMediaItemRow }

func (f wsFakeItems) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	if id != f.item.ID {
		return gen.GetMediaItemRow{}, errors.New("not found")
	}
	return f.item, nil
}

type wsFakeAccess struct{ allowed uuid.UUID }

func (f wsFakeAccess) CanAccessLibrary(_ context.Context, _, lib uuid.UUID, isAdmin bool) (bool, error) {
	return isAdmin || lib == f.allowed, nil
}
func (f wsFakeAccess) AllowedLibraryIDs(context.Context, uuid.UUID, bool) (map[uuid.UUID]struct{}, error) {
	return map[uuid.UUID]struct{}{f.allowed: {}}, nil
}

func wsPut(t *testing.T, h *WatchStatusHandler, itemID uuid.UUID) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/items/"+itemID.String()+"/watch-status",
		strings.NewReader(`{"status":"watching"}`))
	req = withChiParam(req, "id", itemID.String())
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uuid.New()}))
	rec := httptest.NewRecorder()
	h.Put(rec, req)
	return rec.Code
}

// TestWatchStatus_ItemGate pins that a watch status can only be attached to an
// item the caller can see.
func TestWatchStatus_ItemGate(t *testing.T) {
	allowedLib, otherLib := uuid.New(), uuid.New()

	hidden := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: otherLib}
	svc := &wsFakeSvc{}
	h := NewWatchStatusHandler(svc, slog.Default()).
		WithItemGate(wsFakeItems{item: hidden}, wsFakeAccess{allowed: allowedLib})
	if code := wsPut(t, h, hidden.ID); code != http.StatusNotFound {
		t.Errorf("item in an inaccessible library: got %d, want 404", code)
	}
	if svc.setCalls != 0 {
		t.Error("status must not be written for an item the caller cannot see")
	}

	visible := gen.GetMediaItemRow{ID: uuid.New(), LibraryID: allowedLib}
	svc2 := &wsFakeSvc{}
	h2 := NewWatchStatusHandler(svc2, slog.Default()).
		WithItemGate(wsFakeItems{item: visible}, wsFakeAccess{allowed: allowedLib})
	if code := wsPut(t, h2, visible.ID); code != http.StatusOK {
		t.Errorf("visible item: got %d, want 200", code)
	}
	if svc2.setCalls != 1 {
		t.Errorf("expected one Set call; got %d", svc2.setCalls)
	}
}
