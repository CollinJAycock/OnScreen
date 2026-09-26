package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onscreen/onscreen/internal/domain/settings"
)

// A settings PATCH may not re-point SMTP / LDAP / OIDC at a new host while
// keeping the stored secret (the UI round-trips the mask), or the next test
// email / bind / token exchange hands the secret to that host.
func TestEndpointMovesStoredSecret(t *testing.T) {
	s := func(v string) *string { return &v }
	cases := []struct {
		name      string
		cur       string
		next      *string
		curSecret string
		newSecret *string
		want      bool
	}{
		{"host untouched", "smtp.example.com", nil, "pw", nil, false},
		{"same host, mask round-tripped", "smtp.example.com", s("SMTP.example.com/"), "pw", s(maskedSecret), false},
		{"new host, mask round-tripped", "smtp.example.com", s("evil.example"), "pw", s(maskedSecret), true},
		{"new host, secret omitted", "smtp.example.com", s("evil.example"), "pw", nil, true},
		{"new host, secret re-entered", "smtp.example.com", s("relay2.example"), "pw", s("new-pw"), false},
		{"new host, secret cleared", "smtp.example.com", s("relay2.example"), "pw", s(""), false},
		{"new host, nothing stored", "smtp.example.com", s("relay2.example"), "", nil, false},
	}
	for _, c := range cases {
		if got := endpointMovesStoredSecret(c.cur, c.next, c.curSecret, c.newSecret); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// The SMTP endpoint is host:port — a PATCH that only re-points the port moves
// the stored password just as a new host does.
func TestNextSMTPEndpoint(t *testing.T) {
	s := func(v string) *string { return &v }
	i := func(v int) *int { return &v }
	cur := settings.SMTPConfig{Host: "smtp.example.com", Port: 587}
	curEP := smtpEndpoint(cur.Host, cur.Port)
	cases := []struct {
		name string
		host *string
		port *int
		want bool // endpoint moved
	}{
		{"neither touched", nil, nil, false},
		{"same host and port", s("smtp.example.com"), i(587), false},
		{"port only changed", nil, i(2525), true},
		{"same host, new port", s("smtp.example.com"), i(25), true},
		{"new host, same port", s("evil.example"), i(587), true},
	}
	for _, c := range cases {
		got := endpointMovesStoredSecret(curEP, nextSMTPEndpoint(cur, c.host, c.port), "pw", nil)
		if got != c.want {
			t.Errorf("%s: moved=%v, want %v", c.name, got, c.want)
		}
	}
}

// Turning off LDAP TLS (or its certificate check) on the same host exposes the
// stored bind password as surely as re-pointing the host.
func TestLDAPTLSDowngraded(t *testing.T) {
	b := func(v bool) *bool { return &v }
	cases := []struct {
		name                   string
		cur                    settings.LDAPConfig
		startTLS, ldaps, skipV *bool
		want                   bool
	}{
		{"nothing touched", settings.LDAPConfig{UseLDAPS: true}, nil, nil, nil, false},
		{"LDAPS switched off", settings.LDAPConfig{UseLDAPS: true}, nil, b(false), nil, true},
		{"StartTLS switched off", settings.LDAPConfig{StartTLS: true}, b(false), nil, nil, true},
		{"LDAPS to StartTLS", settings.LDAPConfig{UseLDAPS: true}, b(true), b(false), nil, false},
		{"one of two TLS flags off", settings.LDAPConfig{UseLDAPS: true, StartTLS: true}, b(false), nil, nil, false},
		{"verification switched off", settings.LDAPConfig{UseLDAPS: true}, nil, nil, b(true), true},
		{"verification already off", settings.LDAPConfig{UseLDAPS: true, SkipTLSVerify: true}, nil, nil, b(true), false},
		{"verification switched on", settings.LDAPConfig{UseLDAPS: true, SkipTLSVerify: true}, nil, nil, b(false), false},
		{"plaintext stays plaintext", settings.LDAPConfig{}, b(false), b(false), b(true), false},
		{"plaintext to TLS", settings.LDAPConfig{}, nil, b(true), nil, false},
	}
	for _, c := range cases {
		if got := ldapTLSDowngraded(c.cur, c.startTLS, c.ldaps, c.skipV); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// Handler level: the refusals fire before anything is written, and re-entering
// the secret lets the same change through.
func TestSettings_Update_SecretFollowsSMTPPortAndLDAPTLS(t *testing.T) {
	patch := func(svc *mockSettingsService, body string) int {
		rec := httptest.NewRecorder()
		newSettingsHandler(svc).Update(rec, httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(body)))
		return rec.Code
	}

	smtpSvc := &mockSettingsService{smtp: settings.SMTPConfig{Host: "smtp.example.com", Port: 587, Password: "pw"}}
	if got := patch(smtpSvc, `{"smtp":{"host":"smtp.example.com","port":2525,"password":"****"}}`); got != http.StatusUnprocessableEntity {
		t.Errorf("SMTP port change with mask: got %d, want 422", got)
	}
	if smtpSvc.smtp.Port != 587 || smtpSvc.smtp.Password != "pw" {
		t.Errorf("refused SMTP update was applied: %+v", smtpSvc.smtp)
	}
	if got := patch(smtpSvc, `{"smtp":{"host":"smtp.example.com","port":587,"password":"****","from":"a@b.c"}}`); got != http.StatusNoContent {
		t.Errorf("SMTP unchanged endpoint with mask: got %d, want 204", got)
	}
	if got := patch(smtpSvc, `{"smtp":{"port":2525,"password":"new-pw"}}`); got != http.StatusNoContent || smtpSvc.smtp.Port != 2525 {
		t.Errorf("SMTP port change with re-entered password: got %d port=%d, want 204 and 2525", got, smtpSvc.smtp.Port)
	}

	ldapSvc := &mockSettingsService{ldap: settings.LDAPConfig{Host: "ldap.example.com:636", UseLDAPS: true, BindPassword: "pw"}}
	if got := patch(ldapSvc, `{"ldap":{"host":"ldap.example.com:636","use_ldaps":false,"bind_password":"****"}}`); got != http.StatusUnprocessableEntity {
		t.Errorf("LDAPS off with mask: got %d, want 422", got)
	}
	if got := patch(ldapSvc, `{"ldap":{"skip_tls_verify":true}}`); got != http.StatusUnprocessableEntity {
		t.Errorf("skip verify on, password omitted: got %d, want 422", got)
	}
	if !ldapSvc.ldap.UseLDAPS || ldapSvc.ldap.SkipTLSVerify {
		t.Errorf("refused LDAP update was applied: %+v", ldapSvc.ldap)
	}
	if got := patch(ldapSvc, `{"ldap":{"use_ldaps":false,"bind_password":"new-pw"}}`); got != http.StatusNoContent || ldapSvc.ldap.UseLDAPS {
		t.Errorf("LDAPS off with re-entered password: got %d use_ldaps=%v, want 204 and false", got, ldapSvc.ldap.UseLDAPS)
	}
}
