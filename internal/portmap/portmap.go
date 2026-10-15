// Package portmap asks the home router to forward the server's port from the
// internet ("Remote access → map a port automatically"). It speaks the three
// protocols consumer routers offer — UPnP IGD (v1/v2, WANIPConnection and
// WANPPPConnection), PCP and NAT-PMP — requests a lease on this host's LAN
// address and the API listen port, renews it before it runs out, and removes
// it when the setting is turned off or the server stops.
//
// It is security-sensitive by nature: a mapping makes the server reachable
// from the whole internet. So it maps only the OnScreen listen port, never
// anything when the server listens on loopback only, logs every mapping it
// adds, renews or removes, and only one node of a cluster runs it (the master
// instance, see cmd/server). Discovery is SSDP multicast on the LAN and a
// unicast to the default gateway; nothing leaves the local network.
package portmap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Mapping methods, as Status reports them.
const (
	MethodUPnP   = "upnp"
	MethodPCP    = "pcp"
	MethodNATPMP = "nat-pmp"
)

// ProtocolTCP is the only transport mapped: the API is HTTP(S).
const ProtocolTCP = "TCP"

// Mapping is one port forward on the router.
type Mapping struct {
	// Protocol is "TCP".
	Protocol string
	// InternalIP:InternalPort is where the router sends the traffic — this
	// host's LAN address and the API listen port.
	InternalIP   net.IP
	InternalPort int
	// ExternalPort is the port asked for on the router's public address.
	ExternalPort int
	// Lease is how long the mapping is asked to last.
	Lease time.Duration
	// Description labels the mapping in the router's UI.
	Description string
}

// Granted is what the router actually gave.
type Granted struct {
	// ExternalPort is the public port (NAT-PMP / PCP may assign another one
	// than asked for).
	ExternalPort int
	// Lease is the lifetime granted; 0 = permanent (a UPnP router that only
	// does permanent leases).
	Lease time.Duration
}

// Gateway is a router found on the LAN that can map ports.
type Gateway interface {
	// Method names the protocol (MethodUPnP, MethodPCP, MethodNATPMP).
	Method() string
	// Addr is the router's address, for the status page.
	Addr() string
	// LocalIP is this host's address on the router's network.
	LocalIP() net.IP
	// ExternalIP is the router's public address.
	ExternalIP(ctx context.Context) (net.IP, error)
	// AddMapping creates or refreshes the mapping.
	AddMapping(ctx context.Context, m Mapping) (Granted, error)
	// DeleteMapping removes it.
	DeleteMapping(ctx context.Context, m Mapping) error
}

// Discoverer finds the router.
type Discoverer interface {
	Discover(ctx context.Context) (Gateway, error)
}

// ErrNoGateway means no router answered on any protocol.
var ErrNoGateway = errors.New("no UPnP, PCP or NAT-PMP router found on the local network")

// Settings is the admin's choice (Settings ▸ Remote access).
type Settings struct {
	Enabled bool
	// ExternalPort is the public port; 0 = the same as the listen port.
	ExternalPort int
}

// States, as Status reports them.
const (
	StateDisabled     = "disabled"      // the setting is off
	StateLoopbackOnly = "loopback_only" // the server listens on loopback: never mapped
	StateStandby      = "standby"       // another node of the cluster does the mapping
	StateNoGateway    = "no_gateway"    // nothing answered discovery
	StateMapped       = "mapped"        // a mapping is in place
	StateError        = "error"         // the router refused or failed
)

