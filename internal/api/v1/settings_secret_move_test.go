package v1

import "testing"

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
