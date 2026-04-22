package plugin

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// allowPrivateIPs swaps the loopback/private rejection out for the duration of
// the test. Required because httptest.NewServer binds 127.0.0.1, which the
// production policy rejects.
func allowPrivateIPs(t *testing.T) {
	t.Helper()
	prev := isNonPublicIP
	isNonPublicIP = func(net.IP) bool { return false }
	t.Cleanup(func() { isNonPublicIP = prev })
}

func TestNewAllowedHostSet_IncludesEndpointHost(t *testing.T) {
	set, err := newAllowedHostSet("https://hooks.example.com/mcp", nil)
	if err != nil {
		t.Fatalf("new allowed set: %v", err)
	}
	if !set.allows("hooks.example.com") {
		t.Errorf("endpoint host should be implicitly allowed")
	}
	if !set.allows("HOOKS.EXAMPLE.COM") {
		t.Errorf("host comparison must be case-insensitive")
	}
	if set.allows("evil.example.com") {
		t.Errorf("non-allowlisted host must be rejected")
	}
}

func TestHttpClientForPlugin_RejectsHostNotOnAllowlist(t *testing.T) {
	allowPrivateIPs(t)
	p := Plugin{
		ID:           uuid.New(),
		Name:         "test",
		EndpointURL:  "https://allowed.example.com/mcp",
		AllowedHosts: nil,
	}
	c, err := httpClientForPlugin(p)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	// Dial directly so we surface the dialer error, not an HTTP layer one.
	transport := c.Transport.(*http.Transport)
	_, err = transport.DialContext(context.Background(), "tcp", "evil.example.com:443")
	if err == nil {
		t.Fatalf("expected egress error, got nil")
	}
	if !strings.Contains(err.Error(), "not in plugin") {
		t.Errorf("expected allowlist error, got %v", err)
	}
}

func TestHttpClientForPlugin_RejectsLoopbackUnderProductionPolicy(t *testing.T) {
	// No allowPrivateIPs — exercise the real DNS guard.
	p := Plugin{
		ID:          uuid.New(),
		Name:        "test",
		EndpointURL: "http://localhost:9999/mcp",
	}
	c, err := httpClientForPlugin(p)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	transport := c.Transport.(*http.Transport)
	_, err = transport.DialContext(context.Background(), "tcp", "localhost:9999")
	if err == nil {
		t.Fatalf("expected loopback rejection, got nil")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Errorf("expected non-public-address error, got %v", err)
	}
}

func TestHttpClientForPlugin_RejectsRedirects(t *testing.T) {
	p := Plugin{
		ID:          uuid.New(),
		Name:        "test",
		EndpointURL: "https://allowed.example.com/mcp",
	}
	c, err := httpClientForPlugin(p)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	if c.CheckRedirect == nil {
		t.Fatal("CheckRedirect must be set so MCP redirects fail closed")
	}
	req, _ := http.NewRequest("GET", "https://evil.example.com/", nil)
	if err := c.CheckRedirect(req, nil); err == nil {
		t.Errorf("CheckRedirect should refuse all redirects")
	}
}

func TestHttpClientForPlugin_HasHardTimeout(t *testing.T) {
	p := Plugin{
		ID:          uuid.New(),
		Name:        "test",
		EndpointURL: "https://allowed.example.com/mcp",
	}
	c, err := httpClientForPlugin(p)
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	// Outer safety net: context deadlines are primary, but the client-level
	// timeout exists so a caller that forgets one still can't hang forever.
	if c.Timeout == 0 {
		t.Error("http.Client.Timeout must be set as outer safety net")
	}
}
