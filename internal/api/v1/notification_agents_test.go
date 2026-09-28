package v1

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/notifyagents"
)

// agentsFakeDB is a map-backed notifyagents.DB.
type agentsFakeDB struct {
	mu     sync.Mutex
	agents map[uuid.UUID]gen.NotificationAgent
}

func (f *agentsFakeDB) ListNotificationAgents(context.Context) ([]gen.NotificationAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []gen.NotificationAgent{}
	for _, a := range f.agents {
		out = append(out, a)
	}
	return out, nil
}
func (f *agentsFakeDB) GetNotificationAgent(_ context.Context, id uuid.UUID) (gen.NotificationAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.agents[id]
	if !ok {
		return a, pgx.ErrNoRows
	}
	return a, nil
}
func (f *agentsFakeDB) ListEnabledNotificationAgentsForEvent(context.Context, string) ([]gen.NotificationAgent, error) {
	return nil, nil
}
func (f *agentsFakeDB) CreateNotificationAgent(_ context.Context, p gen.CreateNotificationAgentParams) (gen.NotificationAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := pgtype.Timestamptz{Time: time.Now(), Valid: true}
	a := gen.NotificationAgent{ID: p.ID, Kind: p.Kind, Name: p.Name, Enabled: p.Enabled, Events: p.Events,
		Config: p.Config, Secret: p.Secret, AllowPrivateNetwork: p.AllowPrivateNetwork, CreatedAt: now, UpdatedAt: now}
	f.agents[p.ID] = a
	return a, nil
}
func (f *agentsFakeDB) UpdateNotificationAgent(_ context.Context, p gen.UpdateNotificationAgentParams) (gen.NotificationAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.agents[p.ID]
	if !ok {
		return a, pgx.ErrNoRows
	}
	a.Name, a.Enabled, a.Events, a.Config, a.Secret, a.AllowPrivateNetwork = p.Name, p.Enabled, p.Events, p.Config, p.Secret, p.AllowPrivateNetwork
	f.agents[p.ID] = a
	return a, nil
}
func (f *agentsFakeDB) DeleteNotificationAgent(_ context.Context, id uuid.UUID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.agents[id]; !ok {
		return 0, nil
	}
	delete(f.agents, id)
	return 1, nil
}
func (f *agentsFakeDB) RecordNotificationAgentSuccess(context.Context, uuid.UUID) error { return nil }
func (f *agentsFakeDB) RecordNotificationAgentFailure(context.Context, gen.RecordNotificationAgentFailureParams) error {
	return nil
}
func (f *agentsFakeDB) ListNewContentForNotificationAgents(context.Context, gen.ListNewContentForNotificationAgentsParams) ([]gen.ListNewContentForNotificationAgentsRow, error) {
	return nil, nil
}
func (f *agentsFakeDB) GetUser(context.Context, uuid.UUID) (gen.User, error) {
	return gen.User{}, pgx.ErrNoRows
}

type agentsFixture struct {
	h      *NotificationAgentHandler
	db     *agentsFakeDB
	audits chan gen.InsertAuditLogParams
	admin  *auth.Claims
}

