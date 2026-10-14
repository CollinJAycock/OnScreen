package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/portmap"
)

type memRemoteAccess struct{ cfg settings.RemoteAccessConfig }

func (m *memRemoteAccess) RemoteAccess(context.Context) settings.RemoteAccessConfig {
	return m.cfg.WithDefaults()
}
func (m *memRemoteAccess) SetRemoteAccess(_ context.Context, c settings.RemoteAccessConfig) error {
	c = c.WithDefaults()
	if err := c.Validate(); err != nil {
		return err
	}
	m.cfg = c
	return nil
}

type fakeMapper struct {
	running bool
	status  portmap.Status
	kicks   int
}

func (f *fakeMapper) Running() bool          { return f.running }
func (f *fakeMapper) Status() portmap.Status { return f.status }
func (f *fakeMapper) Kick()                  { f.kicks++ }

type fakeShared struct {
	st portmap.Status
	ok bool
}

func (f fakeShared) Load(context.Context) (portmap.Status, bool) { return f.st, f.ok }

func decodeRemoteAccess(t *testing.T, rec *httptest.ResponseRecorder) remoteAccessDTO {
	t.Helper()
	var env struct {
		Data remoteAccessDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return env.Data
}

func TestRemoteAccess_GetDefaultsOff(t *testing.T) {
	h := NewRemoteAccessHandler(&memRemoteAccess{}, &fakeMapper{}, ":7070", false, slog.Default())
	rec := httptest.NewRecorder()
	h.Get(rec, withClaims(httptest.NewRequest(http.MethodGet, "/api/v1/settings/remote-access", nil)))
	d := decodeRemoteAccess(t, rec)
	if d.MapPort || d.Protocol != "TCP" || d.ListenPort != 7070 || d.LoopbackOnly {
		t.Fatalf("got %+v", d)
	}
	if d.Status.State != portmap.StateDisabled || d.ReachabilityHint == "" {
		t.Fatalf("status = %+v hint %q", d.Status, d.ReachabilityHint)
	}
}

func TestRemoteAccess_LoopbackListenIsFlagged(t *testing.T) {
	h := NewRemoteAccessHandler(&memRemoteAccess{}, &fakeMapper{}, "127.0.0.1:7070", false, slog.Default())
	rec := httptest.NewRecorder()
	h.Get(rec, withClaims(httptest.NewRequest(http.MethodGet, "/", nil)))
	if d := decodeRemoteAccess(t, rec); !d.LoopbackOnly {
		t.Fatalf("got %+v, want loopback_only", d)
	}
}

func TestRemoteAccess_UpdateValidatesSavesAndKicks(t *testing.T) {
	store := &memRemoteAccess{}
	mapper := &fakeMapper{}
	h := NewRemoteAccessHandler(store, mapper, ":7070", true, slog.Default())

	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.Update(rec, withClaims(httptest.NewRequest(http.MethodPut, "/", bytes.NewBufferString(body))))
		return rec
	}
	if rec := put(`{"map_port":true,"external_port":70000}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad port: %d", rec.Code)
	}
	if rec := put(`{"map_port":true,"protocol":"udp"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("udp: %d", rec.Code)
	}
	if store.cfg.MapPort || mapper.kicks != 0 {
		t.Fatal("an invalid update was saved")
	}
	rec := put(`{"map_port":true,"external_port":32400}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid: %d %s", rec.Code, rec.Body.String())
	}
	if !store.cfg.MapPort || store.cfg.ExternalPort != 32400 || mapper.kicks != 1 {
		t.Fatalf("saved %+v kicks %d", store.cfg, mapper.kicks)
	}
	if d := decodeRemoteAccess(t, rec); !d.MapPort || !d.HTTPS || d.ExternalPort != 32400 {
		t.Fatalf("reply = %+v", d)
	}
}

// The admin's request can land on a node that doesn't map: it shows the
// status the mapping node published.
func TestRemoteAccess_StatusFromMappingNode(t *testing.T) {
	store := &memRemoteAccess{cfg: settings.RemoteAccessConfig{MapPort: true}}
	published := portmap.Status{State: portmap.StateMapped, ExternalIP: "203.0.113.7", ExternalPort: 7070, Node: "node-b"}
	h := NewRemoteAccessHandler(store, &fakeMapper{running: false}, ":7070", false, slog.Default()).
		WithSharedStatus(fakeShared{st: published, ok: true})
	rec := httptest.NewRecorder()
	h.Get(rec, withClaims(httptest.NewRequest(http.MethodGet, "/", nil)))
	d := decodeRemoteAccess(t, rec)
	if d.Status.Node != "node-b" || d.Status.State != portmap.StateMapped || !d.Status.Enabled {
		t.Fatalf("status = %+v", d.Status)
	}
	if !bytes.Contains([]byte(d.ReachabilityHint), []byte("http://203.0.113.7:7070")) {
		t.Fatalf("hint = %q", d.ReachabilityHint)
	}
}
