package v1

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/userexport"
)

// exportStoreStub answers only the profile lookup; anything past it would
// panic on the nil embedded Store, which these tests never reach.
type exportStoreStub struct {
	userexport.Store
	calls int
}

func (s *exportStoreStub) ExportUserProfile(context.Context, uuid.UUID) (gen.ExportUserProfileRow, error) {
	s.calls++
	return gen.ExportUserProfileRow{}, pgx.ErrNoRows
}

func exportReq(claims *auth.Claims, id string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r = r.WithContext(middleware.WithClaims(r.Context(), claims))
	rctx := chi.NewRouteContext()
	if id != "" {
		rctx.URLParams.Add("id", id)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestUserExport_RefusesKeysAndTokens(t *testing.T) {
	st := &exportStoreStub{}
	h := NewUserExportHandler(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for name, c := range map[string]*auth.Claims{
		"api key":      {UserID: uuid.New(), APIKeyID: uuid.New()},
		"stream token": {UserID: uuid.New(), Purpose: "stream"},
	} {
		rec := httptest.NewRecorder()
		h.ExportMine(rec, exportReq(c, ""))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "API_KEY_NOT_ALLOWED") {
			t.Errorf("%s: status %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if st.calls != 0 {
		t.Error("the store was read for a refused caller")
	}
}

func TestUserExport_AdminOnlyAndUnknownUser(t *testing.T) {
	st := &exportStoreStub{}
	h := NewUserExportHandler(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec := httptest.NewRecorder()
	h.ExportUser(rec, exportReq(&auth.Claims{UserID: uuid.New()}, uuid.NewString()))
	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin: status %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ExportUser(rec, exportReq(&auth.Claims{UserID: uuid.New(), IsAdmin: true}, uuid.NewString()))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown user: status %d", rec.Code)
	}
}
