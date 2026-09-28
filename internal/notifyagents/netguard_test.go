package notifyagents

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateDiscordWebhookURL(t *testing.T) {
	ok := []string{
		"https://discord.com/api/webhooks/123456789012345678/AbC-dEf_123",
		"https://discordapp.com/api/webhooks/1/x",
		"https://DISCORD.com/api/webhooks/1/x",
		"https://discord.com:443/api/webhooks/1/x",
		"https://discord.com/api/v10/webhooks/1/x",
		"https://discord.com/api/webhooks/1/x?thread_id=987",
	}
	for _, u := range ok {
		if err := validateDiscordWebhookURL(u); err != nil {
			t.Errorf("%s: unexpected error %v", u, err)
		}
	}
	bad := []string{
		"http://discord.com/api/webhooks/1/x",               // not https
		"https://discord.com.evil.example/api/webhooks/1/x", // suffix trick
		"https://evil.example/api/webhooks/1/x",             // other host
		"https://ptb.discord.com/api/webhooks/1/x",          // not on the allow-list
		"https://discord.com:8443/api/webhooks/1/x",         // odd port
		"https://user:pw@discord.com/api/webhooks/1/x",      // credentials
		"https://discord.com/api/webhooks/abc/x",            // non-numeric id
		"https://discord.com/api/webhooks/1/x/../../users",  // path games
		"https://discord.com/api/webhooks/1/x?wait=true",    // other query
		"https://discord.com/api/webhooks/1/x#frag",         // fragment
		"https://127.0.0.1/api/webhooks/1/x",                // IP literal
		"not a url",
	}
	for _, u := range bad {
		err := validateDiscordWebhookURL(u)
		var verr *ValidationError
		if !errors.As(err, &verr) {
			t.Errorf("%s: want ValidationError, got %v", u, err)
		}
	}
}

func TestNormalizeServerURL(t *testing.T) {
	cases := []struct {
		raw          string
		allowPrivate bool
		want         string
		wantErr      bool
	}{
		{"https://ntfy.sh/", false, "https://ntfy.sh", false},
		{" https://push.example.com/gotify/ ", false, "https://push.example.com/gotify", false},
		{"http://ntfy.lan:8080", false, "", true}, // http only on the LAN
		{"http://ntfy.lan:8080", true, "http://ntfy.lan:8080", false},
		{"ftp://ntfy.sh", true, "", true},
		{"https://u:p@ntfy.sh", false, "", true},
		{"https://ntfy.sh/?x=1", false, "", true},
		{"", false, "", true},
		{"ntfy.sh", false, "", true},
	}
	for _, c := range cases {
		got, err := normalizeServerURL(c.raw, c.allowPrivate)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("normalizeServerURL(%q, %v) = %q, %v; want %q, err=%v", c.raw, c.allowPrivate, got, err, c.want, c.wantErr)
		}
	}
}

func TestCheckResolvedHost(t *testing.T) {
	lookup := func(ips ...string) func(context.Context, string) ([]string, error) {
		return func(context.Context, string) ([]string, error) { return ips, nil }
	}
	ctx := context.Background()
	if err := checkResolvedHost(ctx, lookup("93.184.216.34"), "ntfy.example.com", netPublic); err != nil {
		t.Errorf("public host rejected: %v", err)
	}
	for _, ip := range []string{"10.0.0.5", "192.168.1.2", "100.64.0.1", "127.0.0.1", "169.254.169.254"} {
		if err := checkResolvedHost(ctx, lookup("93.184.216.34", ip), "rebind.example.com", netPublic); err == nil {
			t.Errorf("%s accepted without allow_private_network", ip)
		}
	}
	for _, ip := range []string{"10.0.0.5", "100.64.0.1", "127.0.0.1"} {
		if err := checkResolvedHost(ctx, lookup(ip), "ntfy.lan", netPrivateAllowed); err != nil {
			t.Errorf("%s rejected with allow_private_network: %v", ip, err)
		}
	}
	// Link-local (cloud metadata) stays blocked even for a LAN server.
	if err := checkResolvedHost(ctx, lookup("169.254.169.254"), "meta", netPrivateAllowed); err == nil {
		t.Error("link-local accepted with allow_private_network")
	}
	// IP literals skip DNS.
	if err := checkResolvedHost(ctx, nil, "10.1.2.3", netPublic); err == nil {
		t.Error("private IP literal accepted")
	}
	fail := func(context.Context, string) ([]string, error) { return nil, errors.New("nxdomain") }
	if err := checkResolvedHost(ctx, fail, "nope.invalid", netPublic); err == nil {
		t.Error("unresolvable host accepted")
	}
}

