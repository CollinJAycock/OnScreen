package uat

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ── Now Playing: admin stop (POST /api/v1/sessions/{id}/stop) ────────────────

func TestSessionsStop_RequiresAuth(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.do("POST", "/api/v1/sessions/any/stop", "", map[string]any{})
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

// Stopping someone's stream is an admin moderation action.
func TestSessionsStop_AdminOnly(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.do("POST", "/api/v1/sessions/any/stop", ts.userToken(), map[string]any{"message": "hi"})
	assertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

// Unknown ids are a 404 — unlike the idempotent DELETE
// /transcode/sessions/{sid}, which keeps its 204 contract for player teardown.
func TestSessionsStop_UnknownSessionIs404(t *testing.T) {
	ts := newTestServer(t)
	admin := ts.adminToken()
	for _, id := range []string{uuid.New().String(), "10.0.0.1|" + uuid.New().String()} {
		resp := ts.do("POST", "/api/v1/sessions/"+id+"/stop", admin, map[string]any{})
		assertStatus(t, resp, http.StatusNotFound)
		resp.Body.Close()
	}
	// An empty body is fine too (the message is optional).
	resp := ts.do("POST", "/api/v1/sessions/"+uuid.New().String()+"/stop", admin, nil)
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestSessionsStop_MessageCapped(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.do("POST", "/api/v1/sessions/any/stop", ts.adminToken(),
		map[string]any{"message": strings.Repeat("x", 201)})
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}
