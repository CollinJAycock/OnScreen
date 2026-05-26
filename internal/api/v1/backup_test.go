package v1

import (
	"errors"
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

// classifyRestoreOutcome filters out the cosmetic "cannot drop inherited
// constraint" errors `pg_restore --clean --if-exists` emits once per child
// of every partitioned table — they don't break anything but they show up
// as exit_error in the admin response and look like failure. The classifier
// only suppresses runs where every counted error matches that signature.

func TestClassifyRestoreOutcome_NilErrIsPassthrough(t *testing.T) {
	// A clean run shouldn't be touched.
	gotStderr, suppressed, got := classifyRestoreOutcome(nil, "")
	if got != nil || gotStderr != "" || suppressed != 0 {
		t.Errorf("got (%v,%q,%d), want (nil,\"\",0)", got, gotStderr, suppressed)
	}
}

func TestClassifyRestoreOutcome_BenignOnlyClearsError(t *testing.T) {
	// Six "cannot drop inherited constraint" errors and the matching
	// summary line — exactly what the partitioned watch_events tables
	// produce on every restore. exit_error and stderr should be cleared.
	stderr := strings.Repeat(
		"pg_restore: error: could not execute query: ERROR:  cannot drop inherited constraint \"watch_events_2026_03_pkey\" of relation \"watch_events_2026_03\"\n"+
			"Command was: ALTER TABLE IF EXISTS ONLY public.watch_events_2026_03 DROP CONSTRAINT IF EXISTS watch_events_2026_03_pkey;\n",
		6) +
		"pg_restore: warning: errors ignored on restore: 6\n"

	gotStderr, suppressed, got := classifyRestoreOutcome(errors.New("exit status 1"), stderr)
	if got != nil {
		t.Errorf("benign-only run should clear runErr; got %v", got)
	}
	if gotStderr != "" {
		t.Errorf("benign-only run should clear stderr; got %q", gotStderr)
	}
	if suppressed != 6 {
		t.Errorf("suppressed count: got %d want 6", suppressed)
	}
}

func TestClassifyRestoreOutcome_MixedKeepsRealError(t *testing.T) {
	// One benign error and one genuine error should NOT be suppressed —
	// the operator needs to see the real one. Total error count is 2, but
	// only 1 is benign, so the classifier bails out.
	stderr := "pg_restore: error: could not execute query: ERROR:  cannot drop inherited constraint \"x_pkey\" of relation \"x\"\n" +
		"Command was: ALTER TABLE IF EXISTS ONLY public.x DROP CONSTRAINT IF EXISTS x_pkey;\n" +
		"pg_restore: error: could not execute query: ERROR:  relation \"missing_table\" does not exist\n" +
		"Command was: ALTER TABLE IF EXISTS ONLY public.missing_table OWNER TO postgres;\n" +
		"pg_restore: warning: errors ignored on restore: 2\n"

	runErr := errors.New("exit status 1")
	gotStderr, suppressed, got := classifyRestoreOutcome(runErr, stderr)
	if got == nil {
		t.Error("mixed errors must keep runErr; got nil")
	}
	if gotStderr == "" {
		t.Error("mixed errors must keep stderr so the real failure is visible")
	}
	if suppressed != 0 {
		t.Errorf("mixed errors must not report a suppressed count; got %d", suppressed)
	}
}

func TestClassifyRestoreOutcome_NoSummaryIsRealFailure(t *testing.T) {
	// pg_restore bailing before its summary line (e.g. connection refused,
	// malformed dump) is a real failure — keep everything intact regardless
	// of whether the partial stderr happens to contain the benign signature.
	stderr := "pg_restore: error: could not execute query: ERROR:  cannot drop inherited constraint \"x_pkey\" of relation \"x\"\n" +
		"pg_restore: error: could not connect to database\n"

	runErr := errors.New("exit status 1")
	gotStderr, suppressed, got := classifyRestoreOutcome(runErr, stderr)
	if got == nil {
		t.Error("no summary line → must keep runErr")
	}
	if gotStderr != stderr {
		t.Error("no summary line → must keep stderr intact")
	}
	if suppressed != 0 {
		t.Errorf("no summary line → suppressed must be 0; got %d", suppressed)
	}
}

func TestClassifyRestoreOutcome_NonBenignOnly(t *testing.T) {
	// pg_restore reported errors but none were the partition-inheritance
	// kind — surface everything verbatim, suppressed=0.
	stderr := "pg_restore: error: could not execute query: ERROR:  permission denied for table foo\n" +
		"Command was: COPY public.foo (id) FROM stdin;\n" +
		"pg_restore: warning: errors ignored on restore: 1\n"

	runErr := errors.New("exit status 1")
	gotStderr, suppressed, got := classifyRestoreOutcome(runErr, stderr)
	if got == nil || gotStderr == "" || suppressed != 0 {
		t.Errorf("non-benign errors must pass through: got (%v,%q,%d)", got, gotStderr, suppressed)
	}
}
