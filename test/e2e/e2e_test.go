// Package e2e contains end-to-end tests that run against a live OnScreen stack
// (server + worker + postgres + valkey), typically via docker-compose.
//
// These tests exercise real HTTP endpoints and verify the full request path
// from network ingress through middleware, handlers, services, and database.
//
// Run:
//
//	ONSCREEN_BASE_URL=http://localhost:7080 go test -tags e2e -count=1 -run E2E ./test/e2e/...
//
// The server base URL defaults to http://localhost:7070.
//
//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

var baseURL string

func TestMain(m *testing.M) {
	baseURL = os.Getenv("ONSCREEN_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:7070"
	}
	os.Exit(m.Run())
}

// ── helpers ─────────────────────────────────────────────────────────────────

func apiURL(path string) string { return baseURL + path }

func doJSON(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, apiURL(path), r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// decodeData decodes the JSON body and extracts the "data" envelope.
// Returns the inner data as map[string]any.
func decodeData(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var envelope map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("response missing data envelope; got: %v", envelope)
	}
	return data
}

// decodeDataArray decodes a JSON body and extracts the "data" envelope as an array.
func decodeDataArray(t *testing.T, resp *http.Response) []any {
	t.Helper()
	defer resp.Body.Close()
	var envelope map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := envelope["data"].([]any)
	if !ok {
		t.Fatalf("response data is not an array; got: %v", envelope)
	}
	return data
}

// decodeRaw decodes a JSON body without assuming any envelope (for health endpoints).
func decodeRaw(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return data
}

func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("status: got %d, want %d; body: %s", resp.StatusCode, want, string(body))
	}
}

// registerAndLogin registers a new user and logs in.
// If adminToken is empty, the first user is created (becomes admin).
// Otherwise, adminToken is used to register subsequent users.
func registerAndLogin(t *testing.T, adminToken, username, password string) (accessToken, refreshToken string) {
	t.Helper()

	// Register.
	resp := doJSON(t, "POST", "/api/v1/auth/register", adminToken, map[string]string{
		"username": username,
		"password": password,
	})
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Login to get tokens.
	resp = doJSON(t, "POST", "/api/v1/auth/login", "", map[string]string{
		"username": username,
		"password": password,
	})
	assertStatus(t, resp, http.StatusOK)
	data := decodeData(t, resp)
	accessToken, _ = data["access_token"].(string)
	refreshToken, _ = data["refresh_token"].(string)
	if accessToken == "" {
		t.Fatal("login: no access_token in response")
	}
	return accessToken, refreshToken
}

// bootstrapAdmin logs in as the well-known admin user created by TestE2E_SetupAndAuth.
// Must run after that test has created the first user.
func bootstrapAdmin(t *testing.T) string {
	t.Helper()
	resp := doJSON(t, "POST", "/api/v1/auth/login", "", map[string]string{
		"username": "e2e_bootstrap_admin",
		"password": "TestPassword123!",
	})
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("bootstrap admin login failed: %d %s", resp.StatusCode, body)
	}
	data := decodeData(t, resp)
	token, _ := data["access_token"].(string)
	if token == "" {
		t.Fatal("bootstrap admin: no access_token")
	}
	return token
}

// ── health ──────────────────────────────────────────────────────────────────

func TestE2E_HealthLive(t *testing.T) {
	resp := doJSON(t, "GET", "/health/live", "", nil)
	assertStatus(t, resp, http.StatusOK)
	data := decodeRaw(t, resp)
	if data["status"] != "ok" {
		t.Errorf("health/live status: got %q, want ok", data["status"])
	}
}

func TestE2E_HealthReady(t *testing.T) {
	resp := doJSON(t, "GET", "/health/ready", "", nil)
	assertStatus(t, resp, http.StatusOK)
	data := decodeRaw(t, resp)
	if data["status"] != "ok" {
		t.Fatalf("health/ready status: got %v, want ok", data["status"])
	}
	checks, ok := data["checks"].(map[string]any)
	if !ok {
		t.Fatal("health/ready missing checks object")
	}
	if checks["postgres"] != "ok" {
		t.Errorf("postgres check: %v", checks["postgres"])
	}
	if checks["valkey"] != "ok" {
		t.Errorf("valkey check: %v", checks["valkey"])
	}
}

// ── setup + auth ────────────────────────────────────────────────────────────

