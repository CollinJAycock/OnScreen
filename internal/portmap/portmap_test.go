package portmap

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGateway records what the mapper asks of the router.
type fakeGateway struct {
	mu       sync.Mutex
	adds     []Mapping
	deletes  []Mapping
	addErr   error
	grant    time.Duration // lease granted; <0 = echo the request
	extPort  int           // port granted; 0 = as asked
	external net.IP
}

func (g *fakeGateway) Method() string  { return MethodNATPMP }
func (g *fakeGateway) Addr() string    { return "192.168.1.1" }
func (g *fakeGateway) LocalIP() net.IP { return net.ParseIP("192.168.1.20") }
func (g *fakeGateway) ExternalIP(context.Context) (net.IP, error) {
	if g.external == nil {
		return nil, errors.New("unknown")
	}
	return g.external, nil
}
func (g *fakeGateway) AddMapping(_ context.Context, m Mapping) (Granted, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.addErr != nil {
		return Granted{}, g.addErr
	}
	g.adds = append(g.adds, m)
	lease := g.grant
	if lease < 0 {
		lease = m.Lease
	}
	return Granted{ExternalPort: g.extPort, Lease: lease}, nil
}
func (g *fakeGateway) DeleteMapping(_ context.Context, m Mapping) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.deletes = append(g.deletes, m)
	return nil
}

type fakeDiscoverer struct {
	gw    Gateway
	err   error
	calls int
}

func (d *fakeDiscoverer) Discover(context.Context) (Gateway, error) {
	d.calls++
	if d.err != nil {
		return nil, d.err
	}
	return d.gw, nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

type harness struct {
	m        *Mapper
	gw       *fakeGateway
	disc     *fakeDiscoverer
	clk      *clock
	settings Settings
	pub      []Status
}

func newHarness(t *testing.T, listen string) *harness {
	t.Helper()
	h := &harness{
		gw:  &fakeGateway{grant: -1, external: net.ParseIP("203.0.113.7")},
		clk: &clock{t: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)},
	}
	h.disc = &fakeDiscoverer{gw: h.gw}
	h.m = New(Config{ListenAddr: listen, Node: "node-a", Lease: time.Hour},
		func(context.Context) Settings { return h.settings }, h.disc, nil)
	h.m.now = h.clk.now
	h.m.WithPublisher(func(_ context.Context, s Status) { h.pub = append(h.pub, s) })
	return h
}

func TestMapper_DisabledMapsNothing(t *testing.T) {
	h := newHarness(t, ":7070")
	h.m.step(context.Background())
	if len(h.gw.adds) != 0 || h.disc.calls != 0 {
		t.Fatalf("disabled: adds=%d discover=%d, want none", len(h.gw.adds), h.disc.calls)
	}
	if st := h.m.Status(); st.State != StateDisabled || st.Enabled {
		t.Fatalf("status = %+v, want disabled", st)
	}
}

func TestMapper_MapsListenPortAndRenewsBeforeExpiry(t *testing.T) {
	h := newHarness(t, ":7070")
	h.settings = Settings{Enabled: true}
	ctx := context.Background()

	wait := h.m.step(ctx)
	if len(h.gw.adds) != 1 {
		t.Fatalf("adds = %d, want 1", len(h.gw.adds))
	}
	got := h.gw.adds[0]
	if got.Protocol != ProtocolTCP || got.InternalPort != 7070 || got.ExternalPort != 7070 ||
		!got.InternalIP.Equal(net.ParseIP("192.168.1.20")) || got.Lease != time.Hour {
		t.Fatalf("mapping = %+v", got)
	}
	st := h.m.Status()
	if st.State != StateMapped || st.ExternalIP != "203.0.113.7" || st.ExternalPort != 7070 ||
		st.LeaseSeconds != 3600 || st.LastRenewal == nil || st.ExpiresAt == nil || st.Node != "node-a" {
		t.Fatalf("status = %+v", st)
	}
	if wait > 30*time.Second {
		t.Fatalf("next check in %v, want at most the check interval", wait)
	}
	if len(h.pub) == 0 || h.pub[len(h.pub)-1].State != StateMapped {
		t.Fatal("status not published")
	}

	// Before half the lease: no renewal.
	h.clk.advance(29 * time.Minute)
	h.m.step(ctx)
	if len(h.gw.adds) != 1 {
		t.Fatalf("renewed early: adds = %d", len(h.gw.adds))
	}
	// Past half the lease: renewed, well before it expires.
	h.clk.advance(2 * time.Minute)
	h.m.step(ctx)
	if len(h.gw.adds) != 2 {
		t.Fatalf("not renewed at half-lease: adds = %d", len(h.gw.adds))
	}
	if h.disc.calls != 1 {
		t.Fatalf("rediscovered on renewal: %d calls", h.disc.calls)
	}
}

