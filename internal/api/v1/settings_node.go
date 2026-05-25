package v1

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/settings"
)

// nodeSettingDTO is the flat per-node config the Nodes settings page edits. For
// the current node, unset fields are filled with the running (env-effective)
// value; for other nodes we only know what's stored, so unset fields are blank.
// Restart-required — read once at the target node's startup.
type nodeSettingDTO struct {
	NodeID                string `json:"node_id"`
	IsCurrent             bool   `json:"is_current"`
	ListenAddr            string `json:"listen_addr"`
	MetricsAddr           string `json:"metrics_addr"`
	WorkerHealthAddr      string `json:"worker_health_addr"`
	CachePath             string `json:"cache_path"`
	StaticABRRoot         string `json:"static_abr_root"`
	SiteID                string `json:"site_id"`
	TranscodeQSVDecode    bool   `json:"transcode_qsv_decode"`
	DisableEmbeddedWorker bool   `json:"disable_embedded_worker"`
}

// nodeListDTO is the picker payload: which node is serving this request, and
// every node that has a stored config row.
type nodeListDTO struct {
	CurrentNodeID string                 `json:"current_node_id"`
	Nodes         []settings.NodeSummary `json:"nodes"`
}

// GetNodes handles GET /settings/nodes — the node picker. It returns nodes that
// have a stored config row PLUS any node currently joined to the transcode fleet
// (by the NODE_ID each worker advertises), so an admin can find and configure a
// freshly-joined worker before it has a config row of its own.
func (h *SettingsHandler) GetNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.svc.ListNodes(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list nodes", "err", err)
		respond.InternalError(w, r)
		return
	}
	// Merge in joined fleet workers that have no stored config yet. Dedupe by
	// NODE_ID against the config rows + the current node (which the UI always
	// shows). Workers on older builds report no NODE_ID and are skipped — they'd
	// only be addressable by a key their per-node config isn't read by.
	seen := map[string]bool{h.nodeID: true}
	for _, n := range nodes {
		seen[n.NodeID] = true
	}
	if h.workerLister != nil {
		if workers, werr := h.workerLister.ListWorkers(r.Context()); werr == nil {
			for _, wk := range workers {
				if wk.NodeID == "" || seen[wk.NodeID] {
					continue
				}
				seen[wk.NodeID] = true
				nodes = append(nodes, settings.NodeSummary{NodeID: wk.NodeID})
			}
		}
	}
	// Marshal as [] not null when empty, so clients can map() over it safely.
	if nodes == nil {
		nodes = []settings.NodeSummary{}
	}
	respond.Success(w, r, nodeListDTO{CurrentNodeID: h.nodeID, Nodes: nodes})
}

// GetNode handles GET /settings/node/{nodeID} — the effective config for a node.
func (h *SettingsHandler) GetNode(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeID")
	if nodeID == "" {
		respond.BadRequest(w, r, "node id required")
		return
	}
	stored := h.svc.NodeSettingsGet(r.Context(), nodeID)
	// Only the current node has a known env-effective default; for other nodes we
	// can only surface what's stored.
	var d settings.NodeSettings
	if nodeID == h.nodeID {
		d = h.nodeDefaults
	}
	respond.Success(w, r, nodeSettingDTO{
		NodeID:                nodeID,
		IsCurrent:             nodeID == h.nodeID,
		ListenAddr:            pickStr(stored.ListenAddr, d.ListenAddr),
		MetricsAddr:           pickStr(stored.MetricsAddr, d.MetricsAddr),
		WorkerHealthAddr:      pickStr(stored.WorkerHealthAddr, d.WorkerHealthAddr),
		CachePath:             pickStr(stored.CachePath, d.CachePath),
		StaticABRRoot:         pickStr(stored.StaticABRRoot, d.StaticABRRoot),
		SiteID:                pickStr(stored.SiteID, d.SiteID),
		TranscodeQSVDecode:    pickBool(stored.TranscodeQSVDecode, d.TranscodeQSVDecode),
		DisableEmbeddedWorker: pickBool(stored.DisableEmbeddedWorker, d.DisableEmbeddedWorker),
	})
}

// UpdateNode handles PUT /settings/node/{nodeID} — store a node's config.
func (h *SettingsHandler) UpdateNode(w http.ResponseWriter, r *http.Request) {
	nodeID := chi.URLParam(r, "nodeID")
	if nodeID == "" {
		respond.BadRequest(w, r, "node id required")
		return
	}
	var dto nodeSettingDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	ns := settings.NodeSettings{
		ListenAddr:            &dto.ListenAddr,
		MetricsAddr:           &dto.MetricsAddr,
		WorkerHealthAddr:      &dto.WorkerHealthAddr,
		CachePath:             &dto.CachePath,
		StaticABRRoot:         &dto.StaticABRRoot,
		SiteID:                &dto.SiteID,
		TranscodeQSVDecode:    &dto.TranscodeQSVDecode,
		DisableEmbeddedWorker: &dto.DisableEmbeddedWorker,
	}
	if err := h.svc.SetNodeSettings(ctx, nodeID, ns); err != nil {
		h.logger.ErrorContext(ctx, "update node settings", "node_id", nodeID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(ctx); claims != nil {
			h.audit.Log(ctx, &claims.UserID, audit.ActionSettingsUpdate, "", map[string]any{
				"node_settings": nodeID,
			}, audit.ClientIP(r))
		}
	}
	respond.NoContent(w)
}