func TestE2E_SetupAndAuth(t *testing.T) {
	// Use the well-known bootstrap credentials so subsequent tests can reuse them.
	username := "e2e_bootstrap_admin"
	password := "TestPassword123!"

	// 1. Fresh DB: setup_required should be true.
	t.Run("setup_required", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/setup/status", "", nil)
		assertStatus(t, resp, http.StatusOK)
		data := decodeData(t, resp)
		if data["setup_required"] != true {
			t.Errorf("setup_required: got %v, want true", data["setup_required"])
		}
	})

	// 2. Register first user (becomes admin).
	t.Run("register", func(t *testing.T) {
		resp := doJSON(t, "POST", "/api/v1/auth/register", "", map[string]string{
			"username": username,
			"password": password,
		})
		assertStatus(t, resp, http.StatusCreated)
		data := decodeData(t, resp)
		if data["username"] != username {
			t.Errorf("registered username: got %v", data["username"])
		}
		if data["is_admin"] != true {
			t.Error("first user should be admin")
		}
	})

	// 3. Setup no longer required.
	t.Run("setup_complete", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/setup/status", "", nil)
		assertStatus(t, resp, http.StatusOK)
		data := decodeData(t, resp)
		if data["setup_required"] != false {
			t.Errorf("setup_required after register: got %v", data["setup_required"])
		}
	})

	// 4. Login.
	var accessToken, refreshToken string
	t.Run("login", func(t *testing.T) {
		resp := doJSON(t, "POST", "/api/v1/auth/login", "", map[string]string{
			"username": username,
			"password": password,
		})
		assertStatus(t, resp, http.StatusOK)
		data := decodeData(t, resp)
		accessToken, _ = data["access_token"].(string)
		refreshToken, _ = data["refresh_token"].(string)
		if accessToken == "" {
			t.Fatal("login: no access_token")
		}
		if refreshToken == "" {
			t.Fatal("login: no refresh_token")
		}
	})

	// 5. Refresh token.
	t.Run("refresh", func(t *testing.T) {
		resp := doJSON(t, "POST", "/api/v1/auth/refresh", "", map[string]string{
			"refresh_token": refreshToken,
		})
		assertStatus(t, resp, http.StatusOK)
		data := decodeData(t, resp)
		newToken, _ := data["access_token"].(string)
		if newToken == "" {
			t.Fatal("refresh: no access_token")
		}
		accessToken = newToken
	})

	// 6. Authenticated endpoints.
	t.Run("libraries_authed", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/libraries", accessToken, nil)
		assertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	})

	t.Run("libraries_unauthed", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/libraries", "", nil)
		assertStatus(t, resp, http.StatusUnauthorized)
		resp.Body.Close()
	})

	// 7. Hub.
	t.Run("hub", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/hub", accessToken, nil)
		assertStatus(t, resp, http.StatusOK)
		data := decodeData(t, resp)
		if _, ok := data["continue_watching"]; !ok {
			t.Error("hub: missing continue_watching")
		}
		if _, ok := data["recently_added"]; !ok {
			t.Error("hub: missing recently_added")
		}
	})

	// 8. Search.
	t.Run("search", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/search?q=test", accessToken, nil)
		assertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	})

	// 9. History.
	t.Run("history", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/history", accessToken, nil)
		assertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	})

	// 10. Logout.
	t.Run("logout", func(t *testing.T) {
		resp := doJSON(t, "POST", "/api/v1/auth/logout", accessToken, map[string]string{
			"refresh_token": refreshToken,
		})
		assertStatus(t, resp, http.StatusNoContent)
		resp.Body.Close()
	})
}

// ── library CRUD ────────────────────────────────────────────────────────────

func TestE2E_LibraryCRUD(t *testing.T) {
	token := bootstrapAdmin(t)

	var libID string

	t.Run("create", func(t *testing.T) {
		resp := doJSON(t, "POST", "/api/v1/libraries", token, map[string]any{
			"name":       "E2E Movies",
			"type":       "movie",
			"scan_paths": []string{"/media/e2e-movies"},
		})
		assertStatus(t, resp, http.StatusCreated)
		data := decodeData(t, resp)
		libID, _ = data["id"].(string)
		if libID == "" {
			t.Fatal("create library: no id")
		}
		if data["name"] != "E2E Movies" {
			t.Errorf("library name: got %v", data["name"])
		}
	})

	t.Run("list", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/libraries", token, nil)
		assertStatus(t, resp, http.StatusOK)
		libs := decodeDataArray(t, resp)
		found := false
		for _, l := range libs {
			if m, ok := l.(map[string]any); ok && m["id"] == libID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("library %s not in list", libID)
		}
	})

	t.Run("get", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/libraries/"+libID, token, nil)
		assertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	})

	t.Run("update", func(t *testing.T) {
		resp := doJSON(t, "PATCH", "/api/v1/libraries/"+libID, token, map[string]any{
			"name":       "E2E Movies Updated",
			"scan_paths": []string{"/media/e2e-movies"},
		})
		assertStatus(t, resp, http.StatusOK)
		data := decodeData(t, resp)
		if data["name"] != "E2E Movies Updated" {
			t.Errorf("updated name: got %v", data["name"])
		}
	})

	t.Run("delete", func(t *testing.T) {
		resp := doJSON(t, "DELETE", "/api/v1/libraries/"+libID, token, nil)
		assertStatus(t, resp, http.StatusNoContent)
		resp.Body.Close()
	})

	t.Run("get_deleted", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/libraries/"+libID, token, nil)
		assertStatus(t, resp, http.StatusNotFound)
		resp.Body.Close()
	})
}