func TestMapper_ShortGrantedLeaseRenewsAtHalf(t *testing.T) {
	h := newHarness(t, ":7070")
	h.settings = Settings{Enabled: true}
	h.gw.grant = 10 * time.Minute // NAT-PMP routers may cut the lease
	ctx := context.Background()
	h.m.step(ctx)
	h.clk.advance(5*time.Minute + time.Second)
	h.m.step(ctx)
	if len(h.gw.adds) != 2 {
		t.Fatalf("adds = %d, want renewal at half the granted lease", len(h.gw.adds))
	}
}

func TestMapper_CustomExternalPortAndPortChange(t *testing.T) {
	h := newHarness(t, "0.0.0.0:7070")
	h.settings = Settings{Enabled: true, ExternalPort: 32400}
	ctx := context.Background()
	h.m.step(ctx)
	if h.gw.adds[0].ExternalPort != 32400 || h.gw.adds[0].InternalPort != 7070 {
		t.Fatalf("mapping = %+v", h.gw.adds[0])
	}
	// The admin picks another public port: the old mapping goes first.
	h.settings.ExternalPort = 40000
	h.m.step(ctx)
	if len(h.gw.deletes) != 1 || h.gw.deletes[0].ExternalPort != 32400 {
		t.Fatalf("deletes = %+v, want the old 32400 mapping removed", h.gw.deletes)
	}
	if len(h.gw.adds) != 2 || h.gw.adds[1].ExternalPort != 40000 {
		t.Fatalf("adds = %+v", h.gw.adds)
	}
}

func TestMapper_GrantedPortIsTheOneDeleted(t *testing.T) {
	h := newHarness(t, ":7070")
	h.settings = Settings{Enabled: true}
	h.gw.extPort = 51234 // the router assigned a different public port
	ctx := context.Background()
	h.m.step(ctx)
	if st := h.m.Status(); st.ExternalPort != 51234 {
		t.Fatalf("status port = %d, want the granted 51234", st.ExternalPort)
	}
	h.settings.Enabled = false
	h.m.step(ctx)
	if len(h.gw.deletes) != 1 || h.gw.deletes[0].ExternalPort != 51234 {
		t.Fatalf("deletes = %+v, want 51234", h.gw.deletes)
	}
}

func TestMapper_TurnedOffRemovesMapping(t *testing.T) {
	h := newHarness(t, ":7070")
	h.settings = Settings{Enabled: true}
	ctx := context.Background()
	h.m.step(ctx)
	h.settings.Enabled = false
	h.m.step(ctx)
	if len(h.gw.deletes) != 1 {
		t.Fatalf("deletes = %d, want 1", len(h.gw.deletes))
	}
	if st := h.m.Status(); st.State != StateDisabled || st.ExternalPort != 0 {
		t.Fatalf("status = %+v", st)
	}
	// Still off: nothing more.
	h.m.step(ctx)
	if len(h.gw.deletes) != 1 || len(h.gw.adds) != 1 {
		t.Fatalf("adds=%d deletes=%d after a second off check", len(h.gw.adds), len(h.gw.deletes))
	}
}

func TestMapper_RunRemovesMappingOnShutdown(t *testing.T) {
	h := newHarness(t, ":7070")
	h.settings = Settings{Enabled: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.m.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.gw.mu.Lock()
		n := len(h.gw.adds)
		h.gw.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never mapped")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !h.m.Running() {
		t.Fatal("Running() = false while Run is active")
	}
	cancel()
	<-done
	h.gw.mu.Lock()
	defer h.gw.mu.Unlock()
	if len(h.gw.deletes) != 1 {
		t.Fatalf("deletes on shutdown = %d, want 1", len(h.gw.deletes))
	}
	if h.m.Running() {
		t.Fatal("Running() = true after Run returned")
	}
	if st := h.m.Status(); st.State != StateStandby {
		t.Fatalf("status after shutdown = %s, want standby", st.State)
	}
}

func TestMapper_NeverMapsLoopbackOnly(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:7070", "localhost:7070", "[::1]:7070"} {
		h := newHarness(t, listen)
		h.settings = Settings{Enabled: true}
		h.m.step(context.Background())
		if len(h.gw.adds) != 0 || h.disc.calls != 0 {
			t.Fatalf("%s: mapped a loopback-only server", listen)
		}
		if st := h.m.Status(); st.State != StateLoopbackOnly || st.LastError == "" {
			t.Fatalf("%s: status = %+v", listen, st)
		}
	}
}

func TestMapper_BoundAddressIsTheInternalClient(t *testing.T) {
	h := newHarness(t, "192.168.1.50:7070")
	h.settings = Settings{Enabled: true}
	h.m.step(context.Background())
	if got := h.gw.adds[0].InternalIP; !got.Equal(net.ParseIP("192.168.1.50")) {
		t.Fatalf("internal client = %v, want the bound 192.168.1.50", got)
	}
}