// Status is the mapping's state for the admin.
type Status struct {
	Enabled      bool       `json:"enabled"`
	State        string     `json:"state"`
	GatewayFound bool       `json:"gateway_found"`
	Method       string     `json:"method,omitempty"`
	GatewayAddr  string     `json:"gateway_addr,omitempty"`
	LocalIP      string     `json:"local_ip,omitempty"`
	InternalPort int        `json:"internal_port,omitempty"`
	ExternalIP   string     `json:"external_ip,omitempty"`
	ExternalPort int        `json:"external_port,omitempty"`
	LeaseSeconds int        `json:"lease_seconds,omitempty"`
	Permanent    bool       `json:"permanent,omitempty"`
	LastRenewal  *time.Time `json:"last_renewal,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	LastErrorAt  *time.Time `json:"last_error_at,omitempty"`
	// Node is the cluster node doing the mapping.
	Node      string    `json:"node,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Config is the mapper's fixed configuration.
type Config struct {
	// ListenAddr is the API server's listen address (LISTEN_ADDR, ":7070").
	ListenAddr string
	// Node names this node in the status (NODE_ID or the host name).
	Node string
	// Lease is the lifetime asked for; renewed at half of what is granted.
	// Default 1 h.
	Lease time.Duration
	// CheckEvery is how often the setting is re-read. Default 30 s.
	CheckEvery time.Duration
	// Description labels the mapping on the router. Default "OnScreen".
	Description string
}

const (
	defaultLease      = time.Hour
	defaultCheckEvery = 30 * time.Second
	// permanentReassert: a permanent mapping (lease 0) is re-added this
	// often, so one a router dropped on reboot comes back.
	permanentReassert = 30 * time.Minute
	minBackoff        = time.Minute
	maxBackoff        = 15 * time.Minute
	// removeTimeout bounds the delete on shutdown.
	removeTimeout = 5 * time.Second
)

// Mapper keeps the router's port mapping in line with the setting.
type Mapper struct {
	cfg      Config
	settings func(ctx context.Context) Settings
	disc     Discoverer
	logger   *slog.Logger
	now      func() time.Time
	// publish shares the status with the other nodes (optional).
	publish func(ctx context.Context, s Status)

	kick chan struct{}

	// mu guards what other goroutines read: the published status and
	// whether Run is active.
	mu      sync.Mutex
	status  Status
	running bool

	// The rest belongs to the Run goroutine alone (and to tests driving
	// step directly), so a slow discovery never blocks a status read.
	work     Status // the status being built; committed after each step
	gw       Gateway
	mapped   *Mapping // the mapping in place, as asked for
	granted  Granted
	renewAt  time.Time
	backoff  time.Duration
	retryAt  time.Time
	lastSeen Settings
}

// New builds a mapper. settings is read on every check (the live setting).
func New(cfg Config, settings func(ctx context.Context) Settings, disc Discoverer, logger *slog.Logger) *Mapper {
	if cfg.Lease <= 0 {
		cfg.Lease = defaultLease
	}
	if cfg.CheckEvery <= 0 {
		cfg.CheckEvery = defaultCheckEvery
	}
	if cfg.Description == "" {
		cfg.Description = "OnScreen"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Mapper{
		cfg: cfg, settings: settings, disc: disc, logger: logger, now: time.Now,
		kick:   make(chan struct{}, 1),
		status: Status{State: StateStandby},
		work:   Status{State: StateStandby},
	}
}

// WithPublisher shares every status change (cmd/server: through Valkey, so
// the node an admin's request lands on can show the master's status).
func (m *Mapper) WithPublisher(p func(ctx context.Context, s Status)) *Mapper {
	m.publish = p
	return m
}

// Kick re-checks the setting now (it was just changed).
func (m *Mapper) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Running reports whether this node is the one mapping (Run is active).
func (m *Mapper) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// Status returns this node's view of the mapping.
func (m *Mapper) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Run keeps the mapping until ctx is cancelled, then removes it. Called on
// the master node only; losing mastership cancels ctx, so the mapping moves
// with the master.
func (m *Mapper) Run(ctx context.Context) {
	m.mu.Lock()
	m.running = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
	}()
	defer m.shutdown()

	for {
		wait := m.step(ctx)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-m.kick:
			t.Stop()
			m.retryAt = time.Time{} // a settings change retries at once
		case <-t.C:
		}
	}
}