func newAgentsFixture(t *testing.T) *agentsFixture {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	db := &agentsFakeDB{agents: map[uuid.UUID]gen.NotificationAgent{}}
	svc := notifyagents.New(notifyagents.Options{DB: db, Encrypter: enc, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	t.Cleanup(svc.Close)
	audits := make(chan gen.InsertAuditLogParams, 16)
	h := NewNotificationAgentHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithAudit(audit.New(&auditCapture{ch: audits}, slog.Default()))
	return &agentsFixture{h: h, db: db, audits: audits, admin: &auth.Claims{UserID: uuid.New(), Username: "root", IsAdmin: true}}
}

func (fx *agentsFixture) call(t *testing.T, fn http.HandlerFunc, method, target, body string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	fn(rec, issueReq(t, method, target, body, fx.admin, params))
	return rec
}

func (fx *agentsFixture) nextAudit(t *testing.T) gen.InsertAuditLogParams {
	t.Helper()
	select {
	case a := <-fx.audits:
		return a
	case <-time.After(2 * time.Second):
		t.Fatal("no audit entry")
	}
	return gen.InsertAuditLogParams{}
}

const agentsTestWebhook = "https://discord.com/api/webhooks/42/tOpSeCrEt-token"

// TestNotificationAgents_CreateAuditsNeverReturnsSecret: create → 201 with
// secret_configured and no secret; the audit entry records kind/name/events
// but not the webhook URL; list and events work.
func TestNotificationAgents_CreateAuditsNeverReturnsSecret(t *testing.T) {
	fx := newAgentsFixture(t)
	rec := fx.call(t, fx.h.Create, http.MethodPost, "/api/v1/admin/notification-agents",
		`{"kind":"discord","name":"Family chat","secret":"`+agentsTestWebhook+`"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "tOpSeCrEt") || !strings.Contains(rec.Body.String(), `"secret_configured":true`) {
		t.Errorf("create response = %s", rec.Body)
	}
	var created notifyagents.Agent
	issueDecodeData(t, rec, &created)
	if created.Kind != notifyagents.KindDiscord || !slices.Contains(created.Events, notifyagents.EventRequestPending) {
		t.Errorf("created = %+v", created)
	}
	a := fx.nextAudit(t)
	if a.Action != audit.ActionNotificationAgentCreate || a.Target == nil || *a.Target != created.ID.String() {
		t.Errorf("audit = %+v", a)
	}
	if strings.Contains(string(a.Detail), "tOpSeCrEt") || strings.Contains(string(a.Detail), "webhooks") ||
		!strings.Contains(string(a.Detail), `"kind":"discord"`) {
		t.Errorf("audit detail = %s", a.Detail)
	}

	rec = fx.call(t, fx.h.List, http.MethodGet, "/api/v1/admin/notification-agents", "", nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "tOpSeCrEt") || !strings.Contains(rec.Body.String(), "Family chat") {
		t.Errorf("list: %d %s", rec.Code, rec.Body)
	}
	rec = fx.call(t, fx.h.Events, http.MethodGet, "/api/v1/admin/notification-agents/events", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"key":"new_content"`) {
		t.Errorf("events: %d %s", rec.Code, rec.Body)
	}
}

func TestNotificationAgents_ValidationAndNotFound(t *testing.T) {
	fx := newAgentsFixture(t)
	for _, body := range []string{
		`{"kind":"discord","name":"x","secret":"https://evil.example/api/webhooks/1/x"}`,
		`{"kind":"slack","name":"x"}`,
		`{"kind":"email","name":"x","config":{"recipients":["nope"]}}`,
	} {
		rec := fx.call(t, fx.h.Create, http.MethodPost, "/api/v1/admin/notification-agents", body, nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d, want 422", body, rec.Code)
		}
	}
	if rec := fx.call(t, fx.h.Create, http.MethodPost, "/", `{bad`, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json: %d", rec.Code)
	}
	missing := uuid.New().String()
	params := map[string]string{"id": missing}
	if rec := fx.call(t, fx.h.Update, http.MethodPatch, "/", `{"enabled":false}`, params); rec.Code != http.StatusNotFound {
		t.Errorf("update missing: %d", rec.Code)
	}
	if rec := fx.call(t, fx.h.Delete, http.MethodDelete, "/", "", params); rec.Code != http.StatusNotFound {
		t.Errorf("delete missing: %d", rec.Code)
	}
	if rec := fx.call(t, fx.h.Test, http.MethodPost, "/", "", params); rec.Code != http.StatusNotFound {
		t.Errorf("test missing: %d", rec.Code)
	}
	if rec := fx.call(t, fx.h.Update, http.MethodPatch, "/", `{}`, map[string]string{"id": "nope"}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", rec.Code)
	}
}

// TestNotificationAgents_EndpointMoveNeedsSecret: PATCHing a self-hosted
// agent to a new server without re-entering its token → 422.
func TestNotificationAgents_EndpointMoveNeedsSecret(t *testing.T) {
	fx := newAgentsFixture(t)
	rec := fx.call(t, fx.h.Create, http.MethodPost, "/", `{"kind":"gotify","name":"LAN","secret":"AppTok3n",
		"allow_private_network":true,"config":{"server_url":"http://10.0.0.7:8080"}}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var a notifyagents.Agent
	issueDecodeData(t, rec, &a)
	<-fx.audits
	params := map[string]string{"id": a.ID.String()}
	rec = fx.call(t, fx.h.Update, http.MethodPatch, "/", `{"config":{"server_url":"http://10.0.0.99:8080"},"secret":""}`, params)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "re-entered") {
		t.Errorf("move without token: %d %s", rec.Code, rec.Body)
	}
	rec = fx.call(t, fx.h.Update, http.MethodPatch, "/", `{"config":{"server_url":"http://10.0.0.99:8080"},"secret":"NewTok"}`, params)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "NewTok") {
		t.Fatalf("move with token: %d %s", rec.Code, rec.Body)
	}
	au := fx.nextAudit(t)
	if au.Action != audit.ActionNotificationAgentUpdate || !strings.Contains(string(au.Detail), `"secret_changed":true`) ||
		!strings.Contains(string(au.Detail), `"host":"10.0.0.99:8080"`) || strings.Contains(string(au.Detail), "NewTok") {
		t.Errorf("update audit = %s", au.Detail)
	}
}

// TestNotificationAgents_TestEndpoint: POST …/test delivers synchronously and
// answers {ok:true}; a failed delivery is 200 {ok:false, error}; both audited.
func TestNotificationAgents_TestEndpoint(t *testing.T) {
	fx := newAgentsFixture(t)
	var status, hits atomic.Int32
	status.Store(http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/topic1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(int(status.Load()))
	}))
	defer srv.Close()
	// A self-hosted ntfy on loopback needs the private-network opt-in.
	body, _ := json.Marshal(map[string]any{"kind": "ntfy", "name": "Local", "allow_private_network": true,
		"config": map[string]string{"server_url": srv.URL, "topic": "topic1"}})
	rec := fx.call(t, fx.h.Create, http.MethodPost, "/", string(body), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var a notifyagents.Agent
	issueDecodeData(t, rec, &a)
	<-fx.audits
	params := map[string]string{"id": a.ID.String()}

	rec = fx.call(t, fx.h.Test, http.MethodPost, "/", "", params)
	var res NotificationAgentTestResult
	issueDecodeData(t, rec, &res)
	if rec.Code != http.StatusOK || !res.OK || hits.Load() != 1 {
		t.Fatalf("test ok: %d %+v hits=%d", rec.Code, res, hits.Load())
	}
	if au := fx.nextAudit(t); au.Action != audit.ActionNotificationAgentTest || !strings.Contains(string(au.Detail), `"ok":true`) {
		t.Errorf("test audit = %+v", au)
	}

	status.Store(http.StatusForbidden)
	rec = fx.call(t, fx.h.Test, http.MethodPost, "/", "", params)
	res = NotificationAgentTestResult{}
	issueDecodeData(t, rec, &res)
	if rec.Code != http.StatusOK || res.OK || !strings.Contains(res.Error, "HTTP 403") {
		t.Errorf("test failure: %d %+v", rec.Code, res)
	}
	if au := fx.nextAudit(t); !strings.Contains(string(au.Detail), `"ok":false`) {
		t.Errorf("failed test audit = %s", au.Detail)
	}

	rec = fx.call(t, fx.h.Delete, http.MethodDelete, "/", "", params)
	if rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d", rec.Code)
	}
	if au := fx.nextAudit(t); au.Action != audit.ActionNotificationAgentDelete {
		t.Errorf("delete audit = %+v", au)
	}
}