func TestMapper_NoGatewayBacksOff(t *testing.T) {
	h := newHarness(t, ":7070")
	h.settings = Settings{Enabled: true}
	h.disc.err = ErrNoGateway
	ctx := context.Background()
	wait := h.m.step(ctx)
	if st := h.m.Status(); st.State != StateNoGateway || st.LastError == "" || st.GatewayFound {
		t.Fatalf("status = %+v", st)
	}
	if wait != minBackoff {
		t.Fatalf("backoff = %v, want %v", wait, minBackoff)
	}
	// Inside the backoff: no new discovery.
	h.clk.advance(30 * time.Second)
	h.m.step(ctx)
	if h.disc.calls != 1 {
		t.Fatalf("discovered during backoff: %d calls", h.disc.calls)
	}
	// After it: tries again, and the backoff doubles.
	h.clk.advance(31 * time.Second)
	wait = h.m.step(ctx)
	if h.disc.calls != 2 || wait != 2*minBackoff {
		t.Fatalf("calls=%d wait=%v", h.disc.calls, wait)
	}
	// A router appears; a settings change retries at once.
	h.disc.err = nil
	h.settings.ExternalPort = 8443
	h.m.step(ctx)
	if len(h.gw.adds) != 1 {
		t.Fatalf("adds = %d after the router appeared", len(h.gw.adds))
	}
}

func TestMapper_RefusedMappingReportsAndRediscovers(t *testing.T) {
	h := newHarness(t, ":7070")
	h.settings = Settings{Enabled: true}
	h.gw.addErr = errors.New("ConflictInMappingEntry")
	ctx := context.Background()
	h.m.step(ctx)
	st := h.m.Status()
	if st.State != StateError || st.LastError == "" || st.LastErrorAt == nil {
		t.Fatalf("status = %+v", st)
	}
	h.gw.addErr = nil
	h.clk.advance(2 * time.Minute)
	h.m.step(ctx)
	if h.disc.calls != 2 {
		t.Fatalf("discover calls = %d, want a fresh discovery after a failure", h.disc.calls)
	}
	if st := h.m.Status(); st.State != StateMapped || st.LastError != "" {
		t.Fatalf("status after recovery = %+v", st)
	}
}

func TestMapper_PermanentLeaseIsReasserted(t *testing.T) {
	h := newHarness(t, ":7070")
	h.settings = Settings{Enabled: true}
	h.gw.grant = 0 // the router only does permanent mappings
	ctx := context.Background()
	h.m.step(ctx)
	if st := h.m.Status(); !st.Permanent || st.ExpiresAt != nil {
		t.Fatalf("status = %+v", st)
	}
	h.clk.advance(permanentReassert + time.Second)
	h.m.step(ctx)
	if len(h.gw.adds) != 2 {
		t.Fatalf("adds = %d, want the permanent mapping re-asserted", len(h.gw.adds))
	}
}

func TestParseListenAddr(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
		bad  bool
	}{
		{":7070", "", 7070, false},
		{"0.0.0.0:8443", "0.0.0.0", 8443, false},
		{"[::]:7070", "::", 7070, false},
		{"192.168.1.5:80", "192.168.1.5", 80, false},
		{"7070", "", 0, true},
		{":0", "", 0, true},
		{":99999", "", 0, true},
	}
	for _, c := range cases {
		h, p, err := ParseListenAddr(c.in)
		if (err != nil) != c.bad || (!c.bad && (h != c.host || p != c.port)) {
			t.Errorf("ParseListenAddr(%q) = %q, %d, %v", c.in, h, p, err)
		}
	}
}

func TestReachabilityHint(t *testing.T) {
	st := Status{State: StateMapped, ExternalIP: "203.0.113.7", ExternalPort: 7070}
	if got := ReachabilityHint(st, true); !strings.Contains(got, "https://203.0.113.7:7070") {
		t.Fatalf("hint = %q", got)
	}
	st.ExternalIP = "100.72.1.2"
	if got := ReachabilityHint(st, false); !strings.Contains(got, "carrier-grade NAT") {
		t.Fatalf("CGNAT hint = %q", got)
	}
	st.ExternalIP = "192.168.0.2"
	if got := ReachabilityHint(st, false); !strings.Contains(got, "second router") {
		t.Fatalf("double-NAT hint = %q", got)
	}
	if got := ReachabilityHint(Status{State: StateNoGateway}, false); !strings.Contains(got, "UPnP") {
		t.Fatalf("no-gateway hint = %q", got)
	}
}

type memKV map[string]string

func (m memKV) Set(_ context.Context, k string, v any, _ time.Duration) error {
	m[k] = v.(string)
	return nil
}
func (m memKV) Get(_ context.Context, k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", errors.New("nil")
	}
	return v, nil
}

func TestSharedStatus_RoundTrip(t *testing.T) {
	kv := memKV{}
	s := SharedStatus{KV: kv}
	if _, ok := s.Load(context.Background()); ok {
		t.Fatal("loaded a status before any was published")
	}
	s.Publish(context.Background(), Status{State: StateMapped, ExternalPort: 7070, Node: "a"})
	got, ok := s.Load(context.Background())
	if !ok || got.State != StateMapped || got.ExternalPort != 7070 || got.Node != "a" {
		t.Fatalf("loaded %+v, %v", got, ok)
	}
}