// shutdown removes the mapping (server stopping or no longer master).
func (m *Mapper) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), removeTimeout)
	defer cancel()
	if m.mapped != nil {
		m.unmap(ctx, "server stopping")
	}
	m.setIdle(StateStandby, "")
	m.work.UpdatedAt = m.now()
	m.commit(ctx)
}

// commit publishes the status step built.
func (m *Mapper) commit(ctx context.Context) {
	m.mu.Lock()
	m.status = m.work
	st := m.status
	m.mu.Unlock()
	if m.publish != nil {
		m.publish(ctx, st)
	}
}

// step runs one check and returns how long to wait before the next.
func (m *Mapper) step(ctx context.Context) time.Duration {
	s := m.settings(ctx)
	defer m.commit(ctx)
	now := m.now()
	m.work.Enabled = s.Enabled
	m.work.Node = m.cfg.Node
	m.work.UpdatedAt = now
	if s != m.lastSeen {
		m.retryAt = time.Time{}
		m.backoff = 0
		m.lastSeen = s
	}

	if !s.Enabled {
		if m.mapped != nil {
			m.unmap(ctx, "setting turned off")
		}
		m.setIdle(StateDisabled, "")
		return m.cfg.CheckEvery
	}

	host, port, err := ParseListenAddr(m.cfg.ListenAddr)
	if err != nil {
		m.setIdle(StateError, err.Error())
		return m.cfg.CheckEvery
	}
	if IsLoopbackHost(host) {
		if m.mapped != nil {
			m.unmap(ctx, "server listens on loopback only")
		}
		m.setIdle(StateLoopbackOnly, "the server listens on "+m.cfg.ListenAddr+
			" (loopback only), so nothing outside this machine could use a mapping")
		return m.cfg.CheckEvery
	}
	ext := s.ExternalPort
	if ext <= 0 {
		ext = port
	}
	// The admin changed the public port: drop the old mapping first, so
	// exactly one port stays open.
	if m.mapped != nil && m.mapped.ExternalPort != ext {
		m.unmap(ctx, "external port changed")
	}

	if !m.retryAt.IsZero() && now.Before(m.retryAt) {
		return m.retryAt.Sub(now)
	}
	if m.mapped != nil && now.Before(m.renewAt) {
		return min(m.cfg.CheckEvery, m.renewAt.Sub(now))
	}

	if m.gw == nil {
		gw, err := m.disc.Discover(ctx)
		if err != nil {
			m.work.GatewayFound = false
			m.work.Method, m.work.GatewayAddr = "", ""
			state := StateError
			if errors.Is(err, ErrNoGateway) {
				state = StateNoGateway
			}
			return m.fail(now, state, fmt.Errorf("discover router: %w", err))
		}
		m.gw = gw
		m.work.GatewayFound = true
		m.work.Method = gw.Method()
		m.work.GatewayAddr = gw.Addr()
		m.logger.InfoContext(ctx, "remote access: router found", "method", gw.Method(), "gateway", gw.Addr(), "local_ip", gw.LocalIP())
	}

	internalIP := m.gw.LocalIP()
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		internalIP = ip // bound to one address: that's the one to forward to
	}
	if internalIP == nil {
		return m.fail(now, StateError, errors.New("couldn't tell this host's LAN address"))
	}
	want := Mapping{
		Protocol: ProtocolTCP, InternalIP: internalIP, InternalPort: port,
		ExternalPort: ext, Lease: m.cfg.Lease, Description: m.cfg.Description,
	}
	renewal := m.mapped != nil
	granted, err := m.gw.AddMapping(ctx, want)
	if err != nil {
		if m.mapped != nil && !m.renewAt.IsZero() && m.granted.Lease > 0 &&
			now.After(m.renewAt.Add(m.granted.Lease/2)) {
			m.mapped = nil // the lease has run out on the router by now
		}
		m.gw = nil // rediscover next time: the router may have changed
		return m.fail(now, StateError, fmt.Errorf("map port %d: %w", ext, err))
	}
	if granted.ExternalPort == 0 {
		granted.ExternalPort = ext
	}
	m.mapped, m.granted = &want, granted
	m.backoff, m.retryAt = 0, time.Time{}
	if granted.Lease > 0 {
		m.renewAt = now.Add(granted.Lease / 2)
	} else {
		m.renewAt = now.Add(permanentReassert)
	}
	verb := "added"
	if renewal {
		verb = "renewed"
	}
	m.logger.InfoContext(ctx, "remote access: port mapping "+verb,
		"method", m.gw.Method(), "gateway", m.gw.Addr(), "protocol", want.Protocol,
		"external_port", granted.ExternalPort, "internal", net.JoinHostPort(internalIP.String(), fmt.Sprint(port)),
		"lease", granted.Lease)

	m.work.State = StateMapped
	m.work.LocalIP = internalIP.String()
	m.work.InternalPort = port
	m.work.ExternalPort = granted.ExternalPort
	m.work.LeaseSeconds = int(granted.Lease / time.Second)
	m.work.Permanent = granted.Lease == 0
	renewed := now
	m.work.LastRenewal = &renewed
	if granted.Lease > 0 {
		exp := now.Add(granted.Lease)
		m.work.ExpiresAt = &exp
	} else {
		m.work.ExpiresAt = nil
	}
	m.work.LastError, m.work.LastErrorAt = "", nil
	if ip, err := m.gw.ExternalIP(ctx); err == nil && ip != nil {
		m.work.ExternalIP = ip.String()
	} else if err != nil {
		m.logger.DebugContext(ctx, "remote access: external address", "err", err)
	}
	return min(m.cfg.CheckEvery, m.renewAt.Sub(now))
}