// TestHTTPClient_DialGuard: the real delivery client refuses a loopback
// target (httptest) at dial time unless the agent allows the private
// network — the DNS-rebinding-safe check, not the save-time one.
func TestHTTPClient_DialGuard(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Service{sendTimeout: 2 * time.Second}
	out := &outbound{method: "POST", url: srv.URL + "/topic", header: http.Header{}}
	err := s.do(context.Background(), newHTTPClient(netPublic, 2*1e9), out, "")
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("loopback delivery without allow_private_network: err = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("blocked request reached the server")
	}
	if err := s.do(context.Background(), newHTTPClient(netPrivateAllowed, 2*1e9), out, ""); err != nil {
		t.Fatalf("loopback delivery with allow_private_network: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits = %d, want 1", hits.Load())
	}
}

// TestDelivery_RedirectNotFollowed: a 3xx is a (permanent) failure; the POST
// never reaches the redirect target.
func TestDelivery_RedirectNotFollowed(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHits.Add(1)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	s := &Service{sendTimeout: 2 * time.Second}
	err := s.do(context.Background(), newHTTPClient(netPrivateAllowed, 2*1e9),
		&outbound{method: "POST", url: redirector.URL + "/topic", header: http.Header{}}, "")
	var perm *permanentError
	if !errors.As(err, &perm) || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("err = %v, want a permanent redirect error", err)
	}
	if targetHits.Load() != 0 {
		t.Fatal("redirect was followed")
	}
}

