package v1

import (
	"strings"
	"testing"
)

// parseGooseVersion is a small text parser; exercise its happy path,
// the alternate (unschema-prefixed) COPY header, mixed applied/unapplied
// rows, and the "no data" failure modes.

func TestParseGooseVersion_HappyPath(t *testing.T) {
	in := `--
-- PostgreSQL database dump
--

COPY public.goose_db_version (id, version_id, is_applied, tstamp) FROM stdin;
1	0	t	2026-01-01 00:00:00
2	1	t	2026-01-01 00:00:01
3	2	t	2026-01-01 00:00:02
4	5	t	2026-01-01 00:00:03
\.

--
-- end
--
`
	got, err := parseGooseVersion([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 5 {
		t.Errorf("got version %d, want 5", got)
	}
}

func TestParseGooseVersion_UnschemaPrefixed(t *testing.T) {
	in := `COPY goose_db_version (id, version_id, is_applied, tstamp) FROM stdin;
1	40	t	2026-04-22 00:00:00
\.
`
	got, err := parseGooseVersion([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 40 {
		t.Errorf("got version %d, want 40", got)
	}
}

func TestParseGooseVersion_IgnoresUnapplied(t *testing.T) {
	// goose records rollbacks by writing an is_applied=f row; only the
	// most recent applied row counts as the "current" version.
	in := `COPY public.goose_db_version (id, version_id, is_applied, tstamp) FROM stdin;
1	10	t	2026-01-01 00:00:00
2	11	t	2026-01-02 00:00:00
3	11	f	2026-01-03 00:00:00
\.
`
	got, err := parseGooseVersion([]byte(in))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The rollback row is is_applied=f and is skipped, so 11 still
	// counts as the highest applied (the prior is_applied=t row).
	if got != 11 {
		t.Errorf("got version %d, want 11 (rollback row should be ignored)", got)
	}
}

func TestParseGooseVersion_NoTable(t *testing.T) {
	in := `--
-- nothing to see here, no goose_db_version COPY block
--
COPY public.something_else (id) FROM stdin;
1
\.
`
	_, err := parseGooseVersion([]byte(in))
	if err == nil {
		t.Fatal("expected error when goose_db_version block missing")
	}
	if !strings.Contains(err.Error(), "goose_db_version") {
		t.Errorf("error should mention goose_db_version, got: %v", err)
	}
}

func TestParseGooseVersion_TableEmpty(t *testing.T) {
	in := `COPY public.goose_db_version (id, version_id, is_applied, tstamp) FROM stdin;
\.
`
	_, err := parseGooseVersion([]byte(in))
	if err == nil {
		t.Fatal("expected error when table empty")
	}
	if !strings.Contains(err.Error(), "no applied rows") {
		t.Errorf("error should mention 'no applied rows', got: %v", err)
	}
}
