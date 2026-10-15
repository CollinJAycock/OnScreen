package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/domain/settings"
	"github.com/onscreen/onscreen/internal/portmap"
)

// ── Remote access: automatic router port mapping ─────────────────────────────
//
// GET/PUT /settings/remote-access. With map_port on, the master node asks the
// home router (UPnP IGD / PCP / NAT-PMP) to forward external_port to this
// server's listen port, renews the lease and removes it when turned off or on
// shutdown (internal/portmap). That opens the server to the internet, so the
// setting is off by default, admin-only, refused to API keys, audited, and
// the page says so. The server never probes its own reachability through a
// third-party service: the status shows the external address and port and
// advises testing from outside.

// RemoteAccessStore reads and writes the setting. *settings.Service
// satisfies it.
type RemoteAccessStore interface {
	RemoteAccess(ctx context.Context) settings.RemoteAccessConfig
	SetRemoteAccess(ctx context.Context, cfg settings.RemoteAccessConfig) error
}

// RemoteAccessMapper is this node's mapper. *portmap.Mapper satisfies it.
type RemoteAccessMapper interface {
	Running() bool
	Status() portmap.Status
	Kick()
}

// RemoteAccessStatusSource is the status the mapping node published for the
// others. portmap.SharedStatus satisfies it.
type RemoteAccessStatusSource interface {
	Load(ctx context.Context) (portmap.Status, bool)
}

// RemoteAccessHandler serves /settings/remote-access.
type RemoteAccessHandler struct {
	store      RemoteAccessStore
	mapper     RemoteAccessMapper
	shared     RemoteAccessStatusSource
	listenAddr string
	https      bool
	audit      *audit.Logger
	logger     *slog.Logger
}

// NewRemoteAccessHandler builds the handler. listenAddr is this node's API
// listen address; https whether it serves HTTPS itself.
func NewRemoteAccessHandler(store RemoteAccessStore, mapper RemoteAccessMapper, listenAddr string, https bool, logger *slog.Logger) *RemoteAccessHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &RemoteAccessHandler{store: store, mapper: mapper, listenAddr: listenAddr, https: https, logger: logger}
}

// WithSharedStatus reads the mapping node's status when this node isn't it.
func (h *RemoteAccessHandler) WithSharedStatus(s RemoteAccessStatusSource) *RemoteAccessHandler {
	h.shared = s
	return h
}

// WithAudit records changes to the setting.
func (h *RemoteAccessHandler) WithAudit(a *audit.Logger) *RemoteAccessHandler {
	h.audit = a
	return h
}

// remoteAccessDTO is the GET (and PUT reply) shape. map_port, external_port
// and protocol are the setting; the rest is read-only.
type remoteAccessDTO struct {
	MapPort      bool   `json:"map_port"`
	ExternalPort int    `json:"external_port"`
	Protocol     string `json:"protocol"`

	// ListenAddr / ListenPort: what the mapping forwards to.
	ListenAddr string `json:"listen_addr"`
	ListenPort int    `json:"listen_port,omitempty"`
	// LoopbackOnly: the server listens on loopback, so it is never mapped.
	LoopbackOnly bool `json:"loopback_only"`
	// HTTPS: this server serves HTTPS itself (a reverse proxy in front may
	// too; the server can't tell).
	HTTPS bool `json:"https"`

	Status portmap.Status `json:"status"`
	// ReachabilityHint is what to try next, in words.
	ReachabilityHint string `json:"reachability_hint,omitempty"`
}

type remoteAccessUpdate struct {
	MapPort      bool   `json:"map_port"`
	ExternalPort int    `json:"external_port"`
	Protocol     string `json:"protocol"`
}

func (h *RemoteAccessHandler) view(ctx context.Context) remoteAccessDTO {
	cfg := h.store.RemoteAccess(ctx)
	out := remoteAccessDTO{
		MapPort: cfg.MapPort, ExternalPort: cfg.ExternalPort, Protocol: cfg.Protocol,
		ListenAddr: h.listenAddr, HTTPS: h.https,
	}
	if host, port, err := portmap.ParseListenAddr(h.listenAddr); err == nil {
		out.ListenPort = port
		out.LoopbackOnly = portmap.IsLoopbackHost(host)
	}
	out.Status = h.status(ctx)
	out.Status.Enabled = cfg.MapPort
	if !cfg.MapPort && out.Status.State != portmap.StateMapped {
		out.Status.State = portmap.StateDisabled
	}
	out.ReachabilityHint = portmap.ReachabilityHint(out.Status, h.https)
	return out
}

// status is the mapping node's view: this node's when it maps, else the one
// the mapping node published.
func (h *RemoteAccessHandler) status(ctx context.Context) portmap.Status {
	if h.mapper != nil && h.mapper.Running() {
		return h.mapper.Status()
	}
	if h.shared != nil {
		if st, ok := h.shared.Load(ctx); ok {
			return st
		}
	}
	return portmap.Status{State: portmap.StateStandby}
}

// Get handles GET /api/v1/settings/remote-access.
func (h *RemoteAccessHandler) Get(w http.ResponseWriter, r *http.Request) {
	respond.Success(w, r, h.view(r.Context()))
}

// Update handles PUT /api/v1/settings/remote-access {map_port,
// external_port, protocol}. The mapping follows within a few seconds on the
// node that maps (immediately when that is this node).
func (h *RemoteAccessHandler) Update(w http.ResponseWriter, r *http.Request) {
	var body remoteAccessUpdate
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	ctx := r.Context()
	before := h.store.RemoteAccess(ctx)
	cfg := settings.RemoteAccessConfig{MapPort: body.MapPort, ExternalPort: body.ExternalPort, Protocol: body.Protocol}
	if err := h.store.SetRemoteAccess(ctx, cfg); err != nil {
		if errors.Is(err, settings.ErrInvalidRemoteAccessConfig) {
			respond.ValidationError(w, r, err.Error())
			return
		}
		h.logger.ErrorContext(ctx, "update remote access", "err", err)
		respond.InternalError(w, r)
		return
	}
	saved := cfg.WithDefaults()
	if saved.MapPort != before.MapPort || saved.ExternalPort != before.ExternalPort {
		h.logger.InfoContext(ctx, "remote access setting changed",
			"map_port", saved.MapPort, "external_port", saved.ExternalPort)
	}
	if h.audit != nil {
		if claims := middleware.ClaimsFromContext(ctx); claims != nil {
			h.audit.Log(ctx, &claims.UserID, audit.ActionSettingsUpdate, "remote_access", map[string]any{
				"remote_access_map_port":      saved.MapPort,
				"remote_access_external_port": saved.ExternalPort,
				"remote_access_protocol":      saved.Protocol,
			}, audit.ClientIP(r))
		}
	}
	if h.mapper != nil {
		h.mapper.Kick()
	}
	respond.Success(w, r, h.view(ctx))
}