// ── webhook CRUD ────────────────────────────────────────────────────────────

func TestE2E_WebhookCRUD(t *testing.T) {
	token := bootstrapAdmin(t)

	var whID string

	t.Run("create", func(t *testing.T) {
		resp := doJSON(t, "POST", "/api/v1/webhooks", token, map[string]any{
			"url":    "https://example.com/hook",
			"events": []string{"media.play"},
		})
		assertStatus(t, resp, http.StatusCreated)
		data := decodeData(t, resp)
		whID, _ = data["id"].(string)
		if whID == "" {
			t.Fatal("create webhook: no id")
		}
	})

	t.Run("list", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/webhooks", token, nil)
		assertStatus(t, resp, http.StatusOK)
		whs := decodeDataArray(t, resp)
		if len(whs) == 0 {
			t.Error("expected at least one webhook")
		}
	})

	t.Run("update", func(t *testing.T) {
		resp := doJSON(t, "PATCH", "/api/v1/webhooks/"+whID, token, map[string]any{
			"url": "https://example.com/hook-updated",
		})
		assertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	})

	t.Run("delete", func(t *testing.T) {
		resp := doJSON(t, "DELETE", "/api/v1/webhooks/"+whID, token, nil)
		assertStatus(t, resp, http.StatusNoContent)
		resp.Body.Close()
	})
}

// ── authorization ───────────────────────────────────────────────────────────

func TestE2E_NonAdminForbidden(t *testing.T) {
	adminToken := bootstrapAdmin(t)

	// Create non-admin user via admin and login as them.
	userName := fmt.Sprintf("e2e_usr_%d", time.Now().UnixNano())
	userToken, _ := registerAndLogin(t, adminToken, userName, "TestPassword123!")

	t.Run("user_forbidden_admin_endpoints", func(t *testing.T) {
		for _, ep := range []struct{ method, path string }{
			{"GET", "/api/v1/users"},
			{"GET", "/api/v1/audit"},
		} {
			resp := doJSON(t, ep.method, ep.path, userToken, nil)
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s %s: non-admin got %d, want 403", ep.method, ep.path, resp.StatusCode)
			}
			resp.Body.Close()
		}
	})

	t.Run("admin_allowed", func(t *testing.T) {
		for _, ep := range []struct{ method, path string }{
			{"GET", "/api/v1/users"},
			{"GET", "/api/v1/audit"},
		} {
			resp := doJSON(t, ep.method, ep.path, adminToken, nil)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s %s: admin got %d, want 200", ep.method, ep.path, resp.StatusCode)
			}
			resp.Body.Close()
		}
	})
}

// ── frontend ────────────────────────────────────────────────────────────────

func TestE2E_FrontendServed(t *testing.T) {
	resp, err := http.Get(apiURL("/"))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	assertStatus(t, resp, http.StatusOK)
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		t.Error("missing Content-Type on /")
	}
}

// ── public feature flags ────────────────────────────────────────────────────

func TestE2E_OAuthFlags(t *testing.T) {
	for _, provider := range []string{"google", "github", "discord"} {
		t.Run(provider, func(t *testing.T) {
			resp := doJSON(t, "GET", fmt.Sprintf("/api/v1/auth/%s/enabled", provider), "", nil)
			assertStatus(t, resp, http.StatusOK)
			data := decodeData(t, resp)
			if data["enabled"] != false {
				t.Errorf("%s enabled: got %v, want false", provider, data["enabled"])
			}
		})
	}
}

func TestE2E_EmailEnabled(t *testing.T) {
	resp := doJSON(t, "GET", "/api/v1/email/enabled", "", nil)
	assertStatus(t, resp, http.StatusOK)
	data := decodeData(t, resp)
	if data["enabled"] != false {
		t.Errorf("email enabled: got %v, want false", data["enabled"])
	}
}
