package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/userexport"
)

// emptyOPDS is an OPDS store with no libraries and no files.
type emptyOPDS struct{}

var errNone = errors.New("none")

func (emptyOPDS) ListLibraries(context.Context) ([]gen.Library, error) { return nil, nil }
func (emptyOPDS) GetLibrary(context.Context, uuid.UUID) (gen.Library, error) {
	return gen.Library{}, errNone
}
func (emptyOPDS) ListOPDSBooks(context.Context, gen.ListOPDSBooksParams) ([]gen.ListOPDSBooksRow, error) {
	return nil, nil
}
func (emptyOPDS) GetMediaFile(context.Context, uuid.UUID) (gen.MediaFile, error) {
	return gen.MediaFile{}, errNone
}
func (emptyOPDS) GetMediaItem(context.Context, uuid.UUID) (gen.GetMediaItemRow, error) {
	return gen.GetMediaItemRow{}, errNone
}

func opdsRouter(t *testing.T) (http.Handler, string, *auth.TokenMaker) {
	router, key, _, tm := opdsRouterKeys(t)
	return router, key, tm
}

// opdsRouterKeys also returns a full-scope key.
func opdsRouterKeys(t *testing.T) (http.Handler, string, string, *auth.TokenMaker) {
	t.Helper()
	key, hash, _, _ := auth.GenerateAPIKey()
	full, fullHash, _, _ := auth.GenerateAPIKey()
	store := routerKeyStore{keys: map[string]string{hash: auth.APIKeyScopeReadOnly, fullHash: auth.APIKeyScopeFull}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tm, _ := auth.NewTokenMaker(auth.DeriveKey32("test-secret-key-that-is-32-bytes!"))
	router, _, _ := newTestRouterWith(t, t.TempDir(), func(h *Handlers) {
		h.Auth_mw = h.Auth_mw.WithAPIKeys(store)
		h.OPDS = v1.NewOPDSHandler(emptyOPDS{}, tm, logger)
		h.UserExport = v1.NewUserExportHandler(userexport.Store(nil), logger)
		h.APIKeys = v1.NewAPIKeyHandler(nil, logger)
	})
	return router, key, full, tm
}

func opdsGet(router http.Handler, path string, set func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if set != nil {
		set(req)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// OPDS takes HTTP Basic with an API key as the password — and only that:
// the account password is never accepted, and Basic is a credential nowhere
// else, so a key can't reach the RefuseAPIKey routes this way.
func TestRouter_OPDSBasicAuth(t *testing.T) {
	router, key, _ := opdsRouter(t)

	w := opdsGet(router, "/opds/", nil)
	if w.Code != http.StatusUnauthorized || !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("anonymous: status %d, WWW-Authenticate %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}

	w = opdsGet(router, "/opds/", func(r *http.Request) { r.SetBasicAuth("anything", key) })
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<feed") {
		t.Fatalf("basic + read-only key: status %d body %s", w.Code, w.Body.String())
	}
	// The catalog root answers without the trailing slash too (what people
	// type into a reader app).
	w = opdsGet(router, "/opds", func(r *http.Request) { r.SetBasicAuth("anything", key) })
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<feed") {
		t.Fatalf("/opds without slash: status %d", w.Code)
	}
	// Username is ignored.
	w = opdsGet(router, "/opds/opensearch.xml", func(r *http.Request) { r.SetBasicAuth("", key) })
	if w.Code != http.StatusOK {
		t.Fatalf("empty username: %d", w.Code)
	}
	// X-Api-Key works too.
	w = opdsGet(router, "/opds/", func(r *http.Request) { r.Header.Set("X-Api-Key", key) })
	if w.Code != http.StatusOK {
		t.Fatalf("X-Api-Key: %d", w.Code)
	}

	// A password — anything that isn't a key — is refused.
	w = opdsGet(router, "/opds/", func(r *http.Request) { r.SetBasicAuth("alice", "correct horse battery staple") })
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("password over basic: %d", w.Code)
	}

	// Basic is not a credential outside /opds: a key sent that way can't
	// reach a RefuseAPIKey route (or any other).
	for _, p := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/users/me/api-keys"},
		{http.MethodPost, "/api/v1/users/me/export"},
		{http.MethodGet, "/artwork/Film/poster.jpg"},
	} {
		req := httptest.NewRequest(p.method, p.path, nil)
		req.SetBasicAuth("x", key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with basic key: status %d, want 401", p.method, p.path, w.Code)
		}
	}
}

// Failed Basic attempts lock the address out (429) — even for a right key —
// until the window passes.
func TestRouter_OPDSFailedAuthThrottle(t *testing.T) {
	router, key, _ := opdsRouter(t)
	for i := 0; i < 10; i++ {
		w := opdsGet(router, "/opds/", func(r *http.Request) { r.SetBasicAuth("x", "osk_wrong"+strings.Repeat("a", 39)) })
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	w := opdsGet(router, "/opds/", func(r *http.Request) { r.SetBasicAuth("x", key) })
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("after 10 failures: status %d, want 429", w.Code)
	}
	// Another address is unaffected.
	w = opdsGet(router, "/opds/", func(r *http.Request) {
		r.RemoteAddr = "198.51.100.7:4000"
		r.SetBasicAuth("x", key)
	})
	if w.Code != http.StatusOK {
		t.Fatalf("other address: %d", w.Code)
	}
}

// The cover route takes the file-bound stream token from the feed's links;
// the feed routes don't take ?token= at all.
func TestRouter_OPDSCoverStreamToken(t *testing.T) {
	router, _, tm := opdsRouter(t)
	fileID := uuid.New()
	tok, _ := tm.IssueStreamToken(auth.Claims{UserID: uuid.New()}, fileID)

	// Admitted: the handler's own 404 (the fake has no files), not a 401.
	w := opdsGet(router, "/opds/files/"+fileID.String()+"/cover?token="+tok, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bound token: status %d", w.Code)
	}
	w = opdsGet(router, "/opds/files/"+uuid.NewString()+"/cover?token="+tok, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("token for another file: status %d, want 401", w.Code)
	}
	asset, _ := tm.IssueAssetToken(auth.Claims{UserID: uuid.New()})
	w = opdsGet(router, "/opds/?token="+asset, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("query token on a feed route: status %d, want 401", w.Code)
	}
}

// "Download my data" is account data: an API key is refused, in either
// header, even for the key's own account.
func TestRouter_UserExportRefusesAPIKey(t *testing.T) {
	router, _, full, _ := opdsRouterKeys(t)
	for _, path := range []string{"/api/v1/users/me/export", "/api/v1/users/" + uuid.NewString() + "/export"} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("X-Api-Key", full)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "API_KEY_NOT_ALLOWED") {
			t.Errorf("%s: status %d body %s", path, w.Code, w.Body.String())
		}
	}
}
