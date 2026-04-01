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

var (
	baseURL string

	// Cached tokens — populated once by TestE2E_SetupAndAuth, reused by all
	// subsequent tests to avoid hitting the auth rate limiter (10 req/min).
	cachedAdminToken   string
	cachedUserToken    string
)

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

// bootstrapAdmin returns the admin token cached from TestE2E_SetupAndAuth.
// Falls back to a fresh login only if the cache is empty (e.g. running a
// single test in isolation), but in the normal full run this never fires.
func bootstrapAdmin(t *testing.T) string {
	t.Helper()
	if cachedAdminToken != "" {
		return cachedAdminToken
	}
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
	cachedAdminToken = token
	return token
}

// bootstrapNonAdmin returns a cached non-admin token. Created once on first
// call to avoid burning auth rate-limit budget creating per-test users.
func bootstrapNonAdmin(t *testing.T) string {
	t.Helper()
	if cachedUserToken != "" {
		return cachedUserToken
	}
	adminToken := bootstrapAdmin(t)
	userName := fmt.Sprintf("e2e_user_%d", time.Now().Unix()%100000)
	userToken, _ := registerAndLogin(t, adminToken, userName, "TestPassword123!")
	cachedUserToken = userToken
	return cachedUserToken
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
		cachedAdminToken = newToken // cache for all subsequent tests
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
	userToken := bootstrapNonAdmin(t)

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

// ── fleet management ───────────────────────────────────────────────────────

func TestE2E_FleetManagement(t *testing.T) {
	token := bootstrapAdmin(t)

	// 1. Default fleet: embedded enabled, embedded worker online.
	t.Run("get_default", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/settings/fleet", token, nil)
		assertStatus(t, resp, http.StatusOK)
		data := decodeData(t, resp)

		if data["embedded_enabled"] != true {
			t.Errorf("embedded_enabled: got %v, want true", data["embedded_enabled"])
		}
		// Embedded worker should be online in the docker stack.
		if data["embedded_online"] != true {
			t.Errorf("embedded_online: got %v, want true", data["embedded_online"])
		}
		// Workers array contains auto-discovered local workers (may be 0+).
		workers, ok := data["workers"].([]any)
		if !ok {
			t.Fatalf("workers: expected array, got %T", data["workers"])
		}
		// Each discovered worker should have an addr and online status.
		for i, w := range workers {
			wm := w.(map[string]any)
			if wm["addr"] == nil || wm["addr"] == "" {
				t.Errorf("workers[%d]: missing addr", i)
			}
		}
	})

	// 2. Update fleet: disable embedded, save worker overrides.
	t.Run("update_disable_embedded", func(t *testing.T) {
		resp := doJSON(t, "PUT", "/api/v1/settings/fleet", token, map[string]any{
			"embedded_enabled": false,
			"embedded_encoder": "",
			"workers": []map[string]any{
				{"addr": "10.0.0.5:7073", "name": "NVIDIA Box", "encoder": "h264_nvenc"},
				{"addr": "10.0.0.6:7073", "name": "AMD Box", "encoder": "h264_amf"},
			},
		})
		assertStatus(t, resp, http.StatusNoContent)
		resp.Body.Close()
	})

	// 3. Read back — embedded setting persists; saved workers appear offline
	//    when not discovered via Valkey.
	t.Run("get_after_update", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/settings/fleet", token, nil)
		assertStatus(t, resp, http.StatusOK)
		data := decodeData(t, resp)

		if data["embedded_enabled"] != false {
			t.Errorf("embedded_enabled: got %v, want false", data["embedded_enabled"])
		}
		// Saved workers should appear (offline) even when not discovered.
		workers := data["workers"].([]any)
		// At minimum, the 2 we saved should be present. Additional discovered
		// workers (from Docker containers) may also appear.
		if len(workers) < 2 {
			t.Errorf("workers: got %d, want at least 2 saved offline workers", len(workers))
		}
		// Verify the saved overrides are returned.
		for _, w := range workers {
			wm := w.(map[string]any)
			addr, _ := wm["addr"].(string)
			if addr == "10.0.0.5:7073" {
				if wm["name"] != "NVIDIA Box" {
					t.Errorf("workers[10.0.0.5:7073].name: got %q", wm["name"])
				}
				if wm["encoder"] != "h264_nvenc" {
					t.Errorf("workers[10.0.0.5:7073].encoder: got %q", wm["encoder"])
				}
			}
		}
	})

	// 4. Update fleet: set embedded encoder.
	t.Run("update_embedded_encoder", func(t *testing.T) {
		resp := doJSON(t, "PUT", "/api/v1/settings/fleet", token, map[string]any{
			"embedded_enabled": true,
			"embedded_encoder": "libx264",
			"workers":          []map[string]any{},
		})
		assertStatus(t, resp, http.StatusNoContent)
		resp.Body.Close()
	})

	// 5. Read back — verify embedded encoder persisted.
	t.Run("get_embedded_encoder", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/settings/fleet", token, nil)
		assertStatus(t, resp, http.StatusOK)
		data := decodeData(t, resp)

		if data["embedded_enabled"] != true {
			t.Errorf("embedded_enabled: got %v, want true", data["embedded_enabled"])
		}
		if data["embedded_encoder"] != "libx264" {
			t.Errorf("embedded_encoder: got %v, want libx264", data["embedded_encoder"])
		}
	})

	// 6. Reset to defaults.
	t.Run("reset_defaults", func(t *testing.T) {
		resp := doJSON(t, "PUT", "/api/v1/settings/fleet", token, map[string]any{
			"embedded_enabled": true,
			"embedded_encoder": "",
			"workers":          []map[string]any{},
		})
		assertStatus(t, resp, http.StatusNoContent)
		resp.Body.Close()
	})

	// 7. Non-admin cannot access fleet endpoints.
	t.Run("non_admin_forbidden", func(t *testing.T) {
		userToken := bootstrapNonAdmin(t)

		resp := doJSON(t, "GET", "/api/v1/settings/fleet", userToken, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET fleet as non-admin: got %d, want 403", resp.StatusCode)
		}
		resp.Body.Close()

		resp = doJSON(t, "PUT", "/api/v1/settings/fleet", userToken, map[string]any{
			"embedded_enabled": false,
			"workers":          []map[string]any{},
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("PUT fleet as non-admin: got %d, want 403", resp.StatusCode)
		}
		resp.Body.Close()
	})

	// 8. Unauthenticated access denied.
	t.Run("unauthenticated_denied", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/settings/fleet", "", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET fleet unauthenticated: got %d, want 401", resp.StatusCode)
		}
		resp.Body.Close()

		resp = doJSON(t, "PUT", "/api/v1/settings/fleet", "", map[string]any{
			"embedded_enabled": true,
			"workers":          []map[string]any{},
		})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("PUT fleet unauthenticated: got %d, want 401", resp.StatusCode)
		}
		resp.Body.Close()
	})
}

