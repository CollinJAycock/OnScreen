//go:build integration

// The interface language (migration 00063) against a real Postgres: the
// ui-language endpoint stores and clears users.ui_language, the preferences
// GET returns it, the preferences PUT leaves it alone, and the column's CHECK
// refuses a tag no app ships even when written directly.
//
// Run with: go test -tags=integration -run TestUILanguage ./internal/api/v1/
package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/testdb"
)

func TestUILanguage_Integration(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	q := gen.New(pool)
	u, err := q.CreateUser(ctx, gen.CreateUserParams{Username: "ui-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	h := NewUserHandler(&mockUserService{}).WithDB(q)

	authed := func(method, path, body string) *http.Request {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		return req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: u.ID}))
	}
	setLang := func(body string) int {
		rec := httptest.NewRecorder()
		h.SetUILanguage(rec, authed(http.MethodPut, "/users/me/ui-language", body))
		return rec.Code
	}
	getLang := func() *string {
		t.Helper()
		rec := httptest.NewRecorder()
		h.GetPreferences(rec, authed(http.MethodGet, "/users/me/preferences", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("get preferences: %d %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Data preferencesResponse `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Data.UILanguage
	}

	if got := getLang(); got != nil {
		t.Fatalf("new user: ui_language = %q, want null", *got)
	}
	if code := setLang(`{"ui_language":"zh-Hans"}`); code != http.StatusNoContent {
		t.Fatalf("set zh-Hans: %d", code)
	}
	if got := getLang(); got == nil || *got != "zh-Hans" {
		t.Fatalf("after set: %v, want zh-Hans", got)
	}

	// The preferences PUT rewrites the media languages, not ui_language.
	rec := httptest.NewRecorder()
	h.SetPreferences(rec, authed(http.MethodPut, "/users/me/preferences", `{"preferred_audio_lang":"spa"}`))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("set preferences: %d", rec.Code)
	}
	if got := getLang(); got == nil || *got != "zh-Hans" {
		t.Fatalf("preferences PUT changed ui_language to %v", got)
	}

	if code := setLang(`{"ui_language":null}`); code != http.StatusNoContent {
		t.Fatalf("clear: %d", code)
	}
	if got := getLang(); got != nil {
		t.Fatalf("after clear: %q, want null", *got)
	}

	// The CHECK holds even for a write that skips the handler.
	bad := "fr"
	if err := q.UpdateUserUILanguage(ctx, gen.UpdateUserUILanguageParams{ID: u.ID, UiLanguage: &bad}); err == nil {
		t.Fatal("the users.ui_language CHECK accepted 'fr'")
	}
	for _, ok := range SupportedUILanguages {
		ok := ok
		if err := q.UpdateUserUILanguage(ctx, gen.UpdateUserUILanguageParams{ID: u.ID, UiLanguage: &ok}); err != nil {
			t.Errorf("CHECK refused shipped language %q: %v", ok, err)
		}
	}
}