// unmap removes the mapping in place. A failure is logged and
// forgotten: the router drops the lease when it runs out anyway.
func (m *Mapper) unmap(ctx context.Context, why string) {
	cur := m.mapped
	m.mapped = nil
	m.renewAt = time.Time{}
	if cur == nil || m.gw == nil {
		return
	}
	gm := *cur
	gm.ExternalPort = m.granted.ExternalPort
	if gm.ExternalPort == 0 {
		gm.ExternalPort = cur.ExternalPort
	}
	if err := m.gw.DeleteMapping(ctx, gm); err != nil {
		m.logger.WarnContext(ctx, "remote access: removing port mapping failed; it ends when its lease runs out",
			"method", m.gw.Method(), "gateway", m.gw.Addr(), "external_port", gm.ExternalPort,
			"reason", why, "err", err)
		return
	}
	m.logger.InfoContext(ctx, "remote access: port mapping removed",
		"method", m.gw.Method(), "gateway", m.gw.Addr(), "protocol", gm.Protocol,
		"external_port", gm.ExternalPort, "reason", why)
}

// setIdle records a state with no mapping.
func (m *Mapper) setIdle(state, msg string) {
	m.work.State = state
	m.work.ExternalPort, m.work.InternalPort = 0, 0
	m.work.LeaseSeconds, m.work.Permanent = 0, false
	m.work.ExpiresAt = nil
	if msg != "" {
		t := m.now()
		m.work.LastError, m.work.LastErrorAt = msg, &t
	} else {
		m.work.LastError, m.work.LastErrorAt = "", nil
	}
}

// fail records an error and returns the backoff before the retry.
func (m *Mapper) fail(now time.Time, state string, err error) time.Duration {
	if m.mapped == nil {
		m.setIdle(state, "")
	} else {
		m.work.State = state
	}
	m.work.LastError, m.work.LastErrorAt = err.Error(), &now
	if m.backoff == 0 {
		m.backoff = minBackoff
	} else {
		m.backoff = min(m.backoff*2, maxBackoff)
	}
	m.retryAt = now.Add(m.backoff)
	m.logger.Warn("remote access: port mapping failed", "err", err, "retry_in", m.backoff)
	return m.backoff
}
