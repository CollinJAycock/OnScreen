package v1

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/transcode"
)

func nodeHandler(svc *mockSettingsService, nodeID string, defaults settings.NodeSettings) *SettingsHandler {
	return NewSettingsHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil))).
		SetNodeIdentity(nodeID, defaults)
}

func getNodeDTO(t *testing.T, h *SettingsHandler, nodeID string) nodeSettingDTO {
	t.Helper()
	req := withChiParams(httptest.NewRequest(http.MethodGet, "/settings/node/"+nodeID, nil), "nodeID", nodeID)
	rec := httptest.NewRecorder()
	h.GetNode(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetNode status = %d (%s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Data nodeSettingDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env.Data
}

func TestGetNode_CurrentFillsFromDefaults(t *testing.T) {
	// The current node, with nothing stored, surfaces its running (env) values.
	h := nodeHandler(&mockSettingsService{}, "node-a", settings.NodeSettings{
		ListenAddr: strPtr(":7070"),
		SiteID:     strPtr("site-a"),
	})
	dto := getNodeDTO(t, h, "node-a")
	if !dto.IsCurrent || dto.ListenAddr != ":7070" || dto.SiteID != "site-a" {
		t.Errorf("current node defaults not surfaced: %+v", dto)
	}
}

func TestGetNode_OtherNodeShowsStoredOnly(t *testing.T) {
	// A different node: we don't know its env, so only stored values appear and
	// the current-node defaults are NOT applied.
	svc := &mockSettingsService{nodeCfg: map[string]settings.NodeSettings{
		"node-b": {ListenAddr: strPtr(":8080")},
	}}
	h := nodeHandler(svc, "node-a", settings.NodeSettings{ListenAddr: strPtr(":7070")})
	dto := getNodeDTO(t, h, "node-b")
	if dto.IsCurrent {
		t.Error("node-b should not be marked current")
	}
	if dto.ListenAddr != ":8080" {
		t.Errorf("stored value should win: %q", dto.ListenAddr)
	}
	if dto.MetricsAddr != "" {
		t.Errorf("other node's unset field must stay blank, got %q", dto.MetricsAddr)
	}
}

func TestUpdateNode_StoresForTarget(t *testing.T) {
	svc := &mockSettingsService{}
	h := nodeHandler(svc, "node-a", settings.NodeSettings{})
	body := `{"listen_addr":":9090","site_id":"site-z","transcode_qsv_decode":true,"disable_embedded_worker":true}`
	req := withChiParams(httptest.NewRequest(http.MethodPut, "/settings/node/node-a", strings.NewReader(body)), "nodeID", "node-a")
	rec := httptest.NewRecorder()
	h.UpdateNode(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
	got := svc.nodeCfg["node-a"]
	if got.ListenAddr == nil || *got.ListenAddr != ":9090" {
		t.Errorf("ListenAddr not stored: %+v", got.ListenAddr)
	}
	if got.SiteID == nil || *got.SiteID != "site-z" {
		t.Errorf("SiteID not stored: %+v", got.SiteID)
	}
	if got.TranscodeQSVDecode == nil || !*got.TranscodeQSVDecode {
		t.Error("TranscodeQSVDecode not stored")
	}
	if got.DisableEmbeddedWorker == nil || !*got.DisableEmbeddedWorker {
		t.Error("DisableEmbeddedWorker not stored")
	}
}

func TestGetNodes_IncludesJoinedFleetWorkers(t *testing.T) {
	// A worker that joined the fleet but has no stored config row must still show
	// up in the picker, keyed by the NODE_ID it advertises — that's the whole
	// "I don't see my joined node in the dropdown" fix.
	h := nodeHandler(&mockSettingsService{}, "node-a", settings.NodeSettings{})
	h.SetWorkerLister(&mockWorkerLister{workers: []transcode.WorkerRegistration{
		{ID: "w1", Addr: "10.0.0.5:7073", NodeID: "gpu-box"},
		{ID: "w2", Addr: "127.0.0.1:7073", NodeID: "node-a"}, // == current node → deduped
		{ID: "w3", Addr: "10.0.0.6:7073", NodeID: ""},        // old build, no NODE_ID → skipped
	}})

	rec := httptest.NewRecorder()
	h.GetNodes(rec, httptest.NewRequest(http.MethodGet, "/settings/nodes", nil))
	var env struct {
		Data nodeListDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ids := make([]string, 0, len(env.Data.Nodes))
	for _, n := range env.Data.Nodes {
		ids = append(ids, n.NodeID)
	}
	if !contains(ids, "gpu-box") {
		t.Errorf("joined worker gpu-box should appear in the picker; got %v", ids)
	}
	if count(ids, "node-a") != 0 {
		t.Errorf("current node must not be duplicated into the list; got %v", ids)
	}
	if contains(ids, "") {
		t.Errorf("a worker with no NODE_ID must be skipped; got %v", ids)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
func count(s []string, v string) int {
	n := 0
	for _, x := range s {
		if x == v {
			n++
		}
	}
	return n
}

func TestDeleteNode_RemovesStoredConfig(t *testing.T) {
	svc := &mockSettingsService{nodeCfg: map[string]settings.NodeSettings{
		"stale-node": {SiteID: strPtr("old-site")},
	}}
	h := nodeHandler(svc, "node-a", settings.NodeSettings{})

	req := withChiParams(httptest.NewRequest(http.MethodDelete, "/settings/node/stale-node", nil), "nodeID", "stale-node")
	rec := httptest.NewRecorder()
	h.DeleteNode(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	if _, ok := svc.nodeCfg["stale-node"]; ok {
		t.Error("stale-node config row should have been deleted")
	}
}

func TestGetNodes_EmptyMarshalsAsArrayNotNull(t *testing.T) {
	// No stored rows → "nodes":[] so the web client can map() over it safely.
	h := nodeHandler(&mockSettingsService{}, "node-a", settings.NodeSettings{})
	rec := httptest.NewRecorder()
	h.GetNodes(rec, httptest.NewRequest(http.MethodGet, "/settings/nodes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"nodes":[]`) {
		t.Errorf("empty nodes should marshal as [], got: %s", body)
	}
}

func TestGetNodes_ListsCurrentAndStored(t *testing.T) {
	svc := &mockSettingsService{nodes: []settings.NodeSummary{{NodeID: "node-b"}}}
	h := nodeHandler(svc, "node-a", settings.NodeSettings{})
	rec := httptest.NewRecorder()
	h.GetNodes(rec, httptest.NewRequest(http.MethodGet, "/settings/nodes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var env struct {
		Data nodeListDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.CurrentNodeID != "node-a" {
		t.Errorf("current node id = %q, want node-a", env.Data.CurrentNodeID)
	}
	if len(env.Data.Nodes) != 1 || env.Data.Nodes[0].NodeID != "node-b" {
		t.Errorf("nodes list = %+v", env.Data.Nodes)
	}
}
