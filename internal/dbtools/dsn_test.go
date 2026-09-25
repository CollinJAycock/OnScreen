package dbtools

import (
	"strings"
	"testing"
)

func TestCommandDSN_MovesPasswordToEnv(t *testing.T) {
	clean, env := CommandDSN("postgres://onscreen:s3cret@db:5432/onscreen?sslmode=disable")
	if strings.Contains(clean, "s3cret") {
		t.Fatalf("password must not remain in the DSN passed on argv: %s", clean)
	}
	if !strings.Contains(clean, "onscreen@db:5432/onscreen") || !strings.Contains(clean, "sslmode=disable") {
		t.Errorf("DSN otherwise changed unexpectedly: %s", clean)
	}
	found := false
	for _, e := range env {
		if e == "PGPASSWORD=s3cret" {
			found = true
		}
	}
	if !found {
		t.Error("expected PGPASSWORD in the child environment")
	}
}

func TestCommandDSN_NoPasswordUnchanged(t *testing.T) {
	in := "postgres://onscreen@db/onscreen"
	if clean, _ := CommandDSN(in); clean != in {
		t.Errorf("got %q, want unchanged %q", clean, in)
	}
	kv := "host=db user=onscreen dbname=onscreen"
	if clean, _ := CommandDSN(kv); clean != kv {
		t.Errorf("key/value DSN must pass through unchanged; got %q", clean)
	}
}