// TestSend_DiscordAndTelegramNeverPrivate: even a row that claims
// allow_private_network (written around the API) gets the public-only
// client for Discord and Telegram.
func TestSend_DiscordAndTelegramNeverPrivate(t *testing.T) {
	db := newFakeDB()
	s := testService(t, db, "")
	var asked []netPolicy
	s.clientFor = func(p netPolicy) *http.Client {
		asked = append(asked, p)
		return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("stop")
		})}
	}
	for _, k := range []Kind{KindDiscord, KindTelegram, KindNtfy} {
		id := uuid.New()
		secret := map[Kind]string{
			KindDiscord:  "https://discord.com/api/webhooks/1/x",
			KindTelegram: "123:abcdefghijklmnopqrstuvwxyz",
			KindNtfy:     "",
		}[k]
		sealed, _ := s.seal(id, secret)
		db.agents[id] = genAgent(id, k, sealed, `{"server_url":"http://ntfy.lan","topic":"t","chat_id":"1"}`, true)
		_ = s.send(context.Background(), db.agents[id], Message{Event: EventTest, Title: "t"})
	}
	// The ntfy row is plain http with the opt-in: private-only.
	if want := []netPolicy{netPublic, netPublic, netPrivateOnly}; !slices.Equal(asked, want) {
		t.Errorf("policy per send = %v, want %v", asked, want)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestPlainHTTP_PrivateAddressesOnly: allow_private_network makes plain http
// acceptable only for a server on the LAN. Every resolved address must be
// private or loopback — at save (the admin's error) and at every dial (the
// real guard: DNS can change) — while https keeps the opt-in's wider rule.
func TestPlainHTTP_PrivateAddressesOnly(t *testing.T) {
	lookup := func(ips ...string) func(context.Context, string) ([]string, error) {
		return func(context.Context, string) ([]string, error) { return ips, nil }
	}
	ctx := context.Background()
	for _, ip := range []string{"10.0.0.5", "192.168.1.2", "100.64.0.1", "127.0.0.1", "::1", "fd00::1"} {
		if err := checkResolvedHost(ctx, lookup(ip), "ntfy.lan", netPrivateOnly); err != nil {
			t.Errorf("http to %s rejected: %v", ip, err)
		}
	}
	for _, ips := range [][]string{{"93.184.216.34"}, {"10.0.0.5", "93.184.216.34"}, {"2606:4700::1111"}} {
		err := checkResolvedHost(ctx, lookup(ips...), "ntfy.example.com", netPrivateOnly)
		var verr *ValidationError
		if !errors.As(err, &verr) || !strings.Contains(verr.Msg, "public address") || !strings.Contains(verr.Msg, "https") {
			t.Errorf("http to %v: err = %v, want a public-address validation error", ips, err)
		}
	}
	if err := checkResolvedHost(ctx, lookup("169.254.169.254"), "meta", netPrivateOnly); err == nil {
		t.Error("link-local accepted for http")
	}
	if err := checkResolvedHost(ctx, nil, "93.184.216.34", netPrivateOnly); err == nil {
		t.Error("public IP literal accepted for http")
	}
	// https with the opt-in still reaches a public host.
	if err := checkResolvedHost(ctx, lookup("93.184.216.34"), "ntfy.example.com", netPrivateAllowed); err != nil {
		t.Errorf("https + opt-in to a public host rejected: %v", err)
	}

	// Save: an http server that resolves public is refused even with the
	// opt-in; the same host over https is fine.
	s := testService(t, newFakeDB(), "")
	s.lookupHost = lookup("93.184.216.34")
	in := CreateInput{Kind: KindGotify, Name: "G", Secret: "AppTok3n", AllowPrivateNetwork: true,
		Config: Config{ServerURL: "http://gotify.example.com"}}
	_, err := s.Create(ctx, in)
	mustValidation(t, err, "public address")
	in.Config.ServerURL = "https://gotify.example.com"
	if _, err := s.Create(ctx, in); err != nil {
		t.Errorf("https + opt-in to a public host: %v", err)
	}

	// Dial: the guard runs on the resolved address before any connection,
	// so this never touches the network.
	sv := &Service{sendTimeout: 2 * time.Second}
	err = sv.do(ctx, newHTTPClient(netPrivateOnly, 2*time.Second),
		&outbound{method: "POST", url: "http://93.184.216.34:9/topic", header: http.Header{}}, "")
	if err == nil || !strings.Contains(err.Error(), "public address") {
		t.Fatalf("http dial to a public address: err = %v", err)
	}

	// Send: a row that is http with the opt-in uses the private-only
	// client; one written around the API without the opt-in never dials.
	db := newFakeDB()
	s2 := testService(t, db, "")
	id := uuid.New()
	db.agents[id] = genAgent(id, KindNtfy, nil, `{"server_url":"http://93.184.216.34","topic":"t"}`, true)
	if err := s2.send(ctx, db.agents[id], Message{Event: EventTest, Title: "t"}); err == nil || !strings.Contains(err.Error(), "public address") {
		t.Errorf("send over http to a public host: err = %v", err)
	}
	dialed := false
	s2.clientFor = func(netPolicy) *http.Client {
		dialed = true
		return nil
	}
	id2 := uuid.New()
	db.agents[id2] = genAgent(id2, KindNtfy, nil, `{"server_url":"http://ntfy.lan","topic":"t"}`, false)
	err = s2.send(ctx, db.agents[id2], Message{Event: EventTest, Title: "t"})
	var perm *permanentError
	if !errors.As(err, &perm) || !strings.Contains(err.Error(), "https") || dialed {
		t.Errorf("http row without the opt-in: err = %v, dialed = %v", err, dialed)
	}
}

// TestHTTPClients_ReusedAcrossSends: the delivery clients are built once per
// network policy and reused, so sequential sends share one keep-alive
// connection instead of each leaking a Transport, an idle connection and its
// goroutines.
func TestHTTPClients_ReusedAcrossSends(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	db := newFakeDB()
	s := testService(t, db, "") // the production clients
	id := uuid.New()
	db.agents[id] = genAgent(id, KindNtfy, nil, `{"server_url":"`+srv.URL+`","topic":"t"}`, true)
	ctx := context.Background()
	send := func() {
		t.Helper()
		if err := s.send(ctx, db.agents[id], Message{Event: EventTest, Title: "t"}); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	send() // warm up: the one connection and its goroutines
	before := runtime.NumGoroutine()
	const n = 30
	for i := 0; i < n; i++ {
		send()
	}
	if got := conns.Load(); got != 1 {
		t.Errorf("connections for %d sends = %d, want 1 (reused)", n+1, got)
	}
	if grew := runtime.NumGoroutine() - before; grew > 5 {
		t.Errorf("goroutines grew by %d over %d sends", grew, n)
	}
	for p := netPolicy(0); p < netPolicyCount; p++ {
		if c := s.clientFor(p); c == nil || c != s.clientFor(p) {
			t.Errorf("policy %d: client not built once and reused", p)
		}
	}
}
