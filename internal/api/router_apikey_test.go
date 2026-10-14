package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
)

// routerKeyStore resolves every key it holds to the same admin user.
type routerKeyStore struct {
	keys map[string]string // hash → scope
}

func (s routerKeyStore) ResolveAPIKey(_ context.Context, hash string) (middleware.APIKeyIdentity, error) {
	scope, ok := s.keys[hash]
	if !ok {
		return middleware.APIKeyIdentity{}, middleware.ErrAPIKeyNotFound
	}
	return middleware.APIKeyIdentity{KeyID: uuid.New(), UserID: uuid.New(), Scope: scope, Username: "admin", IsAdmin: true}, nil
}

func (s routerKeyStore) APIKeyState(context.Context, uuid.UUID, uuid.UUID) (int64, bool, error) {
	return 0, true, nil
}

func (s routerKeyStore) TouchAPIKey(context.Context, uuid.UUID) error { return nil }

// Every credential and account-security endpoint refuses a personal API key
// — even an admin's — so a leaked key can't become a password, a second
// factor change, a new key, a paired device or another account. Ordinary
// routes take it.
func TestRouter_APIKeyRefusedOnAccountSecurityRoutes(t *testing.T) {
	full, fullHash, _, _ := auth.GenerateAPIKey()
	ro, roHash, _, _ := auth.GenerateAPIKey()
	store := routerKeyStore{keys: map[string]string{fullHash: auth.APIKeyScopeFull, roHash: auth.APIKeyScopeReadOnly}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router, _, _ := newTestRouterWith(t, t.TempDir(), func(h *Handlers) {
		h.Auth_mw = h.Auth_mw.WithAPIKeys(store)
		h.TOTP = v1.NewTOTPHandler(nil, logger)
		h.User = v1.NewUserHandler(nil)
		h.UserSessions = v1.NewUserSessionsHandler(nil, logger)
		h.APIKeys = v1.NewAPIKeyHandler(nil, logger)
		h.Settings = v1.NewSettingsHandler(nil, logger)
		h.Backup = v1.NewBackupHandler("", 0, nil, logger)
		h.Pair = v1.NewPairHandler(nil, nil, logger)
		h.Invite = v1.NewInviteHandler(nil, nil, "", logger)
		h.Webhook = v1.NewWebhookHandler(nil, logger)
	})
	uid := uuid.NewString()
	refused := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/auth/refresh"},
		{http.MethodPost, "/api/v1/auth/register"},
		{http.MethodPost, "/api/v1/auth/totp/verify"},
		{http.MethodPost, "/api/v1/auth/totp/setup"},
		{http.MethodPost, "/api/v1/auth/totp/disable"},
		{http.MethodGet, "/api/v1/users/me/api-keys"},
		{http.MethodPost, "/api/v1/users/me/api-keys"},
		{http.MethodDelete, "/api/v1/users/me/api-keys/" + uid},
		{http.MethodGet, "/api/v1/admin/api-keys"},
		{http.MethodDelete, "/api/v1/admin/api-keys/" + uid},
		{http.MethodDelete, "/api/v1/users/me/sessions"},
		{http.MethodDelete, "/api/v1/users/me/sessions/" + uid},
		{http.MethodDelete, "/api/v1/users/me"},
		{http.MethodPut, "/api/v1/users/me/pin"},
		{http.MethodPost, "/api/v1/auth/pin-switch"},
		{http.MethodPost, "/api/v1/auth/pair/claim"},
		{http.MethodGet, "/api/v1/users"},
		{http.MethodPut, "/api/v1/users/" + uid + "/password"},
		{http.MethodPatch, "/api/v1/users/" + uid},
		{http.MethodDelete, "/api/v1/users/" + uid + "/sessions"},
		{http.MethodPost, "/api/v1/profiles"},
		{http.MethodPost, "/api/v1/invites"},
		{http.MethodPatch, "/api/v1/settings"},
		{http.MethodGet, "/api/v1/admin/backup"},
	}
	for _, c := range refused {
		for _, header := range []string{"Authorization", "X-Api-Key"} {
			req := httptest.NewRequest(c.method, c.path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			if header == "Authorization" {
				req.Header.Set(header, "Bearer "+full)
			} else {
				req.Header.Set(header, full)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "API_KEY_NOT_ALLOWED") {
				t.Errorf("%s %s with %s: status %d body %s", c.method, c.path, header, w.Code, w.Body.String())
			}
		}
	}

	// An ordinary route takes either key (the artwork route's own 404 for a
	// missing file proves the request was admitted).
	for _, k := range []string{full, ro} {
		req := httptest.NewRequest(http.MethodGet, "/artwork/Film/poster.jpg", nil)
		req.Header.Set("X-Api-Key", k)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("key on an ordinary route: status %d body %s", w.Code, w.Body.String())
		}
	}

	// A read-only key can't write, even where its owner (an admin) could.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks", strings.NewReader(`{}`))
	req.Header.Set("X-Api-Key", ro)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var env struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if w.Code != http.StatusForbidden || env.Error.Code != "API_KEY_READ_ONLY" {
		t.Fatalf("read-only key PUT: status %d body %s", w.Code, w.Body.String())
	}
}
