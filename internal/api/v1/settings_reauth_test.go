package v1

import "testing"

func TestRewriteHostToLAN(t *testing.T) {
	const lan = "10.0.0.5"
	cases := []struct {
		name, in, want string
	}{
		{
			"database_url localhost",
			"postgres://postgres:s3cret@localhost:5432/onscreen?sslmode=disable",
			"postgres://postgres:s3cret@10.0.0.5:5432/onscreen?sslmode=disable",
		},
		{
			"database_url 127.0.0.1",
			"postgres://postgres:s3cret@127.0.0.1:5432/onscreen?sslmode=disable",
			"postgres://postgres:s3cret@10.0.0.5:5432/onscreen?sslmode=disable",
		},
		{
			"valkey_url localhost",
			"redis://localhost:6379",
			"redis://10.0.0.5:6379",
		},
		{
			"valkey_url 127.0.0.1",
			"redis://127.0.0.1:6379",
			"redis://10.0.0.5:6379",
		},
		{
			"already a remote host is untouched",
			"postgres://postgres:s3cret@db.lan:5432/onscreen",
			"postgres://postgres:s3cret@db.lan:5432/onscreen",
		},
	}
	for _, c := range cases {
		if got := rewriteHostToLAN(c.in, lan); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	// No LAN IP detected → leave the DSN as-is (UI shows the placeholder host).
	const ph = "postgres://postgres:s3cret@localhost:5432/onscreen"
	if got := rewriteHostToLAN(ph, ""); got != ph {
		t.Errorf("empty lan should be a no-op, got %q", got)
	}
}
