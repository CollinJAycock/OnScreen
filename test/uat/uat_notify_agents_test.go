// uat_notify_agents_test.go — router-level gates for the outbound
// notification agents (Settings → Notifications).
package uat

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/notifyagents"
)

// stubNotifyAgents satisfies v1.NotificationAgentService with an empty store
// that accepts creates.
type stubNotifyAgents struct{}

func (stubNotifyAgents) List(context.Context) ([]notifyagents.Agent, error) {
	return []notifyagents.Agent{}, nil
}
func (stubNotifyAgents) Get(context.Context, uuid.UUID) (notifyagents.Agent, error) {
	return notifyagents.Agent{}, notifyagents.ErrNotFound
}
func (stubNotifyAgents) Create(_ context.Context, in notifyagents.CreateInput) (notifyagents.Agent, error) {
	return notifyagents.Agent{ID: uuid.New(), Kind: in.Kind, Name: in.Name, Enabled: true,
		Events: []string{notifyagents.EventRequestPending}, SecretConfigured: in.Secret != ""}, nil
}
func (stubNotifyAgents) Update(context.Context, uuid.UUID, notifyagents.UpdateInput) (notifyagents.Agent, error) {
	return notifyagents.Agent{}, notifyagents.ErrNotFound
}
func (stubNotifyAgents) Delete(context.Context, uuid.UUID) error { return notifyagents.ErrNotFound }
func (stubNotifyAgents) SendTest(context.Context, uuid.UUID) error {
	return notifyagents.ErrNotFound
}

func newNotifyAgentsServer(t *testing.T) *testServer {
	t.Helper()
	return newExtrasServer(t, func(h *api.Handlers) {
		h.NotificationAgents = v1.NewNotificationAgentHandler(stubNotifyAgents{}, slog.Default())
	})
}

// TestNotifyAgents_AdminOnly: every notification-agent route sits behind
// AdminRequired — anonymous → 401, a regular user → 403, an admin reaches the
// handler.
func TestNotifyAgents_AdminOnly(t *testing.T) {
	ts := newNotifyAgentsServer(t)
	id := uuid.New().String()
	routes := []struct {
		method, path string
		body         any
		adminWant    int
	}{
		{"GET", "/api/v1/admin/notification-agents", nil, http.StatusOK},
		{"GET", "/api/v1/admin/notification-agents/events", nil, http.StatusOK},
		{"POST", "/api/v1/admin/notification-agents", map[string]any{"kind": "discord", "name": "Chat", "secret": "https://discord.com/api/webhooks/1/x"}, http.StatusCreated},
		{"PATCH", "/api/v1/admin/notification-agents/" + id, map[string]any{"enabled": false}, http.StatusNotFound},
		{"DELETE", "/api/v1/admin/notification-agents/" + id, nil, http.StatusNotFound},
		{"POST", "/api/v1/admin/notification-agents/" + id + "/test", nil, http.StatusNotFound},
	}
	for _, rt := range routes {
		resp := ts.do(rt.method, rt.path, "", rt.body)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous: status = %d, want 401", rt.method, rt.path, resp.StatusCode)
		}
		resp.Body.Close()

		resp = ts.do(rt.method, rt.path, ts.userToken(), rt.body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as non-admin: status = %d, want 403", rt.method, rt.path, resp.StatusCode)
		}
		resp.Body.Close()

		resp = ts.do(rt.method, rt.path, ts.adminToken(), rt.body)
		if resp.StatusCode != rt.adminWant {
			t.Errorf("%s %s as admin: status = %d, want %d", rt.method, rt.path, resp.StatusCode, rt.adminWant)
		}
		resp.Body.Close()
	}
}

// TestNotifyAgents_EventsShape: the events catalog the settings page renders.
func TestNotifyAgents_EventsShape(t *testing.T) {
	ts := newNotifyAgentsServer(t)
	resp := ts.do("GET", "/api/v1/admin/notification-agents/events", ts.adminToken(), nil)
	assertStatus(t, resp, http.StatusOK)
	var env struct {
		Data []notifyagents.EventInfo `json:"data"`
	}
	mustDecode(t, resp, &env)
	keys := make([]string, 0, len(env.Data))
	for _, e := range env.Data {
		keys = append(keys, e.Key)
		if e.Label == "" || e.DefaultFor == nil {
			t.Errorf("incomplete event %+v", e)
		}
	}
	joined := strings.Join(keys, ",")
	for _, want := range []string{"request_pending", "request_available", "issue_reported", "new_content", "task_failed", "backup_failed", "worker_fleet_down"} {
		if !strings.Contains(joined, want) {
			t.Errorf("events missing %s: %v", want, keys)
		}
	}
}
