package v1

import (
	"net/http/httptest"
	"testing"
)

// First-run setup must refuse a Host an attacker could rebind to the server's
// LAN address, while every way an operator addresses a box on their own
// network keeps working.
func TestSetupHostAllowed(t *testing.T) {
	allowed := []string{
		"192.168.1.10:7070", "10.0.0.66", "[::1]:7070", "[fe80::1]", "127.0.0.1",
		"localhost:7070", "nas", "NAS:7070", "onscreen.local", "media.home.arpa",
		"box.internal", "server.lan:8080", "tv.home", "app.localhost", "onscreen.local.",
	}
	for _, h := range allowed {
		r := httptest.NewRequest("POST", "/api/v1/auth/register", nil)
		r.Host = h
		if !setupHostAllowed(r) {
			t.Errorf("host %q refused, want allowed", h)
		}
	}
	refused := []string{
		"attacker.example", "attacker.example:7070", "rebind.evil.com",
		"onscreen.example.com", "local.evil.com", "lan.attacker.net", "",
	}
	for _, h := range refused {
		r := httptest.NewRequest("POST", "/api/v1/auth/register", nil)
		r.Host = h
		if setupHostAllowed(r) {
			t.Errorf("host %q allowed during setup, want refused (rebindable)", h)
		}
	}
}
