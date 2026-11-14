package v1

import (
	"bytes"
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
	"github.com/onscreen/onscreen/internal/domain/media"
)

// allowAllLibraries is a LibraryAccessChecker that lets everyone see every
// library — what a handler test wires when the ACL isn't what it tests (the
// ACL fails closed when no checker is wired).
type allowAllLibraries struct{}

func (allowAllLibraries) CanAccessLibrary(context.Context, uuid.UUID, uuid.UUID, bool) (bool, error) {
	return true, nil
}
func (allowAllLibraries) AllowedLibraryIDs(context.Context, uuid.UUID, bool) (map[uuid.UUID]struct{}, error) {
	return nil, nil
}

// testViewer is the signed-in, unrestricted, non-admin user request helpers
// attach when a test sets no claims of its own: every item route sits behind
// authentication, and a request with no caller is a 401.
func testViewer() *auth.Claims {
	return &auth.Claims{UserID: uuid.New(), Username: "viewer", IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
}

// withViewer attaches testViewer unless r already carries claims.
func withViewer(r *http.Request) *http.Request {
	if middleware.ClaimsFromContext(r.Context()) != nil {
		return r
	}
	return r.WithContext(middleware.WithClaims(r.Context(), testViewer()))
}

type fakeItemGetter struct {
	item *media.Item
	err  error
}

func (f fakeItemGetter) GetItem(context.Context, uuid.UUID) (*media.Item, error) {
	return f.item, f.err
}

type denyLibraries struct{ err error }

func (d denyLibraries) CanAccessLibrary(context.Context, uuid.UUID, uuid.UUID, bool) (bool, error) {
	return false, d.err
}
func (d denyLibraries) AllowedLibraryIDs(context.Context, uuid.UUID, bool) (map[uuid.UUID]struct{}, error) {
	return map[uuid.UUID]struct{}{}, d.err
}

// loadVisibleItem answers every refusal the same way across item routes:
// 401 without a caller, 404 for missing, hidden-by-library and
// hidden-by-rating, 500 (logged) for a lookup failure, and it fails closed
// for non-admins when no ACL checker is wired.
func TestLoadVisibleItem_Answers(t *testing.T) {
	pg13 := "PG-13"
	rRated := "R"
	movie := &media.Item{ID: uuid.New(), LibraryID: uuid.New(), Type: "movie", ContentRating: &rRated}
	capped := &auth.Claims{UserID: uuid.New(), MaxContentRating: pg13}
	admin := &auth.Claims{UserID: uuid.New(), IsAdmin: true}

	cases := []struct {
		name    string
		claims  *auth.Claims
		items   itemGetter
		access  LibraryAccessChecker
		want    int
		want403 int // loadVisibleItem403's answer, when it differs
	}{
		{"no caller", nil, fakeItemGetter{item: movie}, allowAllLibraries{}, http.StatusUnauthorized, 0},
		{"missing", testViewer(), fakeItemGetter{err: media.ErrNotFound}, allowAllLibraries{}, http.StatusNotFound, 0},
		{"nil item", testViewer(), fakeItemGetter{}, allowAllLibraries{}, http.StatusNotFound, 0},
		{"lookup failure", testViewer(), fakeItemGetter{err: errors.New("db down")}, allowAllLibraries{}, http.StatusInternalServerError, 0},
		{"library hidden", testViewer(), fakeItemGetter{item: movie}, denyLibraries{}, http.StatusNotFound, 0},
		{"acl failure", testViewer(), fakeItemGetter{item: movie}, denyLibraries{err: errors.New("db down")}, http.StatusInternalServerError, 0},
		{"no checker wired, non-admin", testViewer(), fakeItemGetter{item: movie}, nil, http.StatusNotFound, 0},
		{"no checker wired, admin", admin, fakeItemGetter{item: movie}, nil, http.StatusOK, 0},
		{"over the ceiling", capped, fakeItemGetter{item: movie}, allowAllLibraries{}, http.StatusNotFound, http.StatusForbidden},
		{"visible", testViewer(), fakeItemGetter{item: movie}, allowAllLibraries{}, http.StatusOK, 0},
	}
	for _, tc := range cases {
		for _, variant := range []string{"404", "403"} {
			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, nil))
			req := httptest.NewRequest(http.MethodGet, "/items/x", nil)
			if tc.claims != nil {
				req = req.WithContext(middleware.WithClaims(req.Context(), tc.claims))
			}
			rec := httptest.NewRecorder()
			load := loadVisibleItem
			want := tc.want
			if variant == "403" {
				load = loadVisibleItem403
				if tc.want403 != 0 {
					want = tc.want403
				}
			}
			item, ok := load(rec, req, tc.items, tc.access, logger, movie.ID)
			got := rec.Code
			if ok {
				got = http.StatusOK
				if item != movie {
					t.Errorf("%s/%s: ok without the item", tc.name, variant)
				}
			}
			if got != want {
				t.Errorf("%s/%s: got %d, want %d", tc.name, variant, got, want)
			}
			if want == http.StatusInternalServerError && !strings.Contains(buf.String(), "db down") {
				t.Errorf("%s/%s: 500 without the error in the log: %q", tc.name, variant, buf.String())
			}
		}
	}
}
