package v1

import (
	"crypto/tls"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/settings"
)

// Item 11: StartTLS upgrades an open connection with tls.Client, which (unlike
// a TLS dial) never fills ServerName from the address — so without one every
// StartTLS handshake failed unless certificate checks were off. The config now
// names the configured host, port stripped, for StartTLS and LDAPS alike.
func TestLDAPTLSConfig_ServerName(t *testing.T) {
	for _, tc := range []struct {
		host, want string
	}{
		{"ldap.corp.example:389", "ldap.corp.example"},
		{"ldap.corp.example:636", "ldap.corp.example"},
		{"ldap.corp.example", "ldap.corp.example"},
		{" dc1.ad.example:389 ", "dc1.ad.example"},
		{"10.0.0.5:389", "10.0.0.5"},
		{"[fd00::5]:636", "fd00::5"},
		{"[fd00::5]", "fd00::5"},
	} {
		c := ldapTLSConfig(settings.LDAPConfig{Host: tc.host, StartTLS: true})
		if c.ServerName != tc.want {
			t.Errorf("host %q: ServerName = %q, want %q", tc.host, c.ServerName, tc.want)
		}
		if c.InsecureSkipVerify {
			t.Errorf("host %q: verification off without skip_tls_verify", tc.host)
		}
		if c.MinVersion < tls.VersionTLS12 {
			t.Errorf("host %q: MinVersion %x", tc.host, c.MinVersion)
		}
	}

	// skip_tls_verify stays the admin's explicit opt-in.
	if c := ldapTLSConfig(settings.LDAPConfig{Host: "ldap:389", UseLDAPS: true, SkipTLSVerify: true}); !c.InsecureSkipVerify {
		t.Error("skip_tls_verify not honoured")
	}

	// Each connection gets its own config.
	cfg := settings.LDAPConfig{Host: "ldap:389"}
	if ldapTLSConfig(cfg) == ldapTLSConfig(cfg) {
		t.Error("configs are shared between connections")
	}
}