// ── workers endpoint ───────────────────────────────────────────────────────

func TestE2E_WorkersEndpoint(t *testing.T) {
	token := bootstrapAdmin(t)

	t.Run("list_workers", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/settings/workers", token, nil)
		assertStatus(t, resp, http.StatusOK)
		workers := decodeDataArray(t, resp)
		// The embedded worker should be registered in the docker stack.
		if len(workers) < 1 {
			t.Fatalf("expected at least 1 worker (embedded), got %d", len(workers))
		}
		w := workers[0].(map[string]any)
		if w["addr"] == nil || w["addr"] == "" {
			t.Error("worker missing addr")
		}
		caps, ok := w["capabilities"].([]any)
		if !ok || len(caps) == 0 {
			t.Error("worker missing capabilities")
		}
		maxSess, ok := w["max_sessions"].(float64)
		if !ok || maxSess <= 0 {
			t.Errorf("worker max_sessions: got %v", w["max_sessions"])
		}
	})

	t.Run("non_admin_forbidden", func(t *testing.T) {
		userToken := bootstrapNonAdmin(t)

		resp := doJSON(t, "GET", "/api/v1/settings/workers", userToken, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("workers as non-admin: got %d, want 403", resp.StatusCode)
		}
		resp.Body.Close()
	})
}

// ── encoders endpoint ──────────────────────────────────────────────────────

func TestE2E_EncodersEndpoint(t *testing.T) {
	token := bootstrapAdmin(t)

	t.Run("list_encoders", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/settings/encoders", token, nil)
		assertStatus(t, resp, http.StatusOK)
		data := decodeData(t, resp)

		detected, ok := data["detected"].([]any)
		if !ok {
			t.Fatalf("detected: expected array, got %T", data["detected"])
		}
		// At minimum, software encoder should always be detected.
		if len(detected) < 1 {
			t.Fatal("expected at least 1 detected encoder (software)")
		}
		// Each entry should have encoder and label fields.
		for i, d := range detected {
			entry := d.(map[string]any)
			if entry["encoder"] == nil || entry["encoder"] == "" {
				t.Errorf("detected[%d].encoder: empty", i)
			}
			if entry["label"] == nil || entry["label"] == "" {
				t.Errorf("detected[%d].label: empty", i)
			}
		}
		// Check that software is present.
		hasSoftware := false
		for _, d := range detected {
			entry := d.(map[string]any)
			if entry["encoder"] == "libx264" {
				hasSoftware = true
				break
			}
		}
		if !hasSoftware {
			t.Error("software encoder (libx264) not in detected list")
		}
	})

	t.Run("current_field_present", func(t *testing.T) {
		resp := doJSON(t, "GET", "/api/v1/settings/encoders", token, nil)
		assertStatus(t, resp, http.StatusOK)
		data := decodeData(t, resp)
		// "current" field should exist (may be empty string for auto-detect).
		if _, ok := data["current"]; !ok {
			t.Error("missing 'current' field in encoders response")
		}
	})
}
